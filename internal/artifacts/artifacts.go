// Package artifacts keeps the folders a piece of work leaves behind: files that shouldn't be committed but someone
// needs to see (generated images, reports, drafts, copy to publish), and records of what lives elsewhere (a PR, a
// folder created outside the repository, a short text). Design and reasons: docs/dev/ARTIFACTS.md.
//
// Layout under the root (~/Gerdr by default, visible in Finder on purpose):
//
//	.gerdr/index.json             project id → folder name; folder id → path; owner → folder id
//	<project>/<repo>/             a repository's folder (every worktree and session of it shares one)
//	  .gerdr/folder.json          {id, owner, createdAt}: the identity, so a folder renamed or moved in Finder is found again
//	  .gerdr/items.jsonl          records, append-only
//	  .gerdrignore                what `gerdr folder` doesn't sync (scratch, caches)
//
// Folders are created lazily: a session gets a path, and nothing exists until something is put there.
package artifacts

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultRoot is ~/Gerdr.
func DefaultRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Gerdr")
}

// Meta is the hidden directory inside the root and inside every folder.
const Meta = ".gerdr"

// DefaultIgnore is written into a new folder's .gerdrignore: scratch stays on the computer that made it.
const DefaultIgnore = "# Not synced to other computers (gerdr folder pull / push), written like .gitignore.\n" +
	"# Scratch work goes in 草稿/ or drafts/; finished things stay outside it.\n草稿/\ndrafts/\ncache/\n*.tmp\n"

// Owner is what a folder belongs to. One of Repo or Panel+Task.
type Owner struct {
	// Repo: a repository's git common dir (the same for all its worktrees), or the directory itself outside git.
	Repo  string `json:"repo,omitempty"`
	Panel string `json:"panel,omitempty"`
	Task  string `json:"task,omitempty"`
}

func (o Owner) key() string {
	if o.Repo != "" {
		return "repo:" + o.Repo
	}
	if o.Panel != "" {
		return "task:" + o.Panel + "/" + o.Task
	}
	return ""
}

// Folder is a folder's identity (.gerdr/folder.json) plus where it is now.
type Folder struct {
	ID        string `json:"id"`
	Owner     Owner  `json:"owner"`
	CreatedAt string `json:"createdAt"`
	Path      string `json:"-"`
}

type index struct {
	Projects map[string]string `json:"projects"` // project id → folder name under the root
	Folders  map[string]string `json:"folders"`  // folder id → path relative to the root (or absolute outside it)
	Owners   map[string]string `json:"owners"`   // Owner.key() → folder id
}

// Store finds and creates folders under a root. Safe for concurrent use.
type Store struct {
	root func() string
	mu   sync.Mutex
	repo sync.Map // dir → [2]string{key, name}
}

// New: root is read on every call (a setting the person may change).
func New(root func() string) *Store {
	if root == nil {
		root = DefaultRoot
	}
	return &Store{root: root}
}

// Root is the root directory now.
func (s *Store) Root() string {
	r := s.root()
	if r == "" {
		r = DefaultRoot()
	}
	return r
}

func (s *Store) loadIndex() index {
	var ix index
	if b, err := os.ReadFile(filepath.Join(s.Root(), Meta, "index.json")); err == nil {
		_ = json.Unmarshal(b, &ix)
	}
	if ix.Projects == nil {
		ix.Projects = map[string]string{}
	}
	if ix.Folders == nil {
		ix.Folders = map[string]string{}
	}
	if ix.Owners == nil {
		ix.Owners = map[string]string{}
	}
	return ix
}

func (s *Store) saveIndex(ix index) error {
	dir := filepath.Join(s.Root(), Meta)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(ix, "", "  ")
	return writeAtomic(filepath.Join(dir, "index.json"), b)
}

func (s *Store) abs(rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(s.Root(), rel)
}

func (s *Store) rel(p string) string {
	if r, err := filepath.Rel(s.Root(), p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}

// RepoOf is the repository a directory is in: its git common dir (shared by every worktree) and a name for its
// folder (the main checkout's directory name). Outside git: the directory itself and its name.
func (s *Store) RepoOf(dir string) (key, name string) {
	if v, ok := s.repo.Load(dir); ok {
		r := v.([2]string)
		return r[0], r[1]
	}
	key, name = dir, filepath.Base(dir)
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if c := strings.TrimSpace(string(out)); err == nil && c != "" {
		key = filepath.Clean(c)
		if filepath.Base(key) == ".git" {
			name = filepath.Base(filepath.Dir(key))
		} else { // a bare repository: weave.git
			name = strings.TrimSuffix(filepath.Base(key), ".git")
		}
	}
	s.repo.Store(dir, [2]string{key, name})
	return key, name
}

// ForRepo is the folder of the repository cwd is in, for a session of the given project (its id and the name it
// shows). It may not exist yet. A folder the person moved or renamed is found again by its id.
func (s *Store) ForRepo(projectID, projectName, cwd string) (string, Owner) {
	key, name := s.RepoOf(cwd)
	o := Owner{Repo: key}
	s.mu.Lock()
	defer s.mu.Unlock()
	ix := s.loadIndex()
	if p, ok := s.known(&ix, o); ok {
		return p, o
	}
	base := filepath.Join(s.projectDir(ix, projectID, projectName), safeName(name))
	return s.free(base, o), o
}

// ForOwner is the folder of something a panel owns (a task: Owner{Panel, Task}), named name under the project's
// directory's under/ ("任务/<title>"). It may not exist yet; once it does, it is found by id, wherever it went.
func (s *Store) ForOwner(projectID, projectName string, o Owner, under, name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ix := s.loadIndex()
	if p, ok := s.known(&ix, o); ok {
		return p
	}
	base := s.projectDir(ix, projectID, projectName)
	for _, seg := range strings.Split(filepath.ToSlash(under), "/") {
		if seg = strings.TrimSpace(seg); seg != "" && seg != "." && seg != ".." {
			base = filepath.Join(base, safeName(seg))
		}
	}
	return s.free(filepath.Join(base, safeName(name)), o)
}

// Rename gives an owner's existing folder a new name in the same place (its title changed), keeping its id. Only
// when the new name is free; otherwise it stays as it is. Returns where it is now.
func (s *Store) Rename(o Owner, name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ix := s.loadIndex()
	p, ok := s.known(&ix, o)
	if !ok {
		return "", nil
	}
	if _, err := os.Stat(p); err != nil {
		return p, nil // not created yet: ForOwner names it when it is
	}
	to := filepath.Join(filepath.Dir(p), safeName(name))
	if to == p {
		return p, nil
	}
	if _, err := os.Lstat(to); err == nil {
		return p, nil
	}
	if err := os.Rename(p, to); err != nil {
		return p, err
	}
	if f, err := Read(to); err == nil {
		ix.Folders[f.ID] = s.rel(to)
		_ = s.saveIndex(ix)
	}
	return to, nil
}

// known: where the owner's folder is, when it has one (repairing the index when it moved).
func (s *Store) known(ix *index, o Owner) (string, bool) {
	id, ok := ix.Owners[o.key()]
	if !ok {
		return "", false
	}
	if rel, ok := ix.Folders[id]; ok {
		p := s.abs(rel)
		if f, err := Read(p); err == nil && f.ID == id {
			return p, true
		}
		if _, err := os.Stat(p); os.IsNotExist(err) {
			if found := s.scan(id); found != "" {
				ix.Folders[id] = s.rel(found)
				_ = s.saveIndex(*ix)
				return found, true
			}
		}
		return p, true // gone, or something else took its place: it comes back where it was
	}
	return "", false
}

// free: base, or base 2, base 3… when base already is another owner's folder.
func (s *Store) free(base string, o Owner) string {
	for i := 1; ; i++ {
		p := base
		if i > 1 {
			p = fmt.Sprintf("%s_%d", base, i)
		}
		f, err := Read(p)
		if err != nil || f.Owner.key() == o.key() || f.Owner.key() == "" { // no owner yet: it is claimed (Ensure)
			return p
		}
	}
}

// projectDir: the project's folder under the root, named after the project's name the first time and kept after.
func (s *Store) projectDir(ix index, id, name string) string {
	if d, ok := ix.Projects[id]; ok {
		return s.abs(d)
	}
	if name == "" {
		name = id
	}
	base := safeName(name)
	taken := map[string]bool{}
	for _, d := range ix.Projects {
		taken[d] = true
	}
	d := base
	for i := 2; taken[d]; i++ {
		d = fmt.Sprintf("%s_%d", base, i)
	}
	return filepath.Join(s.Root(), d)
}

// scan looks for a folder with this id under the root (moved or renamed in Finder), a few levels deep.
func (s *Store) scan(id string) string {
	root := s.Root()
	var found string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			return nil
		}
		if r, _ := filepath.Rel(root, p); strings.Count(r, string(filepath.Separator)) >= 4 {
			return filepath.SkipDir
		}
		if d.Name() == Meta || d.Name() == ".git" || d.Name() == "node_modules" {
			return filepath.SkipDir
		}
		if f, err := Read(p); err == nil && f.ID == id {
			found = p
			return filepath.SkipDir
		}
		return nil
	})
	return found
}

// Read is the identity of the folder at p.
func Read(p string) (Folder, error) {
	var f Folder
	b, err := os.ReadFile(filepath.Join(p, Meta, "folder.json"))
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, err
	}
	if f.ID == "" {
		return f, errors.New("folder.json without an id")
	}
	f.Path = p
	return f, nil
}

// Ensure makes p a folder of owner: creates it, gives it an identity and a .gerdrignore when it has none, and
// records it in the index (projectID: whose project directory it is in, when it is one). A directory an agent
// created by itself is adopted the same way.
func (s *Store) Ensure(p string, o Owner, projectID string) (Folder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, err := Read(p); err == nil {
		if f.Owner.key() == "" && o.key() != "" { // made before its owner was known (a record came first): claimed now
			f.Owner = o
			b, _ := json.MarshalIndent(f, "", "  ")
			if err := writeAtomic(filepath.Join(p, Meta, "folder.json"), b); err != nil {
				return f, err
			}
		}
		return f, s.register(f, projectID)
	}
	if err := os.MkdirAll(filepath.Join(p, Meta), 0o755); err != nil {
		return Folder{}, err
	}
	f := Folder{ID: newID(), Owner: o, CreatedAt: time.Now().UTC().Format(time.RFC3339), Path: p}
	b, _ := json.MarshalIndent(f, "", "  ")
	if err := writeAtomic(filepath.Join(p, Meta, "folder.json"), b); err != nil {
		return Folder{}, err
	}
	ig := filepath.Join(p, ".gerdrignore")
	if _, err := os.Stat(ig); os.IsNotExist(err) {
		_ = os.WriteFile(ig, []byte(DefaultIgnore), 0o644)
	}
	return f, s.register(f, projectID)
}

func (s *Store) register(f Folder, projectID string) error {
	ix := s.loadIndex()
	changed := false
	if rel := s.rel(f.Path); ix.Folders[f.ID] != rel {
		ix.Folders[f.ID] = rel
		changed = true
	}
	if k := f.Owner.key(); k != "" && ix.Owners[k] != f.ID {
		ix.Owners[k] = f.ID
		changed = true
	}
	if projectID != "" {
		if _, ok := ix.Projects[projectID]; !ok {
			if r := s.rel(f.Path); !filepath.IsAbs(r) && strings.Contains(r, string(filepath.Separator)) {
				ix.Projects[projectID] = strings.SplitN(r, string(filepath.Separator), 2)[0]
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	return s.saveIndex(ix)
}

// ---- records ----

// Kinds of record.
const (
	KindLink   = "link"   // a URL: a PR / MR, a pipeline, a published post
	KindPath   = "path"   // something outside the folder, on this computer
	KindText   = "text"   // a short text worth keeping
	KindFile   = "file"   // a file in the folder, pinned (Path relative to the folder)
	KindRetire = "retire" // only marks Supersedes as outdated
)

// Item is one record. Records are never deleted: an outdated one is superseded by a later one.
type Item struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Label      string `json:"label,omitempty"`
	URL        string `json:"url,omitempty"`
	Path       string `json:"path,omitempty"`
	Text       string `json:"text,omitempty"`
	At         int64  `json:"at"`
	Session    string `json:"session,omitempty"`
	Supersedes string `json:"supersedes,omitempty"`
	// Superseded is worked out when reading: a later record replaced or retired this one.
	Superseded bool `json:"superseded,omitempty"`
}

// TextMax is the longest text record; longer belongs in a .md file in the folder.
const TextMax = 2000

func itemsPath(dir string) string { return filepath.Join(dir, Meta, "items.jsonl") }

// Validate checks a record before it is written.
func (it *Item) Validate() error {
	switch it.Kind {
	case KindLink:
		if !regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`).MatchString(it.URL) {
			return fmt.Errorf("a link needs a URL: %q", it.URL)
		}
	case KindPath:
		if !filepath.IsAbs(it.Path) {
			return fmt.Errorf("a path record needs an absolute path: %q", it.Path)
		}
	case KindText:
		if strings.TrimSpace(it.Text) == "" {
			return errors.New("empty text")
		}
		if len(it.Text) > TextMax {
			return fmt.Errorf("text over %d bytes: write it to a .md file in the folder instead (and pin it)", TextMax)
		}
	case KindFile:
		if it.Path == "" || filepath.IsAbs(it.Path) || strings.HasPrefix(filepath.Clean(it.Path), "..") {
			return fmt.Errorf("a pinned file is a path inside the folder: %q", it.Path)
		}
	case KindRetire:
		if it.Supersedes == "" {
			return errors.New("retire needs the id it retires")
		}
	default:
		return fmt.Errorf("unknown kind %q (link, path, text, file, retire)", it.Kind)
	}
	return nil
}

// Record appends a record to the folder at dir (which must exist), filling in its id and time.
func Record(dir string, it Item) (Item, error) {
	if err := it.Validate(); err != nil {
		return it, err
	}
	it.ID, it.At, it.Superseded = newID(), time.Now().UnixMilli(), false
	if err := os.MkdirAll(filepath.Join(dir, Meta), 0o755); err != nil {
		return it, err
	}
	b, _ := json.Marshal(it)
	f, err := os.OpenFile(itemsPath(dir), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return it, err
	}
	_, err = f.Write(append(b, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return it, err
}

// Items are the folder's records, oldest first, with Superseded worked out; retire lines aren't returned.
func Items(dir string) ([]Item, error) {
	b, err := os.ReadFile(itemsPath(dir))
	if os.IsNotExist(err) {
		return []Item{}, nil
	}
	if err != nil {
		return nil, err
	}
	var all []Item
	for _, line := range strings.Split(string(b), "\n") {
		var it Item
		if strings.TrimSpace(line) == "" || json.Unmarshal([]byte(line), &it) != nil {
			continue
		}
		all = append(all, it)
	}
	gone := map[string]bool{}
	for _, it := range all {
		if it.Supersedes != "" {
			gone[it.Supersedes] = true
		}
	}
	out := []Item{}
	for _, it := range all {
		if it.Kind == KindRetire {
			continue
		}
		it.Superseded = gone[it.ID]
		out = append(out, it)
	}
	return out, nil
}

// ---- files ----

// Entry is one thing in a directory listing.
type Entry struct {
	Name  string `json:"name"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"`
}

// Hidden: what a listing leaves out (Gerdr's own files, the Mac's).
func Hidden(name string) bool {
	return name == Meta || name == ".DS_Store" || name == ".gerdr-folder.json" || strings.HasPrefix(name, ".gerdr-tmp-")
}

// List is one level of dir: folders first, then by name.
func List(dir string) ([]Entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, d := range des {
		if Hidden(d.Name()) {
			continue
		}
		info, err := d.Info()
		if err != nil {
			continue
		}
		isDir := d.IsDir()
		if d.Type()&fs.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(dir, d.Name())); err == nil {
				isDir, info = st.IsDir(), st
			}
		}
		e := Entry{Name: d.Name(), Dir: isDir, Mtime: info.ModTime().UnixMilli()}
		if !isDir {
			e.Size = info.Size()
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// Trash moves p into the Mac's Trash (Finder can put it back); elsewhere into <root>/.gerdr/trash.
func (s *Store) Trash(p string) error {
	if runtime.GOOS == "darwin" {
		if home, err := os.UserHomeDir(); err == nil {
			t := filepath.Join(home, ".Trash")
			if st, err := os.Stat(t); err == nil && st.IsDir() {
				dst := filepath.Join(t, filepath.Base(p))
				if _, err := os.Lstat(dst); err == nil {
					dst = filepath.Join(t, fmt.Sprintf("%s %s", filepath.Base(p), time.Now().Format("15.04.05")))
				}
				if os.Rename(p, dst) == nil {
					return nil
				}
			}
		}
	}
	t := filepath.Join(s.Root(), Meta, "trash")
	if err := os.MkdirAll(t, 0o755); err != nil {
		return err
	}
	return os.Rename(p, filepath.Join(t, fmt.Sprintf("%d-%s", time.Now().UnixNano(), filepath.Base(p))))
}

// ---- helpers ----

// Whitespace goes too: a path with spaces has to be quoted in every shell command an agent or a person types, and
// unquoted it silently becomes several arguments.
var unsafeChars = regexp.MustCompile(`[/\\:*?"<>|\x00-\x1f\s]+`)

// safeName makes a name usable as one path segment on every system, without spaces: runs of unsafe characters and
// whitespace become one "_".
func safeName(s string) string {
	s = strings.Trim(unsafeChars.ReplaceAllString(s, "_"), "._")
	if r := []rune(s); len(r) > 80 {
		s = strings.TrimRight(string(r[:80]), "._")
	}
	if s == "" {
		s = "untitled"
	}
	return s
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeAtomic(p string, b []byte) error {
	tmp := p + ".tmp-" + newID()
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
