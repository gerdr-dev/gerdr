// Package folder: artifact folders shared between this computer and another one it drives (docs/dev/MULTIHOST.md,
// "Artifact folders"; the idea is shuttle's, docs/collab-local-agent.md there). A folder here is paired with one
// over there; .gerdr-folder.json in it remembers where and every file's sha256 at the last sync, so pull / push can
// tell who changed what: a file changed on one side goes across, one changed on both is kept twice (never merged:
// most of these are images and videos), one deleted on one side is reported, not deleted on the other. No live
// sync: an agent pulls before it works and pushes after.
package folder

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ManifestName is the file in a folder here that pairs it; never synced itself.
const ManifestName = ".gerdr-folder.json"

// IgnoreName: one pattern per line (a name, or a path from the folder's root; * ? [..] as in filepath.Match; a
// trailing / for folders only; # comments). Synced like any file, so both sides skip the same things.
const IgnoreName = ".gerdrignore"

// MaxFiles bounds a folder: a tree past it is refused rather than half synced (it's almost always node_modules or
// a render cache someone forgot to ignore).
const MaxFiles = 20000

// Always skipped, whatever .gerdrignore says.
var builtinIgnore = []string{".git/", "node_modules/", ".DS_Store", ManifestName, "*.part", ".gerdr-tmp-*"}

// junk: what even a plain copy (Options.All) skips: half-written files of a copy in progress, and the pairing of an
// artifact folder (a copy of a folder isn't paired with anything).
var junk = []string{ManifestName, "*.part", ".gerdr-tmp-*"}

type Entry struct {
	Path  string `json:"path"` // relative, with /
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"` // unix ms
	Sha   string `json:"sha256"`
	Mode  uint32 `json:"mode,omitempty"` // permission bits (0755…), so a copied program stays runnable
}

// Options for Walk. The zero value is an artifact folder's walk: built-in ignores plus .gerdrignore.
type Options struct {
	All     bool     // every file (`gerdr host cp`): no built-in ignores, no .gerdrignore; only half-written copies skipped
	Exclude []string // more patterns, .gerdrignore syntax
}

var ErrTooMany = errors.New("too many files (over 20000): add the big folders to .gerdrignore")

// Walk lists the files under root (sorted), hashed. Hashes are cached by path + size + mtime, so a second look at a
// big folder of videos doesn't read them all again.
func Walk(root string) ([]Entry, error) { return WalkWith(root, Options{}) }

// WalkWith is Walk with Options.
func WalkWith(root string, o Options) ([]Entry, error) {
	ign := append(ignore{}, junk...)
	if !o.All {
		ign = loadIgnore(root)
	}
	ign = append(ign, o.Exclude...)
	var out []Entry
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil // unreadable entries are skipped, not fatal
		}
		if p == root {
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator)))
		if ign.match(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if len(out) >= MaxFiles {
			return ErrTooMany
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		sha, err := Hash(p, info)
		if err != nil {
			return nil
		}
		out = append(out, Entry{Path: rel, Size: info.Size(), Mtime: info.ModTime().UnixMilli(), Sha: sha, Mode: uint32(info.Mode().Perm())})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

type cached struct {
	size  int64
	mtime time.Time
	sha   string
}

var (
	cacheMu sync.Mutex
	cache   = map[string]cached{}
)

// Hash: a file's sha256, from the cache when its size and mtime haven't changed.
func Hash(p string, info fs.FileInfo) (string, error) {
	cacheMu.Lock()
	c, ok := cache[p]
	cacheMu.Unlock()
	if ok && c.size == info.Size() && c.mtime.Equal(info.ModTime()) {
		return c.sha, nil
	}
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	sha := hex.EncodeToString(h.Sum(nil))
	cacheMu.Lock()
	if len(cache) > 200000 {
		cache = map[string]cached{}
	}
	cache[p] = cached{info.Size(), info.ModTime(), sha}
	cacheMu.Unlock()
	return sha, nil
}

type ignore []string

func loadIgnore(root string) ignore {
	ig := append(ignore{}, builtinIgnore...)
	b, err := os.ReadFile(filepath.Join(root, IgnoreName))
	if err != nil {
		return ig
	}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		ig = append(ig, l) // a leading / anchors it at the root (match)
	}
	return ig
}

func (ig ignore) match(rel string, dir bool) bool {
	base := path.Base(rel)
	for _, p := range ig {
		onlyDir := strings.HasSuffix(p, "/")
		p = strings.TrimSuffix(p, "/")
		if onlyDir && !dir {
			continue
		}
		target := base
		if strings.HasPrefix(p, "/") || strings.Contains(p, "/") {
			p, target = strings.TrimPrefix(p, "/"), rel // anchored, or a path: from the folder's root
		}
		if ok, _ := path.Match(p, target); ok {
			return true
		}
	}
	return false
}

// Peer: the other side of a pair. Host "" = this computer (a folder paired with another folder here: only tests).
type Peer struct {
	Host string `json:"host"`
	Path string `json:"path"` // absolute, on that computer
}

// Manifest is .gerdr-folder.json: who the folder is paired with and each file's sha256 at the last sync (the base
// both sides are compared with).
type Manifest struct {
	Peer   Peer              `json:"peer"`
	Synced int64             `json:"synced"` // unix ms of the last pull or push
	Files  map[string]string `json:"files"`  // relative path → sha256
	// Copies: conflict copies (ConflictName) a sync made, kept on one side only by design: not "deleted there".
	Copies map[string]bool `json:"copies,omitempty"`
}

// Quiet drops what isn't news from a plan: a conflict copy missing on the other side is how it's meant to be.
func (m *Manifest) Quiet(plan []Change) []Change {
	out := plan[:0]
	for _, c := range plan {
		if c.Action == "deletedThere" && m.Copies[c.Path] {
			continue
		}
		out = append(out, c)
	}
	return out
}

func LoadManifest(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m.Files == nil {
		m.Files = map[string]string{}
	}
	if m.Copies == nil {
		m.Copies = map[string]bool{}
	}
	return &m, nil
}

func (m *Manifest) Save(dir string) error {
	m.Synced = time.Now().UnixMilli()
	b, _ := json.MarshalIndent(m, "", "  ")
	tmp := filepath.Join(dir, ManifestName+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, ManifestName))
}

// ConflictName: where the other side's version of a file both sides changed goes ("cover (Mac mini).png").
func ConflictName(rel, who string) string {
	ext := path.Ext(rel)
	stem := strings.TrimSuffix(rel, ext)
	who = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < 32 {
			return '-'
		}
		return r
	}, who)
	return stem + " (" + who + ")" + ext
}

// Change: what a sync does with one file.
type Change struct {
	Path   string `json:"path"`
	Action string `json:"action"`           // copy | conflict | deletedThere | deletedHere
	As     string `json:"as,omitempty"`     // conflict: the name the other side's version got
	Detail string `json:"detail,omitempty"` // an error, for a file that failed
}

// Plan compares the side the changes come from (src) with the side they go to (dst), both against base, and says
// what to do on dst. A file changed on src only is copied; on both, kept twice (src's version under a conflict name
// on dst); gone from src but unchanged on dst is reported as deleted there, not removed.
func Plan(base map[string]string, src, dst []Entry) []Change {
	s, d := index(src), index(dst)
	var out []Change
	for _, e := range src {
		b, inBase := base[e.Path]
		cur, onDst := d[e.Path]
		switch {
		case onDst && cur == e.Sha:
			// same on both sides already
		case inBase && b == e.Sha:
			// src didn't change it; dst did (or deleted it): nothing to bring
		case !onDst && (!inBase || b == ""):
			out = append(out, Change{Path: e.Path, Action: "copy"}) // new on src
		case !onDst:
			out = append(out, Change{Path: e.Path, Action: "deletedHere"}) // changed there, deleted here: keep it gone, say so
		case inBase && b == cur:
			out = append(out, Change{Path: e.Path, Action: "copy"}) // dst untouched since: take src's
		default:
			out = append(out, Change{Path: e.Path, Action: "conflict"}) // both changed (or both new and different)
		}
	}
	for p, b := range base {
		if _, ok := s[p]; ok {
			continue
		}
		if cur, ok := d[p]; ok && cur == b {
			out = append(out, Change{Path: p, Action: "deletedThere"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func index(es []Entry) map[string]string {
	m := make(map[string]string, len(es))
	for _, e := range es {
		m[e.Path] = e.Sha
	}
	return m
}
