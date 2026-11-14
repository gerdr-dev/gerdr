package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"

	"github.com/gerdr-dev/gerdr/internal/folder"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// hostCpCmd: `gerdr host cp` copies files and folders between this computer and one it drives, like scp. Before it an agent
// piped one file at a time through `gerdr host exec --stdin -- 'cat > …'` (its own quoting, no folders, no check that
// 45 MB arrived whole) or borrowed a conversation (`session send --file`, which wakes the agent there up).
//
// Nothing new on the other side: the routes artifact folders use (docs/dev/MULTIHOST.md "Artifact folders"):
// GET /api/fs/stat?hash=1 and /api/fs/tree?all=1 to see what is there (and skip what already matches),
// PUT /api/fs/file to write (tmp file + rename, answers the sha256 of what it received), /api/fs/raw to read. Every
// file is checked by sha256 against the sending side, so a cut or mangled transfer is an error, never a quiet success.
// Not auto-approved (sysenv.SafeGerdrCLI): it writes wherever it is told, on either computer.
func hostCpCmd() *cobra.Command {
	var excludes []string
	var jobs int
	cmd := &cobra.Command{
		Use:   i18n.S("cp <源>... <目标>", "cp <source>... <dest>"),
		Short: i18n.S("在这台和它控制的另一台电脑之间拷文件 / 目录（<电脑>:<路径> 是那边的），每个文件都核对 sha256", "Copy files / folders between this computer and one it controls (<host>:<path> is over there); every file checked by sha256"),
		Long: i18n.S(
			"像 scp：一边是这台的路径，一边是 <电脑>:<路径>（电脑是 gerdr host list 的 id；路径是那边的绝对路径、'~/…'，\n"+
				"或相对那边家目录的路径）。目录整个拷，不用 -r；隐藏文件、.git 都拷，不要的用 --exclude（.gerdrignore 的写法）。\n"+
				"目标怎么算（重复跑同一条命令结果一样）：\n"+
				"  - 目标以 / 结尾、是 . / ~ ，或源有好几个：放进这个目录，用原来的名字。\n"+
				"  - 源是文件、目标是已有目录：也放进去；否则目标就是新文件名。\n"+
				"  - 源是目录：目标就是拷出来的那个目录（没有就建，有就往里合并、同名文件覆盖），不会再套一层。\n"+
				"已经一样的文件（sha256 相同）跳过，所以断了重跑只补没传完的。可执行位跟着走。\n"+
				"每个文件到了以后核对 sha256，对不上重传一次，还不对就报错、退出码 1。目标那边从不删文件。\n"+
				"结束打一段 JSON（copied / skipped / failed / bytes）；终端里会显示进度。",
			"Like scp: one side is a path here, the other <host>:<path> (host: an id from gerdr host list; path: absolute\n"+
				"on that computer, '~/…', or relative to its home). Folders are copied whole, no -r; hidden files and .git\n"+
				"included, leave things out with --exclude (.gerdrignore syntax).\n"+
				"Where things land (running the same command again gives the same result):\n"+
				"  - dest ends in /, is . or ~, or there are several sources: into that folder, under the original names.\n"+
				"  - a file onto an existing folder: into it too; otherwise dest is the new file's name.\n"+
				"  - a folder: dest IS the copy (made if missing, merged into if there, same names overwritten), never one\n"+
				"    level deeper.\n"+
				"Files already the same (sha256) are skipped, so re-running after a break only sends what's missing. The\n"+
				"executable bit goes along. Every file's sha256 is checked on arrival; a mismatch is sent once more, then an\n"+
				"error (exit code 1). Nothing is ever deleted on the receiving side.\n"+
				"Prints JSON at the end (copied / skipped / failed / bytes); progress shows in a terminal."),
		Example: "  gerdr host list\n" +
			"  gerdr host cp ./build/app mini:'~/bin/'            # → ~/bin/app on mini\n" +
			"  gerdr host cp ./dist mini:'~/www/site'             # the folder dist becomes ~/www/site\n" +
			"  gerdr host cp ./src ./go.mod mini:'~/work/app/' --exclude node_modules\n" +
			"  gerdr host cp mini:'~/logs/today.log' .\n" +
			"  gerdr host cp mini:/var/data/run-42 ./results/     # → ./results/run-42",
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			return c.copy(args, excludes, max(jobs, 1))
		},
	}
	cmd.Flags().StringArrayVar(&excludes, "exclude", nil, i18n.S("拷目录时跳过的（名字、从根开始的 /路径、通配；可重复）", "skip these when copying a folder (a name, a /path from its root, globs; repeatable)"))
	cmd.Flags().IntVar(&jobs, "jobs", 4, i18n.S("同时传几个文件", "files sent at the same time"))
	return cmd
}

var hostIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]+$`)

// cpSpec: one side of a copy. host "" = this computer.
type cpSpec struct {
	host, path string
	into       bool // written as a folder to go into: ends in /, or . / .. / ~ / empty
}

func parseSpec(a string) cpSpec {
	if h, p, ok := strings.Cut(a, ":"); ok && hostIDRe.MatchString(h) {
		if _, err := os.Lstat(a); err != nil { // a file here that happens to be called "x:y" stays a file here
			return cpSpec{host: h, path: p, into: p == "" || p == "~" || p == "." || strings.HasSuffix(p, "/")}
		}
	}
	return cpSpec{path: a, into: a == "." || a == ".." || strings.HasSuffix(a, "/") || strings.HasSuffix(a, string(filepath.Separator))}
}

// cpJob: one file to send. sha: the source's, when known before sending (a pull checks against it; a push hashes
// what it reads and compares with what arrived).
type cpJob struct {
	host          string
	local, remote string
	size          int64
	sha           string
	mode          uint32
}

type remoteStat struct {
	Dir  bool    `json:"dir"`
	Size int64   `json:"size"`
	Sha  string  `json:"sha256"`
	Mode *uint32 `json:"mode"` // nil: an older Gerdr
}

func (c *apiClient) copy(args []string, excludes []string, jobs int) error {
	if len(args) == 1 {
		args = append(args, ".")
	}
	srcs, dst := make([]cpSpec, 0, len(args)-1), parseSpec(args[len(args)-1])
	for _, a := range args[:len(args)-1] {
		s := parseSpec(a)
		if (s.host == "") == (dst.host == "") {
			return errors.New(i18n.S("一边要是这台的路径，另一边是 <电脑>:<路径>（两边都是这台就用 cp；两台别的电脑之间不行）", "one side must be a path here and the other <host>:<path> (both here: use cp; between two other computers: not supported)"))
		}
		srcs = append(srcs, s)
	}
	into := dst.into || len(srcs) > 1
	t := &cpRun{c: c, push: dst.host != "", excl: excludes, home: map[string]string{}}
	var plan []cpJob
	skipped := 0
	if t.push {
		t.host = dst.host
		root, err := t.remoteAbs(dst.host, dst.path)
		if err != nil {
			return err
		}
		t.to = dst.host + ":" + root
		dstSt, err := t.stat(dst.host, root, false)
		if err != nil {
			return err
		}
		for _, s := range srcs {
			abs, err := filepath.Abs(s.path)
			if err != nil {
				return err
			}
			st, err := os.Stat(abs)
			if err != nil {
				return err
			}
			target := root
			if into || (!st.IsDir() && dstSt != nil && dstSt.Dir) {
				target = path.Join(root, filepath.Base(abs))
			}
			if !st.IsDir() {
				there, err := t.stat(dst.host, target, true)
				if err != nil {
					return err
				}
				sha, err := folder.Hash(abs, st)
				if err != nil {
					return err
				}
				if there != nil && there.Dir {
					return fmt.Errorf(i18n.S("%s:%s 是个目录", "%s:%s is a folder"), dst.host, target)
				}
				if there != nil && there.Sha == sha {
					skipped++
					continue
				}
				plan = append(plan, cpJob{host: t.host, local: abs, remote: target, size: st.Size(), sha: sha, mode: uint32(st.Mode().Perm())})
				continue
			}
			files, err := folder.WalkWith(abs, folder.Options{All: true, Exclude: excludes})
			if err != nil {
				return tooMany(err)
			}
			there, _, err := t.tree(dst.host, target)
			if err != nil {
				return err
			}
			for _, e := range files {
				if there[e.Path] == e.Sha {
					skipped++
					continue
				}
				plan = append(plan, cpJob{host: t.host, local: filepath.Join(abs, filepath.FromSlash(e.Path)), remote: target + "/" + e.Path, size: e.Size, sha: e.Sha, mode: e.Mode})
			}
		}
	} else {
		dabs, err := filepath.Abs(dst.path)
		if err != nil {
			return err
		}
		t.to = dabs
		dstSt, _ := os.Stat(dabs)
		for _, s := range srcs {
			t.host = s.host
			root, err := t.remoteAbs(s.host, s.path)
			if err != nil {
				return err
			}
			st, err := t.stat(s.host, root, true)
			if err != nil {
				return err
			}
			if st == nil {
				return fmt.Errorf(i18n.S("%s 上没有 %s", "no %[2]s on %[1]s"), s.host, root)
			}
			target := dabs
			if into || (!st.Dir && dstSt != nil && dstSt.IsDir()) {
				target = filepath.Join(dabs, path.Base(root))
			}
			if !st.Dir {
				if sameHere(target, st.Sha) {
					skipped++
					continue
				}
				m := uint32(0)
				if st.Mode != nil {
					m = *st.Mode
				}
				plan = append(plan, cpJob{host: t.host, local: target, remote: root, size: st.Size, sha: st.Sha, mode: m})
				continue
			}
			files, all, err := t.treeList(s.host, root)
			if err != nil {
				return err
			}
			if !all {
				fmt.Fprintf(os.Stderr, i18n.S("[gerdr host cp：%s 上的 Gerdr 太旧，.git、node_modules 和 .gerdrignore 里的没拷：让它升级]\n", "[gerdr host cp: the Gerdr on %s is old, so .git, node_modules and what .gerdrignore names were left out: update it]\n"), s.host)
			}
			for _, e := range files {
				local := filepath.Join(target, filepath.FromSlash(e.Path))
				if sameHere(local, e.Sha) {
					skipped++
					continue
				}
				plan = append(plan, cpJob{host: t.host, local: local, remote: root + "/" + e.Path, size: e.Size, sha: e.Sha, mode: e.Mode})
			}
		}
	}
	return t.run(plan, skipped, jobs)
}

func tooMany(err error) error {
	if errors.Is(err, folder.ErrTooMany) || strings.Contains(err.Error(), "too many files") {
		return errors.New(i18n.S("文件太多（超过 20000 个）：用 --exclude 去掉大目录，或者先打个包", "too many files (over 20000): leave big folders out with --exclude, or pack it first"))
	}
	return err
}

// sameHere: a file here already has this content.
func sameHere(p, sha string) bool {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() || sha == "" {
		return false
	}
	h, err := folder.Hash(p, st)
	return err == nil && h == sha
}

type cpRun struct {
	c    *apiClient
	push bool
	host string // the other computer (a pull may name several; the last, for the summary)
	to   string
	excl []string
	home map[string]string

	total, done atomic.Int64
	files       atomic.Int64
	modeLost    atomic.Bool
}

// remoteAbs: a path on another computer, absolute there ("", "~", "~/x", "x" = under its home).
func (t *cpRun) remoteAbs(host, p string) (string, error) {
	if !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "~") {
		p = "~/" + p
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, ok := t.home[host]
		if !ok {
			h, err := t.c.hostCwd(host, "~")
			if err != nil {
				return "", err
			}
			home, t.home[host] = h, h
		}
		p = home + p[1:]
	}
	if p != "/" {
		p = strings.TrimRight(p, "/")
	}
	return path.Clean(p), nil
}

// stat of a path on another computer; nil when there is nothing there.
func (t *cpRun) stat(host, p string, withHash bool) (*remoteStat, error) {
	q := url.Values{"host": {host}, "path": {p}}
	if withHash {
		q.Set("hash", "1")
	}
	var st remoteStat
	code, err := t.c.raw("GET", "/api/fs/stat?"+q.Encode(), nil, 0, &st)
	if code == 404 {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s:%s: %w", host, p, err)
	}
	return &st, nil
}

func (t *cpRun) treeList(host, root string) ([]folder.Entry, bool, error) {
	q := url.Values{"host": {host}, "path": {root}, "all": {"1"}, "exclude": t.excl}
	var out struct {
		Files []folder.Entry `json:"files"`
		All   bool           `json:"all"`
	}
	code, err := t.c.raw("GET", "/api/fs/tree?"+q.Encode(), nil, 0, &out)
	if code == 404 {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("%s:%s: %w", host, root, tooMany(err))
	}
	return out.Files, out.All, nil
}

// tree: what a folder over there already holds, path → sha256 (empty when it isn't there yet).
func (t *cpRun) tree(host, root string) (map[string]string, bool, error) {
	files, all, err := t.treeList(host, root)
	m := map[string]string{}
	for _, e := range files {
		m[e.Path] = e.Sha
	}
	return m, all, err
}

type cpFailed struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

func (t *cpRun) run(plan []cpJob, skipped, jobs int) error {
	for _, j := range plan {
		t.total.Add(j.size)
	}
	start := time.Now()
	stop := t.progress(len(plan))
	var mu sync.Mutex
	var copied []string
	var failed []cpFailed
	ch := make(chan cpJob)
	var wg sync.WaitGroup
	for range min(jobs, max(len(plan), 1)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				err := t.one(j)
				if err != nil && !errors.Is(err, errNoRetry) {
					err = t.one(j) // once more: a file changed meanwhile, a dropped connection
				}
				t.files.Add(1)
				mu.Lock()
				name := j.remote
				if !t.push {
					name = j.local
				}
				if err != nil {
					failed = append(failed, cpFailed{name, err.Error()})
				} else {
					copied = append(copied, name)
				}
				mu.Unlock()
			}
		}()
	}
	for _, j := range plan {
		ch <- j
	}
	close(ch)
	wg.Wait()
	stop()
	if t.modeLost.Load() {
		fmt.Fprintf(os.Stderr, i18n.S("[gerdr host cp：%s 上的 Gerdr 太旧，可执行位没带过去：让它升级，或者 gerdr host exec 去 chmod +x]\n", "[gerdr host cp: the Gerdr on %s is old and dropped the executable bit: update it, or chmod +x with gerdr host exec]\n"), t.host)
	}
	out := map[string]any{"to": t.to, "copied": len(copied), "skipped": skipped, "bytes": t.done.Load(), "verified": "sha256", "ms": time.Since(start).Milliseconds()}
	if len(copied) <= 50 {
		out["files"] = orEmptyS(copied)
	}
	if len(failed) > 0 {
		out["failed"] = failed
	}
	printJSON(out)
	if len(failed) > 0 {
		os.Exit(1)
	}
	return nil
}

func orEmptyS(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

var errNoRetry = errors.New("no retry")

// one sends one file and checks it arrived as it left.
func (t *cpRun) one(j cpJob) error {
	var mine int64 // this try's bytes: taken back off the progress if it fails, the next try counts them again
	err := t.oneTry(j, &mine)
	if err != nil {
		t.done.Add(-mine)
	}
	return err
}

func (t *cpRun) oneTry(j cpJob, mine *int64) error {
	if t.push {
		f, err := os.Open(j.local)
		if err != nil {
			return fmt.Errorf("%w: %w", errNoRetry, err)
		}
		defer f.Close()
		h := newHashCounter()
		q := url.Values{"host": {j.host}, "path": {j.remote}, "ifMatch": {"*"}, "mode": {fmt.Sprintf("%o", j.mode)}}
		var res struct {
			Sha  string  `json:"sha256"`
			Mode *uint32 `json:"mode"`
		}
		body := io.TeeReader(&countReader{f, &t.done, mine}, h)
		if _, err := t.c.raw("PUT", "/api/fs/file?"+q.Encode(), body, 0, &res); err != nil {
			return err
		}
		if res.Sha != h.sum() {
			return fmt.Errorf(i18n.S("到那边的内容不对：发了 %d 字节（sha256 %s），那边是 %s", "arrived different: sent %d bytes (sha256 %s), got %s"), h.n.Load(), h.sum(), res.Sha)
		}
		if res.Mode == nil && j.mode&0o111 != 0 {
			t.modeLost.Store(true)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(j.local), 0o755); err != nil {
		return fmt.Errorf("%w: %w", errNoRetry, err)
	}
	q := url.Values{"host": {j.host}, "path": {j.remote}}
	resp, err := t.c.stream("GET", "/api/fs/raw?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	tmp := j.local + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("%w: %w", errNoRetry, err)
	}
	h := newHashCounter()
	_, err = io.Copy(io.MultiWriter(f, h), &countReader{resp.Body, &t.done, mine})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && j.sha != "" && h.sum() != j.sha {
		err = fmt.Errorf(i18n.S("收到的内容不对：%d 字节，sha256 %s，那边是 %s", "arrived different: %d bytes, sha256 %s, the source is %s"), h.n.Load(), h.sum(), j.sha)
	}
	if err == nil && j.mode != 0 {
		err = os.Chmod(tmp, os.FileMode(j.mode).Perm())
	}
	if err == nil {
		err = os.Rename(tmp, j.local)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// progress: a live line in a terminal; elsewhere (an agent's command output) a line every 10s, and only for a copy
// that takes that long.
func (t *cpRun) progress(n int) (stop func()) {
	st, _ := os.Stderr.Stat()
	tty := st != nil && st.Mode()&os.ModeCharDevice != 0
	every := 10 * time.Second
	if tty {
		every = 250 * time.Millisecond
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	start := time.Now()
	line := func() string {
		d, secs := t.done.Load(), time.Since(start).Seconds()
		return fmt.Sprintf("gerdr host cp: %s / %s, %d / %d files, %s/s", mb(d), mb(t.total.Load()), t.files.Load(), n, mb(int64(float64(d)/max(secs, 0.001))))
	}
	go func() {
		defer wg.Done()
		tk := time.NewTicker(every)
		defer tk.Stop()
		for {
			select {
			case <-done:
				if tty {
					fmt.Fprint(os.Stderr, "\r\033[K")
				}
				return
			case <-tk.C:
				if tty {
					fmt.Fprint(os.Stderr, "\r\033[K"+line())
				} else {
					fmt.Fprintln(os.Stderr, line())
				}
			}
		}
	}()
	return func() { close(done); wg.Wait() }
}

func mb(n int64) string {
	if n < 1<<20 {
		return fmt.Sprintf("%.0f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

type countReader struct {
	r    io.Reader
	n    *atomic.Int64
	mine *int64 // read by the same goroutine once the request is over
}

func (c *countReader) Read(p []byte) (int, error) {
	k, err := c.r.Read(p)
	c.n.Add(int64(k))
	*c.mine += int64(k)
	return k, err
}

// hashCounter: sha256 and length of what passes through it (as an io.Writer).
type hashCounter struct {
	h hash.Hash
	n atomic.Int64
}

func newHashCounter() *hashCounter { return &hashCounter{h: sha256.New()} }

func (c *hashCounter) Write(p []byte) (int, error) {
	c.n.Add(int64(len(p)))
	return c.h.Write(p)
}

func (c *hashCounter) sum() string { return hex.EncodeToString(c.h.Sum(nil)) }

// stream: a request with a raw body (nil = none) and no overall timeout (a big file over the relay takes what it
// takes); the answer, when it is 2xx, for the caller to read and close.
func (c *apiClient) stream(method, p string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, c.base+p, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Gerdr", "1")
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		return resp, respError(resp)
	}
	return resp, nil
}

// raw: stream, with a JSON answer decoded into out. timeout 0 = none. Returns the status code too (404 = not there).
func (c *apiClient) raw(method, p string, body io.Reader, timeout time.Duration, out any) (int, error) {
	req, err := http.NewRequest(method, c.base+p, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Gerdr", "1")
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, respError(resp)
	}
	if out == nil {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
}

func respError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error != "" {
		return errors.New(e.Error)
	}
	return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
}
