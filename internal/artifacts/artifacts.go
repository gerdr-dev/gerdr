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

// ForOwner is the folder of something a panel owns (a task: Owner{Panel, Task}), named name under the project's
// directory's under/ ("任务/<title>"). It may not exist yet; once it does, it is found by id, wherever it went.
// No name: the thing has none yet (a blank task, titled after its first message); the folder waits under a hidden
// ".<task>" until Rename names it, so the user never sees placeholder folders like 新会话_3 (2026-11-27, the user:
// "每个任务都是有自己的名字的").
//
// The path is reserved for the owner the moment it is handed out (an id in the index before the folder exists):
// a panel gives it to the task's conversation right away, and the folder appears only when the agent first writes
// there. Unreserved, two tasks with the same title (every blank task starts as "新会话") were both handed
// 新会话_3, and whichever wrote first had its files adopted by the other.
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
	seg := safeName(name)
	if strings.TrimSpace(name) == "" {
		seg = "." + safeName(o.Task+o.Repo)
	}
	p := s.free(ix, filepath.Join(base, seg), o)
	if f, err := Read(p); err == nil && f.Owner.key() == o.key() {
		_ = s.register(f, projectID) // its own folder, only missing from the index
		return p
	}
	id := newID()
	ix.Owners[o.key()] = id
	ix.Folders[id] = s.rel(p)
	s.claimProject(&ix, projectID, p)
	_ = s.saveIndex(ix)
	return p
}

// ForSession is the folder of a conversation a panel started for owner, given the path it was started with.
// Normally that path (or what a rename left a link at) is the owner's own. Conversations started before paths were
// reserved (2026-11-27) were often given one another task's folder, or one shared by every blank task (新会話_3): for
// those the owner's own folder is used, or a new one is reserved next to the given path, named title.
func (s *Store) ForSession(given string, o Owner, title string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ix := s.loadIndex()
	if p, ok := s.known(&ix, o); ok {
		return p
	}
	g := followRenames(given)
	if _, ro, ok := s.reservation(ix, g); ok && ro.key() == o.key() {
		return g
	}
	if f, err := Read(g); err == nil && f.Owner.key() == o.key() {
		return g
	}
	if _, err := os.Lstat(g); err != nil {
		if _, _, ok := s.reservation(ix, g); !ok {
			return given // nobody's yet: it becomes the owner's when created
		}
	}
	seg := safeName(title)
	if strings.TrimSpace(title) == "" {
		seg = "." + safeName(o.Task+o.Repo)
	}
	p := s.free(ix, filepath.Join(filepath.Dir(given), seg), o)
	id := newID()
	ix.Owners[o.key()] = id
	ix.Folders[id] = s.rel(p)
	_ = s.saveIndex(ix)
	return p
}

// Rename gives an owner's folder a new name in the same place (its title changed), keeping its id. Only when the new
// name is free; otherwise it stays as it is. Returns where it is now ("" when the owner has none).
//
// The old path stays usable as a symlink to the new one: a conversation was handed the old path when it started
// (it is in its prompt) and keeps writing there. Without the link those writes made a second, ownerless folder
// under the old name, which the next task of that name took. A folder not created yet is created to be renamed,
// for the same reason.
func (s *Store) Rename(o Owner, name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ix := s.loadIndex()
	p, ok := s.known(&ix, o)
	if !ok {
		return "", nil
	}
	to := filepath.Join(filepath.Dir(p), safeName(name))
	if to == p {
		return p, nil
	}
	if fi, err := os.Lstat(to); err == nil {
		// Taken, unless it is a link to this very folder left by an earlier rename (renamed back)
		if fi.Mode()&os.ModeSymlink == 0 {
			return p, nil
		}
		if t, err := Read(to); err != nil || t.Path == "" || ix.Owners[o.key()] != t.ID {
			return p, nil
		}
		if err := os.Remove(to); err != nil {
			return p, nil
		}
	}
	if _, err := os.Stat(p); err != nil {
		if _, err := s.ensure(p, o, ""); err != nil {
			return p, err
		}
		ix = s.loadIndex()
	}
	if err := os.Rename(p, to); err != nil {
		return p, err
	}
	_ = os.Symlink(filepath.Base(to), p) // relative: still right when the whole root moves
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

// free: base, or base_2, base_3… when base is someone else's: another owner's folder, a path reserved for another
// owner (handed out, not created yet), or anything already there that isn't this owner's (a directory an agent made
// without Gerdr knowing whose it is is not claimed by whoever comes next).
func (s *Store) free(ix index, base string, o Owner) string {
	reserved := map[string]string{} // path relative to the root → owner key
	for k, id := range ix.Owners {
		if rel, ok := ix.Folders[id]; ok {
			reserved[rel] = k
		}
	}
	for i := 1; ; i++ {
		p := base
		if i > 1 {
			p = fmt.Sprintf("%s_%d", base, i)
		}
		if k, ok := reserved[s.rel(p)]; ok && k != o.key() {
			continue
		}
		if _, err := os.Lstat(p); err != nil {
			return p
		}
		if f, err := Read(p); err == nil && f.Owner.key() == o.key() {
			return p
		}
	}
}

// reservation: the id and owner the index has for path p (reserved by ForOwner, or a known folder).
func (s *Store) reservation(ix index, p string) (id string, o Owner, ok bool) {
	rel := s.rel(p)
	for k, fid := range ix.Owners {
		if ix.Folders[fid] == rel {
			return fid, ownerOf(k), true
		}
	}
	return "", Owner{}, false
}

// followRenames: the folder an old name points to (Rename leaves a link there; a chain after several renames). Only
// the last segment is followed, so the path stays under the root as written (EvalSymlinks would also resolve, say,
// /var → /private/var and the folder would no longer be found under the root).
func followRenames(p string) string {
	for range 8 {
		fi, err := os.Lstat(p)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			return p
		}
		t, err := os.Readlink(p)
		if err != nil {
			return p
		}
		if !filepath.IsAbs(t) {
			t = filepath.Join(filepath.Dir(p), t)
		}
		p = t
	}
	return p
}

// ownerOf is the Owner an Owner.key() was made from.
func ownerOf(k string) Owner {
	if r, ok := strings.CutPrefix(k, "repo:"); ok {
		return Owner{Repo: r}
	}
	if t, ok := strings.CutPrefix(k, "task:"); ok {
		if i := strings.LastIndex(t, "/"); i > 0 {
			return Owner{Panel: t[:i], Task: t[i+1:]}
		}
	}
	return Owner{}
}

// claimProject: the project's directory is the one p is in, when it has none yet.
func (s *Store) claimProject(ix *index, projectID, p string) bool {
	if projectID == "" {
		return false
	}
	if _, ok := ix.Projects[projectID]; ok {
		return false
	}
	if r := s.rel(p); !filepath.IsAbs(r) && strings.Contains(r, string(filepath.Separator)) {
		ix.Projects[projectID] = strings.SplitN(r, string(filepath.Separator), 2)[0]
		return true
	}
	return false
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
// created by itself is adopted the same way. Without an owner (a record or a look by session, which knows only the
// path), the folder is the one whose path was reserved there.
func (s *Store) Ensure(p string, o Owner, projectID string) (Folder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensure(p, o, projectID)
}

func (s *Store) ensure(p string, o Owner, projectID string) (Folder, error) {
	p = followRenames(p)
	ix := s.loadIndex()
	rid, ro, reserved := s.reservation(ix, p)
	if o.key() == "" && reserved {
		o = ro
	}
	if f, err := Read(p); err == nil {
		f.Path = p
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
	id := newID()
	if reserved && ro.key() == o.key() {
		id = rid
	}
	f := Folder{ID: id, Owner: o, CreatedAt: time.Now().UTC().Format(time.RFC3339), Path: p}
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
	if s.claimProject(&ix, projectID, f.Path) {
		changed = true
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

// Only letters, digits, "-", "." and "_" stay; everything else (whitespace, punctuation, symbols, full-width ones
// like ？ and ，) goes. A path with spaces has to be quoted in every shell command and unquoted silently becomes
// several arguments; punctuation in the middle of a path cuts the link the chat draws for it short (a title
// "这个 子产品是什么逻辑？" made a folder whose path the chat linked only up to the ？).
var unsafeChars = regexp.MustCompile(`[^\p{L}\p{M}\p{N}._-]+`)

// safeName makes a name usable as one path segment on every system, with only letters, digits and "-._": every run
// of anything else becomes one "_".
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

// RenamePanel moves the tasks' folders of a renamed panel (key old → new) to the new key: in the index and in each
// folder's own folder.json. Without it a task's next record made it a second folder under the new key. The folders
// themselves stay where they are. Returns how many owners moved.
func (s *Store) RenamePanel(old, new string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	ix := s.loadIndex()
	prefix := "task:" + old + "/" // Owner.key of a panel task
	n := 0
	for k, id := range ix.Owners {
		task, ok := strings.CutPrefix(k, prefix)
		if !ok {
			continue
		}
		o := Owner{Panel: new, Task: task}
		delete(ix.Owners, k)
		ix.Owners[o.key()] = id
		n++
		if rel, ok := ix.Folders[id]; ok {
			p := s.abs(rel)
			if f, err := Read(p); err == nil && f.ID == id {
				f.Owner = o
				b, _ := json.MarshalIndent(f, "", "  ")
				_ = writeAtomic(filepath.Join(p, Meta, "folder.json"), b)
			}
		}
	}
	if n > 0 {
		_ = s.saveIndex(ix)
	}
	return n
}
