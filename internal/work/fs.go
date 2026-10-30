package work

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// ctx.fs: files in the project's Work directory (data a function keeps, exports, reports), the local-disk half of Annulo's
// ctx.fs. Paths are relative to the Work directory with / separators; no absolute paths, no .., and a symlink resolving
// outside doesn't count.
//
//	ctx.fs.root                          the Work directory's absolute path
//	ctx.fs.kind                          'disk'
//	ctx.fs.canOpen                       true: ctx.fs.open works here
//	ctx.fs.list(dir?)                    → [{ path, dir, size, mtime, etag }], all levels down; skips dot entries and
//	                                       node_modules, 5000 at most; a missing dir is []
//	ctx.fs.stat(path)                    → { path, dir, size, mtime, etag } or null
//	ctx.fs.read(path)                    → text (UTF-8, 10MB at most)
//	ctx.fs.write(path, text, { etag }?)  creates missing dirs; with etag (from when it was read) a file changed since isn't
//	                                       overwritten, it throws; returns { etag }
//	ctx.fs.mkdir(path)
//	ctx.fs.move(from, to)                throws if to exists
//	ctx.fs.copy(from, to)                throws if to exists
//	ctx.fs.trash(path)                   into the system Trash on macOS, else .trash/ in the Work directory
//	ctx.fs.open(path, { reveal }?)       open with the default app, or show it in Finder
//
// mtime is in milliseconds.

const (
	fsMaxRead = 10 << 20
	fsMaxList = 5000
)

var fsWrite sync.Mutex // a conditional write's compare and replace, not interleaved with another (in this process)

func etagOf(st fs.FileInfo) string { return fmt.Sprintf("%x-%x", st.ModTime().UnixNano(), st.Size()) }

func entryOf(rel string, st fs.FileInfo) map[string]any {
	m := map[string]any{"path": rel, "dir": st.IsDir(), "size": int64(0), "mtime": st.ModTime().UnixMilli()}
	if !st.IsDir() {
		m["size"], m["etag"] = st.Size(), etagOf(st)
	}
	return m
}

// cleanRel checks a path relative to the Work directory and returns it in / form ("" = the directory itself).
func cleanRel(p string) (string, error) {
	p = strings.ReplaceAll(p, "\\", "/")
	if strings.HasPrefix(p, "/") || filepath.IsAbs(p) {
		return "", fmt.Errorf("%s: paths are relative to the Work directory", p)
	}
	c := path.Clean("/" + p)[1:]
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", fmt.Errorf("%s: .. isn't allowed", p)
		}
	}
	return c, nil
}

// abs turns a relative path into an absolute one inside the Work directory (symlinks resolved). With exist the path
// must exist; otherwise the nearest existing parent is checked.
func (r *runner) abs(p string, exist bool) (string, string, error) {
	rel, err := cleanRel(p)
	if err != nil {
		return "", "", err
	}
	full := filepath.Join(r.dir, filepath.FromSlash(rel))
	realRoot, err := filepath.EvalSymlinks(r.dir)
	if err != nil {
		return "", "", err
	}
	for check := full; ; {
		real, err := filepath.EvalSymlinks(check)
		if err == nil {
			if real != realRoot && !strings.HasPrefix(real, realRoot+string(filepath.Separator)) {
				return "", "", fmt.Errorf("%s is outside the Work directory", p)
			}
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", "", err
		}
		if exist {
			return "", "", fmt.Errorf("no such file: %s", p)
		}
		parent := filepath.Dir(check)
		if parent == check {
			break
		}
		check = parent
	}
	return full, rel, nil
}

func (r *runner) fsObject() map[string]any {
	must := func(full, rel string, err error) (string, string) {
		if err != nil {
			r.throw(err)
		}
		return full, rel
	}
	return map[string]any{
		"root":    r.dir,
		"kind":    "disk",
		"canOpen": runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows",
		"list": func(dir goja.Value) []map[string]any {
			rel := ""
			if dir != nil && !goja.IsUndefined(dir) && !goja.IsNull(dir) {
				rel = dir.String()
			}
			full, base, err := r.abs(rel, true)
			out := []map[string]any{}
			if err != nil {
				if strings.HasPrefix(err.Error(), "no such file") {
					return out
				}
				r.throw(err)
			}
			_ = filepath.WalkDir(full, func(p string, de fs.DirEntry, err error) error {
				if err != nil || p == full {
					return nil
				}
				if strings.HasPrefix(de.Name(), ".") || (de.IsDir() && de.Name() == "node_modules") {
					if de.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if len(out) >= fsMaxList {
					return filepath.SkipAll
				}
				st, err := de.Info()
				if err != nil {
					return nil
				}
				sub, _ := filepath.Rel(full, p)
				out = append(out, entryOf(path.Join(base, filepath.ToSlash(sub)), st))
				return nil
			})
			return out
		},
		"stat": func(p string) any {
			full, rel, err := r.abs(p, true)
			if err != nil {
				if strings.HasPrefix(err.Error(), "no such file") {
					return nil
				}
				r.throw(err)
			}
			st, err := os.Stat(full)
			if err != nil {
				return nil
			}
			return entryOf(rel, st)
		},
		"read": func(p string) string {
			full, _ := must(r.abs(p, true))
			f, err := os.Open(full)
			if err != nil {
				r.throw(err)
			}
			defer f.Close()
			if st, _ := f.Stat(); st != nil && st.IsDir() {
				r.throw(fmt.Errorf("%s is a directory", p))
			}
			b, err := io.ReadAll(io.LimitReader(f, fsMaxRead+1))
			if err != nil {
				r.throw(err)
			}
			if len(b) > fsMaxRead {
				r.throw(fmt.Errorf("%s is larger than %d MB", p, fsMaxRead>>20))
			}
			return string(b)
		},
		"write": func(p, text string, opts map[string]any) map[string]any {
			full, rel := must(r.abs(p, false))
			if rel == "" {
				r.throw(errors.New("a file path is required"))
			}
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				r.throw(err)
			}
			// Write a temp file next to it and swap it in: a failed write leaves no half file
			tmp, err := os.CreateTemp(filepath.Dir(full), ".gerdr-write-*")
			if err != nil {
				r.throw(err)
			}
			defer os.Remove(tmp.Name())
			if _, err := tmp.WriteString(text); err != nil {
				tmp.Close()
				r.throw(err)
			}
			if err := tmp.Close(); err != nil {
				r.throw(err)
			}
			_ = os.Chmod(tmp.Name(), 0o644)
			fsWrite.Lock()
			defer fsWrite.Unlock()
			if want, _ := opts["etag"].(string); want != "" {
				if st, err := os.Stat(full); err != nil || etagOf(st) != want {
					r.throw(fmt.Errorf("%s was changed since it was read (not the version you have): read it again, then change it", p))
				}
			}
			if err := os.Rename(tmp.Name(), full); err != nil {
				r.throw(err)
			}
			st, err := os.Stat(full)
			if err != nil {
				r.throw(err)
			}
			return map[string]any{"etag": etagOf(st)}
		},
		"mkdir": func(p string) {
			full, _ := must(r.abs(p, false))
			if err := os.MkdirAll(full, 0o755); err != nil {
				r.throw(err)
			}
		},
		"move": func(from, to string) {
			src, _ := must(r.abs(from, true))
			dst, _ := must(r.abs(to, false))
			if _, err := os.Lstat(dst); err == nil {
				r.throw(fmt.Errorf("%s already exists", to))
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				r.throw(err)
			}
			if err := os.Rename(src, dst); err != nil {
				r.throw(err)
			}
		},
		"copy": func(from, to string) {
			src, _ := must(r.abs(from, true))
			dst, _ := must(r.abs(to, false))
			if _, err := os.Lstat(dst); err == nil {
				r.throw(fmt.Errorf("%s already exists", to))
			}
			if err := copyTree(src, dst); err != nil {
				r.throw(err)
			}
		},
		"trash": func(p string) {
			full, rel := must(r.abs(p, true))
			if rel == "" {
				r.throw(errors.New("can't trash the whole Work directory"))
			}
			if err := trash(r.dir, full); err != nil {
				r.throw(err)
			}
		},
		"open": func(p string, opts map[string]any) {
			full, _ := must(r.abs(p, true))
			reveal, _ := opts["reveal"].(bool)
			if err := openFile(full, reveal); err != nil {
				r.throw(err)
			}
		},
	}
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if de.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

// trash moves into the macOS Trash (Finder can put it back), else into .trash/ in the Work directory.
func trash(root, full string) error {
	if runtime.GOOS == "darwin" {
		if home, err := os.UserHomeDir(); err == nil {
			t := filepath.Join(home, ".Trash")
			if st, err := os.Stat(t); err == nil && st.IsDir() {
				dst := filepath.Join(t, filepath.Base(full))
				if _, err := os.Lstat(dst); err == nil {
					dst = filepath.Join(t, fmt.Sprintf("%s %s", filepath.Base(full), time.Now().Format("15.04.05")))
				}
				if os.Rename(full, dst) == nil {
					return nil
				}
			}
		}
	}
	t := filepath.Join(root, ".trash")
	if err := os.MkdirAll(t, 0o755); err != nil {
		return err
	}
	return os.Rename(full, filepath.Join(t, fmt.Sprintf("%d-%s", time.Now().UnixNano(), filepath.Base(full))))
}

func openFile(full string, reveal bool) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		if reveal {
			cmd = exec.Command("open", "-R", full)
		} else {
			cmd = exec.Command("open", full)
		}
	case "windows":
		if reveal {
			cmd = exec.Command("explorer", "/select,", full)
		} else {
			cmd = exec.Command("cmd", "/c", "start", "", full)
		}
	default:
		if reveal {
			full = filepath.Dir(full)
		}
		cmd = exec.Command("xdg-open", full)
	}
	return cmd.Start()
}
