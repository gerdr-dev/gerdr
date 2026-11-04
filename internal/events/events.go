// Package events is Gerdr's event model (docs/dev/EVENTS.md): things that happened outside a conversation — a new
// review comment, a failed pipeline, a chat message, a mail — as one record shape, kept and handed to whoever
// subscribed: a panel / Work (triggers/*.json), a Code session (a watch), an add-on (its UI, its service).
//
// Gerdr keeps facts, not decisions (like turns, docs/dev/ADDONS.md): an event says what happened; what it means and
// what to do about it is the subscriber's. Gerdr pulls nothing itself: sources are polls written as Work functions
// on a schedule, add-on services, or anyone with the CLI / API, and they emit what they found.
package events

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Event is one thing that happened.
type Event struct {
	// ID: the emitter's stable id for this happening ("gitlab:note:123"), so polling the same thing twice emits it
	// once: an id already seen (this month or last) is dropped. Empty = a fresh one, never deduplicated.
	ID string `json:"id"`
	// Source: who it comes from (gitlab, ci, feishu, mail, an add-on's id). Kind: what happened, dotted
	// (mr.note, pipeline.failed, message). Both [A-Za-z0-9._-], no "/": patterns are "source/kind".
	Source string `json:"source"`
	Kind   string `json:"kind"`
	// Key: what it is about (group/repo!34, a chat's id), for subscribing to one thing.
	Key string `json:"key,omitempty"`
	// Title / Text / URL: what a person or an agent reads (a session watching it gets these as its message).
	Title string `json:"title,omitempty"`
	Text  string `json:"text,omitempty"`
	URL   string `json:"url,omitempty"`
	// Data: the rest, for code.
	Data json.RawMessage `json:"data,omitempty"`
	// Reply: how to answer it, filled in by the source (a chat message: post into its thread). A subscriber answers
	// without knowing the source by emitting {kind: Reply.Kind, data: Reply.Data + {text}} (ctx.events.reply), and
	// the source, subscribed to that kind, delivers it. Absent: there's nowhere to answer.
	Reply *Reply `json:"reply,omitempty"`
	// At: unix ms, when it was emitted.
	At int64 `json:"at"`
	// Depth: how many triggers deep it was emitted (a trigger's function emitting another event); events past
	// MaxDepth aren't delivered, so two triggers feeding each other stop.
	Depth int `json:"depth,omitempty"`
}

// Reply is where an answer to an event goes.
type Reply struct {
	Kind string          `json:"kind"`           // the event kind the source listens for (slack.send)
	Data json.RawMessage `json:"data,omitempty"` // what it needs besides the text (channel, thread)
}

// Source is one kind of event someone can subscribe to: what an add-on says it emits (addon.json "emits"), or a
// source/kind seen recently. For pickers ("which events make a task?"), so nothing is hard-coded per source.
type Source struct {
	On          string `json:"on"` // source/kind
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Addon       string `json:"addon,omitempty"` // the add-on that emits it
	CanReply    bool   `json:"canReply,omitempty"`
	// Seen: unix ms of the latest one in the log (0 = none yet in the last 30 days)
	Seen int64 `json:"seen,omitempty"`
}

// MaxDepth: an event emitted by a trigger run is one deeper than the event that started the run.
const MaxDepth = 4

const (
	maxText = 20_000
	maxData = 256 << 10
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func (e *Event) check() error {
	if !nameRe.MatchString(e.Source) {
		return fmt.Errorf("event source %q: letters, digits, . _ - (no /)", e.Source)
	}
	if !nameRe.MatchString(e.Kind) {
		return fmt.Errorf("event kind %q: letters, digits, . _ - (no /)", e.Kind)
	}
	if len(e.ID) > 200 || len(e.Key) > 500 || len(e.Title) > 500 || len(e.URL) > 2000 {
		return errors.New("event id / key / title / url too long")
	}
	if len(e.Text) > maxText {
		e.Text = e.Text[:maxText]
	}
	if len(e.Data) > maxData {
		return fmt.Errorf("event data over %d KB", maxData>>10)
	}
	if len(e.Data) > 0 && !json.Valid(e.Data) {
		return errors.New("event data is not JSON")
	}
	if e.Reply != nil && (!nameRe.MatchString(e.Reply.Kind) || len(e.Reply.Data) > 0 && !json.Valid(e.Reply.Data)) {
		return errors.New("event reply: a kind ([A-Za-z0-9._-]) and JSON data")
	}
	return nil
}

// Store keeps events in <root>/events/<yyyy-mm>.jsonl and tells its listeners about each new one.
type Store struct {
	dir string
	now func() time.Time
	// Declared: the sources someone says they emit (the add-ons' addon.json "emits"), for Catalog. nil = none.
	Declared func() []Source

	mu        sync.Mutex
	seen      map[string]bool // ids in this month's and last month's files
	seenMonth string          // the month seen was built for; a new month drops the one before last
	listeners []func(Event)

	watches *watchFile
}

// Open reads the ids of the last two months (for deduplication) and the watches.
func Open(root string) (*Store, error) {
	s := &Store{dir: filepath.Join(root, "events"), now: time.Now, watches: &watchFile{path: filepath.Join(root, "state", "event-watches.json")}}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	s.loadSeen()
	if err := s.watches.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func month(t time.Time) string { return t.Format("2006-01") }

func (s *Store) loadSeen() {
	now := s.now()
	s.seen, s.seenMonth = map[string]bool{}, month(now)
	for _, m := range []string{month(now.AddDate(0, -1, 0)), month(now)} {
		_ = readFile(filepath.Join(s.dir, m+".jsonl"), func(e Event) bool {
			if e.ID != "" {
				s.seen[e.ID] = true
			}
			return true
		})
	}
}

// OnEvent registers fn for every new event (called outside the store's lock, in emit order per emitter).
func (s *Store) OnEvent(fn func(Event)) {
	s.mu.Lock()
	s.listeners = append(s.listeners, fn)
	s.mu.Unlock()
}

// Emit records an event and tells the listeners. fresh is false when its id was already seen (nothing happens then).
func (s *Store) Emit(e Event) (out Event, fresh bool, err error) {
	if err := e.check(); err != nil {
		return e, false, err
	}
	now := s.now()
	s.mu.Lock()
	if month(now) != s.seenMonth {
		s.loadSeen()
	}
	if e.ID == "" {
		e.ID = "e-" + randID()
	} else if s.seen[e.ID] {
		s.mu.Unlock()
		return e, false, nil
	}
	if e.At == 0 {
		e.At = now.UnixMilli()
	}
	b, _ := json.Marshal(e)
	f, err := os.OpenFile(filepath.Join(s.dir, month(now)+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_, err = f.Write(append(b, '\n'))
		_ = f.Close()
	}
	if err != nil {
		s.mu.Unlock()
		return e, false, err
	}
	s.seen[e.ID] = true
	ls := append([]func(Event){}, s.listeners...)
	s.mu.Unlock()
	for _, fn := range ls {
		fn(e)
	}
	return e, true, nil
}

func randID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Query narrows List.
type Query struct {
	Since int64  // unix ms; 0 = the last 24 hours
	On    string // a pattern (Match); "" = all
	Key   string // a key pattern; "" = any
	Limit int    // the newest n; 0 = 100
}

// List: matching events since q.Since, oldest first, the newest q.Limit of them.
func (s *Store) List(q Query) ([]Event, error) {
	since := q.Since
	if since == 0 {
		since = s.now().Add(-24 * time.Hour).UnixMilli()
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	files, _ := filepath.Glob(filepath.Join(s.dir, "*.jsonl"))
	sort.Strings(files)
	first := month(time.UnixMilli(since))
	var out []Event
	for _, f := range files {
		if strings.TrimSuffix(filepath.Base(f), ".jsonl") < first {
			continue
		}
		if err := readFile(f, func(e Event) bool {
			if e.At >= since && Match(q.On, e) && MatchKey(q.Key, e.Key) {
				out = append(out, e)
				if len(out) > limit {
					out = out[1:]
				}
			}
			return true
		}); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func readFile(path string, fn func(Event) bool) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if !fn(e) {
			break
		}
	}
	return sc.Err()
}

// Match: does pattern cover the event? "source/kind" with * globs on each side ("gitlab/mr.*", "*/pipeline.failed");
// without a "/" it is the kind from any source ("mr.note"); "" and "*" match everything.
func Match(pattern string, e Event) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || pattern == "*" {
		return true
	}
	src, kind, ok := strings.Cut(pattern, "/")
	if !ok {
		return glob(pattern, e.Kind)
	}
	return glob(src, e.Source) && glob(kind, e.Kind)
}

// MatchKey: a key pattern, * matching anything (slashes too: keys are paths like group/repo!34). "" = any key.
func MatchKey(pattern, key string) bool {
	return pattern == "" || glob(pattern, key)
}

// CheckPattern: why a subscription's pattern can't match anything ("" when it's fine).
func CheckPattern(pattern string) error {
	src, kind, ok := strings.Cut(strings.TrimSpace(pattern), "/")
	if !ok {
		kind, src = src, "*"
	}
	if strings.Contains(kind, "/") {
		return fmt.Errorf("pattern %q: one / at most (source/kind)", pattern)
	}
	for _, p := range []string{src, kind} {
		if p == "" {
			return fmt.Errorf("pattern %q: empty source or kind (use *)", pattern)
		}
	}
	return nil
}

// glob: * matches any run of characters, everything else itself.
func glob(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(s, p)
		if i < 0 {
			return false
		}
		s = s[i+len(p):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}

// Summary: the event as a message to an agent — a header line, then the text and the link.
func (e Event) Summary(header string) string {
	var b strings.Builder
	b.WriteString(header)
	if e.Title != "" {
		b.WriteString(" " + e.Title)
	}
	if e.Key != "" {
		fmt.Fprintf(&b, " (%s)", e.Key)
	}
	if e.Text != "" {
		b.WriteString("\n\n" + e.Text)
	}
	if e.URL != "" {
		b.WriteString("\n\n" + e.URL)
	}
	return b.String()
}

// Catalog: the declared sources (add-ons' "emits") merged with every source/kind seen in the last 30 days, declared
// ones first, each with when it was last seen.
func (s *Store) Catalog() []Source {
	var declared []Source
	if s.Declared != nil {
		declared = s.Declared()
	}
	seen := map[string]int64{}
	replies := map[string]bool{} // kinds that are answers to events (Reply.Kind): not something to subscribe to
	since := s.now().AddDate(0, 0, -30).UnixMilli()
	files, _ := filepath.Glob(filepath.Join(s.dir, "*.jsonl"))
	first := month(time.UnixMilli(since))
	for _, f := range files {
		if strings.TrimSuffix(filepath.Base(f), ".jsonl") < first {
			continue
		}
		_ = readFile(f, func(e Event) bool {
			if e.Reply != nil {
				replies[e.Reply.Kind] = true
			}
			if e.At >= since {
				on := e.Source + "/" + e.Kind
				if e.At > seen[on] {
					seen[on] = e.At
				}
			}
			return true
		})
	}
	out := []Source{}
	known := map[string]bool{}
	for _, d := range declared {
		d.Seen = seen[d.On]
		known[d.On] = true
		out = append(out, d)
	}
	var rest []Source
	for on, at := range seen {
		if _, kind, _ := strings.Cut(on, "/"); !known[on] && !replies[kind] {
			rest = append(rest, Source{On: on, Seen: at})
		}
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].Seen > rest[j].Seen })
	return append(out, rest...)
}
