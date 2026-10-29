package server

import (
	"encoding/json"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// 对话里点一条路径 → 原地看那个文件（图片 / 视频 / 文本……），手机上尤其要：那边没有 Finder 可以去翻。
//
//	GET /api/fs/stat?path=&cwd=   这个路径是什么（解析过的绝对路径、大小、是不是目录、类型）
//	GET /api/fs/raw?path=&cwd=    文件本身。走 http.ServeContent，带 Range —— 视频要能拖进度条
//
// 权限上和 /api/fs/dirs 是一档：拿到令牌的人本来就能让 agent 跑任意命令，读文件不是新口子。
// 新口子是**同源吐内容**：一个 .html / .svg 在这个源下被当页面打开，里面的脚本就带着我们的 cookie 跑了
// （agent 写出来的文件、它从网上抓下来的文件都算不可信）。所以 raw 的响应一律 CSP sandbox
// （没有源，脚本跑不起来），html / xml 这类直接按纯文本吐。

// resolvePath：`~` 展开成家目录，相对路径按 cwd 解（agent 写的 `docs/a.md` 是相对它的工作目录的）。
func resolvePath(p, cwd string) string {
	home, _ := os.UserHomeDir()
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	if !filepath.IsAbs(p) {
		if strings.HasPrefix(cwd, "~") {
			cwd = filepath.Join(home, strings.TrimPrefix(cwd, "~"))
		}
		if cwd == "" {
			cwd = home
		}
		p = filepath.Join(cwd, p)
	}
	return filepath.Clean(p)
}

// suffixMatches：相对路径（不带 `..`）在 cwd 下找「以它结尾」的文件，按路径长短排，最多 20 个，返回绝对路径。
// cwd 为空（没有会话目录）不找：那会去走整个家目录。
func suffixMatches(p, cwd string) []string {
	return suffixIn(p, cwd, nil)
}

// suffixIn is suffixMatches over an already listed file set (nil = list it now), so a batch of names
// costs one `git ls-files` instead of one each.
func suffixIn(p, cwd string, files []string) []string {
	p = filepath.ToSlash(strings.TrimPrefix(p, "./"))
	if cwd == "" || p == "" || filepath.IsAbs(p) || strings.HasPrefix(p, "~") || slices.Contains(strings.Split(p, "/"), "..") {
		return nil
	}
	dir := resolvePath(".", cwd)
	if files == nil {
		files = listFiles(dir)
	}
	var out []string
	for _, f := range files {
		if f == p || strings.HasSuffix(f, "/"+p) {
			out = append(out, filepath.Join(dir, filepath.FromSlash(f)))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) < len(out[j]) })
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

// fileMime：按扩展名猜类型。认不出来的给空串，前端按「试着当文本读」处理。
func fileMime(p string) string {
	ext := strings.ToLower(filepath.Ext(p))
	switch ext {
	// 系统的 mime 表里不一定有，或者给的是 x- 开头的怪名字
	case ".md", ".markdown":
		return "text/markdown"
	case ".ts", ".tsx", ".go", ".rs", ".py", ".sh", ".yaml", ".yml", ".toml", ".jsx", ".vue", ".swift", ".kt", ".java", ".c", ".h", ".cpp", ".rb", ".lua", ".sql", ".log", ".env", ".conf", ".ini":
		return "text/plain"
	case ".mov":
		return "video/quicktime"
	case ".mkv":
		return "video/x-matroska"
	}
	t := mime.TypeByExtension(ext)
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = t[:i]
	}
	return t
}

// unsafeInline：同源下当页面打开会跑脚本的那几类。raw 一律按纯文本吐。
func unsafeInline(t string) bool {
	switch t {
	case "text/html", "application/xhtml+xml", "text/xml", "application/xml", "text/javascript", "application/javascript":
		return true
	}
	return strings.HasSuffix(t, "+xml") && t != "image/svg+xml"
}

func (s *Server) fileStat(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := resolvePath(q.Get("path"), q.Get("cwd"))
	st, err := os.Stat(p)
	if os.IsNotExist(err) {
		// agent 常把路径写短（`chat/Composer.tsx`，其实在 ui/src/components/ 下），按后缀在会话目录里找：
		// 只有一个就当是它，有好几个就把候选给前端让人挑。
		switch c := suffixMatches(q.Get("path"), q.Get("cwd")); len(c) {
		case 0:
		case 1:
			p = c[0]
			st, err = os.Stat(p)
		default:
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "有好几个文件都对得上：" + q.Get("path"), "candidates": c})
			return
		}
	}
	if err != nil {
		if os.IsNotExist(err) {
			httpError(w, 404, "没有这个文件："+p)
			return
		}
		httpError(w, 400, err.Error())
		return
	}
	m := ""
	if !st.IsDir() {
		m = fileMime(p)
	}
	writeJSON(w, map[string]any{"path": p, "name": filepath.Base(p), "dir": st.IsDir(), "size": st.Size(), "mtime": st.ModTime().UnixMilli(), "mime": m})
}

// fileExists answers, for a batch of names, which ones open in the session directory (as is, or by the
// same suffix search fileStat falls back to). The chat uses it for bare file names in inline code
// (`README.md`, `Composer.tsx:42`): without a slash they can't be told apart from `acpx.GUIHint` or
// `Node.js` by looking, so they only become links once the file is known to be there.
//
//	POST /api/fs/exists {cwd, paths}  →  {exists: {path: true}}
func (s *Server) fileExists(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Cwd   string   `json:"cwd"`
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if len(body.Paths) > 500 {
		body.Paths = body.Paths[:500]
	}
	out := map[string]bool{}
	var files []string
	for _, p := range body.Paths {
		if p == "" {
			continue
		}
		if _, err := os.Stat(resolvePath(p, body.Cwd)); err == nil {
			out[p] = true
			continue
		}
		if body.Cwd == "" {
			continue
		}
		if files == nil {
			files = listFiles(resolvePath(".", body.Cwd))
			if files == nil {
				files = []string{}
			}
		}
		if len(suffixIn(p, body.Cwd, files)) > 0 {
			out[p] = true
		}
	}
	writeJSON(w, map[string]any{"exists": out})
}

func (s *Server) fileRaw(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := resolvePath(q.Get("path"), q.Get("cwd"))
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			httpError(w, 404, "没有这个文件："+p)
			return
		}
		httpError(w, 400, err.Error())
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		httpError(w, 400, "这是个目录："+p)
		return
	}
	t := fileMime(p)
	switch {
	case t == "":
		t = "application/octet-stream"
	case unsafeInline(t):
		t = "text/plain; charset=utf-8"
	case strings.HasPrefix(t, "text/"):
		t += "; charset=utf-8"
	}
	h := w.Header()
	h.Set("Content-Type", t)
	h.Set("Cache-Control", "no-cache")
	// 盖掉 guard 那套页面用的头：PDF 要能放进查看器的 iframe（同源），其余一律 sandbox。
	// PDF 不加 sandbox：Chrome 的 PDF 查看器在 sandbox 下不渲染；它自己的 JS 是隔离跑的。
	h.Del("X-Frame-Options")
	if t == "application/pdf" {
		h.Set("Content-Security-Policy", "frame-ancestors 'self'")
	} else {
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'; frame-ancestors 'self'")
	}
	if q.Get("download") == "1" {
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(p)}))
	}
	http.ServeContent(w, r, filepath.Base(p), st.ModTime(), f)
}
