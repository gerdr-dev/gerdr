package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gerdr-dev/gerdr/internal/folder"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// gerdr folder pull / push / status: artifact folders between this computer and one it drives
// (internal/folder, docs/dev/MULTIHOST.md "Artifact folders"). Run on the computer that drives (the console):
// the other one can't reach back.
func folderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "folder",
		Short: i18n.S("产物文件夹：这台的一个目录和另一台电脑上的一个目录配成一对，按需拉 / 推，两边都改过的文件两份都留", "Artifact folders: a folder here paired with one on another computer, pulled / pushed on demand; a file changed on both sides is kept twice"),
	}
	client := func() *apiClient {
		c, err := newAPIClient()
		if err != nil {
			fail(err)
		}
		return c
	}
	cmd.AddCommand(&cobra.Command{
		Use:   i18n.S("pull [<电脑>:<路径>] [目录]", "pull [<host>:<path>] [dir]"),
		Short: i18n.S("把那边的改动拿到这边（第一次给 <电脑>:<路径>，就配上对；之后在目录里直接 pull）", "Bring the changes over there here (the first time, <host>:<path> pairs them; after that just pull in the folder)"),
		Args:  cobra.MaximumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			c := client()
			f, err := c.openFolder(args, true)
			if err != nil {
				return err
			}
			return f.sync(c, true)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   i18n.S("push [目录] [<电脑>:<路径>]", "push [dir] [<host>:<path>]"),
		Short: i18n.S("把这边的改动送过去（没配过对时给 <电脑>:<路径>）", "Send the changes here over there (give <host>:<path> if it isn't paired yet)"),
		Args:  cobra.MaximumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			c := client()
			f, err := c.openFolder(args, false)
			if err != nil {
				return err
			}
			return f.sync(c, false)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   i18n.S("status [目录]", "status [dir]"),
		Short: i18n.S("两边各改了什么，pull / push 会做什么（不动文件）", "What changed on each side and what pull / push would do (touches nothing)"),
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c := client()
			f, err := c.openFolder(args, false)
			if err != nil {
				return err
			}
			if f.m == nil {
				return errors.New(i18n.S("这个目录还没配对：gerdr folder pull <电脑>:<路径> 或 push <目录> <电脑>:<路径>", "This folder isn't paired: gerdr folder pull <host>:<path> or push <dir> <host>:<path>"))
			}
			local, remote, err := f.trees(c)
			if err != nil {
				return err
			}
			printJSON(map[string]any{"peer": f.m.Peer, "pull": orEmpty(f.m.Quiet(folder.Plan(f.m.Files, remote, local))), "push": orEmpty(f.m.Quiet(folder.Plan(f.m.Files, local, remote)))})
			return nil
		},
	})
	return cmd
}

func orEmpty(c []folder.Change) []folder.Change {
	if c == nil {
		return []folder.Change{}
	}
	return c
}

type syncFolder struct {
	dir string           // here, absolute
	m   *folder.Manifest // nil: not paired yet
}

// openFolder reads the arguments of pull (spec first) or push / status (dir first), in any order really: the one
// with a colon is the other side.
func (c *apiClient) openFolder(args []string, pull bool) (*syncFolder, error) {
	var spec, dir string
	for _, a := range args {
		if h, _, ok := strings.Cut(a, ":"); ok && h != "" && !strings.Contains(h, "/") {
			spec = a
		} else {
			dir = a
		}
	}
	var peer *folder.Peer
	if spec != "" {
		host, p, _ := strings.Cut(spec, ":")
		abs, err := c.hostCwd(host, p)
		if err != nil {
			return nil, err
		}
		peer = &folder.Peer{Host: host, Path: strings.TrimRight(abs, "/")}
		if dir == "" && pull {
			dir = path.Base(peer.Path)
		}
	}
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	f := &syncFolder{dir: abs}
	m, err := folder.LoadManifest(abs)
	switch {
	case err == nil:
		f.m = m
		if peer != nil && *peer != m.Peer {
			return nil, fmt.Errorf(i18n.S("%s 已经和 %s:%s 配成对了", "%s is already paired with %s:%s"), dir, m.Peer.Host, m.Peer.Path)
		}
	case !os.IsNotExist(err):
		return nil, err
	case peer != nil:
		f.m = &folder.Manifest{Peer: *peer, Files: map[string]string{}, Copies: map[string]bool{}}
	}
	if f.m == nil && pull {
		return nil, errors.New(i18n.S("要给 <电脑>:<路径>（gerdr host list 里的 id）", "Give <host>:<path> (an id from gerdr host list)"))
	}
	return f, nil
}

func (f *syncFolder) trees(c *apiClient) (local, remote []folder.Entry, err error) {
	if _, err := os.Stat(f.dir); err == nil {
		if local, err = folder.Walk(f.dir); err != nil {
			return nil, nil, err
		}
	}
	var out struct {
		Files []folder.Entry `json:"files"`
	}
	q := url.Values{"host": {f.m.Peer.Host}, "path": {f.m.Peer.Path}}
	if err := c.do("GET", "/api/fs/tree?"+q.Encode(), nil, &out); err != nil {
		// Pushing into a folder that isn't there yet makes it
		if f.m.Peer.Host != "" && strings.Contains(err.Error(), "no such file") {
			return local, nil, nil
		}
		return nil, nil, err
	}
	return local, out.Files, nil
}

// sync does a pull (there → here) or a push (here → there) and records the new base.
func (f *syncFolder) sync(c *apiClient, pull bool) error {
	if f.m == nil {
		return errors.New(i18n.S("这个目录还没配对：push <目录> <电脑>:<路径>", "This folder isn't paired: push <dir> <host>:<path>"))
	}
	local, remote, err := f.trees(c)
	if err != nil {
		return err
	}
	src, dst := remote, local
	who := f.m.Peer.Host // a conflict copy is named after the side it came from
	if !pull {
		src, dst = local, remote
		who, _ = os.Hostname()
		who = strings.TrimSuffix(who, ".local")
	}
	plan := folder.Plan(f.m.Files, src, dst)
	srcSha, dstSha := map[string]string{}, map[string]string{}
	for _, e := range src {
		srcSha[e.Path] = e.Sha
	}
	for _, e := range dst {
		dstSha[e.Path] = e.Sha
	}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		return err
	}
	done := map[string]bool{}
	written := map[string]bool{} // conflict copies made just now: in neither tree read before
	for i := range plan {
		ch := &plan[i]
		switch ch.Action {
		case "copy":
			if err := f.put(c, pull, ch.Path, ch.Path, ifMatch(dstSha, ch.Path)); err != nil {
				ch.Action, ch.Detail = "failed", err.Error()
				continue
			}
			done[ch.Path] = true
		case "conflict":
			ch.As = folder.ConflictName(ch.Path, who)
			if err := f.put(c, pull, ch.Path, ch.As, "*"); err != nil {
				ch.Action, ch.Detail = "failed", err.Error()
				continue
			}
			// The copy is the sending side's own version: known to the base, so the next sync the other way doesn't
			// bring it back as a new file there
			f.m.Files[ch.As] = srcSha[ch.Path]
			f.m.Copies[ch.As] = true
			written[ch.As] = true
		}
	}
	// The new base: what both sides now agree on. A conflict keeps its old base, so it keeps being reported until
	// someone settles it (deletes the copy, makes both the same).
	for p, sha := range srcSha {
		if done[p] || dstSha[p] == sha {
			f.m.Files[p] = sha
		}
	}
	for p := range f.m.Files {
		if written[p] {
			continue
		}
		if _, a := srcSha[p]; !a {
			if _, b := dstSha[p]; !b {
				delete(f.m.Files, p)
				delete(f.m.Copies, p)
			}
		}
	}
	if err := f.m.Save(f.dir); err != nil {
		return err
	}
	printJSON(map[string]any{"dir": f.dir, "peer": f.m.Peer, "changes": orEmpty(f.m.Quiet(plan))})
	return nil
}

func ifMatch(dst map[string]string, p string) string {
	if s, ok := dst[p]; ok {
		return s
	}
	return "none"
}

// put copies one file across: from there to here (pull) or here to there; to: its name on the receiving side.
func (f *syncFolder) put(c *apiClient, pull bool, from, to, match string) error {
	peer := f.m.Peer
	if pull {
		dest := filepath.Join(f.dir, filepath.FromSlash(to))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		_, err := c.fetch(peer.Host, peer.Path+"/"+from, "", dest)
		return err
	}
	src, err := os.Open(filepath.Join(f.dir, filepath.FromSlash(from)))
	if err != nil {
		return err
	}
	defer src.Close()
	q := url.Values{"host": {peer.Host}, "path": {peer.Path + "/" + to}, "ifMatch": {match}}
	req, err := http.NewRequest("PUT", c.base+"/api/fs/file?"+q.Encode(), src)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Gerdr", "1")
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusPreconditionFailed {
		return errors.New(i18n.S("那边刚刚改过：再 pull 一次看看", "It changed there just now: pull again"))
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}
