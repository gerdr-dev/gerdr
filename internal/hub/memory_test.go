package hub

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeLog(t *testing.T, dir, id string, evs ...*Event) {
	t.Helper()
	var buf bytes.Buffer
	for _, e := range evs {
		b, _ := json.Marshal(e)
		buf.Write(append(b, '\n'))
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions", id+".jsonl"), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newHubWith(t *testing.T, dir string, ids ...string) *Hub {
	t.Helper()
	var recs []Record
	for i, id := range ids {
		recs = append(recs, Record{ID: id, Agent: "claude", AgentSessionID: "a-" + id, CreatedAt: int64(i)})
	}
	b, _ := json.Marshal(recs)
	os.WriteFile(filepath.Join(dir, "sessions", "index.json"), b, 0o600)
	h, err := New(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func hubDir(t *testing.T) string {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sessions"), 0o700)
	return dir
}

// Startup only scans the logs: nothing is loaded, yet what a restart has to close is closed and the sequence numbers
// carry on from the logs.
func TestStartupScansWithoutLoading(t *testing.T) {
	dir := hubDir(t)
	writeLog(t, dir, "s1",
		&Event{Seq: 3, TS: 10, Kind: EvUser, MessageID: "m1"},
		&Event{Seq: 4, TS: 11, Kind: EvTurnStart, MessageID: "m1"},
		&Event{Seq: 5, TS: 12, Kind: EvShell, MessageID: "sh1", Text: "ls"},
		&Event{Seq: 6, TS: 13, Kind: EvPermission, ReqID: "r1"},
		&Event{Seq: 7, TS: 14, Kind: EvPermission, ReqID: "r2"},
		&Event{Seq: 8, TS: 15, Kind: EvPermissionDone, ReqID: "r2"},
	)
	writeLog(t, dir, "s2", &Event{Seq: 20, TS: 1, Kind: EvUser, MessageID: "m2"}, &Event{Seq: 21, TS: 2, Kind: EvTurnEnd})
	h := newHubWith(t, dir, "s1", "s2")
	s1, s2 := h.sessions["s1"], h.sessions["s2"]
	if s1.loaded || s2.loaded {
		t.Fatal("startup loaded a log")
	}
	if s1.orphanSince != 15 || s2.orphanSince != 0 {
		t.Fatalf("orphanSince = %d, %d", s1.orphanSince, s2.orphanSince)
	}
	if h.seq < 21 {
		t.Fatalf("seq %d didn't carry on from the logs", h.seq)
	}
	if got := s2.Summary().LastSeq; got != 21 {
		t.Fatalf("s2 LastSeq = %d", got)
	}
	evs := s1.Events()
	var kinds []string
	for _, e := range evs[6:] {
		kinds = append(kinds, e.Kind+":"+e.MessageID+e.ReqID)
	}
	if want := []string{"turn_end:", "shell_done:sh1", "permission_done:r1"}; strings.Join(kinds, " ") != strings.Join(want, " ") {
		t.Fatalf("recovered %q, want %q", kinds, want)
	}
	if last := evs[len(evs)-1].Seq; s1.Summary().LastSeq != last {
		t.Fatalf("LastSeq %d, last event %d", s1.Summary().LastSeq, last)
	}
}

// A log from before images moved out: loading it puts them in blobs/ and rewrites the file without them; a rewind /
// withdraw hands the bytes back.
func TestImagesLeaveTheLog(t *testing.T) {
	dir := hubDir(t)
	png := bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, 2048)
	data := base64.StdEncoding.EncodeToString(png)
	img, _ := json.Marshal(map[string]string{"type": "image", "mimeType": "image/png", "data": data})
	txt, _ := json.Marshal(map[string]string{"type": "text", "text": "看这张图"})
	writeLog(t, dir, "s1", &Event{Seq: 1, Kind: EvUser, MessageID: "m1", Content: []json.RawMessage{txt, img}})
	h := newHubWith(t, dir, "s1")
	evs := h.sessions["s1"].Events()
	if len(evs) != 1 || bytes.Contains(evs[0].Content[1], []byte(data)) || !bytes.Contains(evs[0].Content[1], []byte(BlobURL)) {
		t.Fatalf("image still inline in memory: %s", evs[0].Content[1])
	}
	file, _ := os.ReadFile(filepath.Join(dir, "sessions", "s1.jsonl"))
	if bytes.Contains(file, []byte(data)) {
		t.Fatal("log not rewritten without the image")
	}
	back := h.inflate(evs[0].Content)
	var b struct{ Type, MimeType, Data, Blob string }
	json.Unmarshal(back[1], &b)
	if b.Data != data || b.Blob != "" || b.MimeType != "image/png" || string(back[0]) != string(txt) {
		t.Fatalf("inflate: %s", back[1])
	}
}

// An idle session drops its events and reads them back on the next access; one that's running keeps them.
func TestUnloadIdle(t *testing.T) {
	dir := hubDir(t)
	writeLog(t, dir, "s1", &Event{Seq: 1, Kind: EvUser, MessageID: "m1"}, &Event{Seq: 2, Kind: EvTurnEnd})
	writeLog(t, dir, "s2", &Event{Seq: 3, Kind: EvUser, MessageID: "m2"})
	h := newHubWith(t, dir, "s1", "s2")
	s1, s2 := h.sessions["s1"], h.sessions["s2"]
	s1.Events()
	s2.Events()
	s2.running = true
	s1.lastUse = time.Now().Add(-2 * unloadAfter)
	s2.lastUse = s1.lastUse
	h.unloadIdle()
	if s1.loaded || !s2.loaded {
		t.Fatalf("loaded after unloadIdle: s1 %v s2 %v", s1.loaded, s2.loaded)
	}
	if s1.Summary().LastSeq != 2 {
		t.Fatalf("LastSeq %d", s1.Summary().LastSeq)
	}
	s1.emit(&Event{Kind: EvNotice, Level: "info", Text: "hi"})
	s1.flush(true)
	evs, _ := readLog(s1.lf.Path)
	if !s1.loaded || len(s1.Events()) != 3 || len(evs) != 3 {
		t.Fatalf("emit on an unloaded session: %d in memory, %d on disk", len(s1.Events()), len(evs))
	}
}

// Over budget, the least recently used idle sessions go first, even before unloadAfter.
func TestUnloadOverBudget(t *testing.T) {
	dir := hubDir(t)
	big := strings.Repeat("x", loadedBudget/2+1024)
	for _, id := range []string{"a", "b", "c"} {
		writeLog(t, dir, id, &Event{Seq: 1, Kind: EvNotice, Text: big})
	}
	h := newHubWith(t, dir, "a", "b", "c")
	for i, id := range []string{"a", "b", "c"} {
		s := h.sessions[id]
		s.Events()
		s.lastUse = time.Now().Add(-time.Duration(3-i) * time.Minute / 2) // a oldest, all past unloadGrace
	}
	h.unloadIdle()
	if h.sessions["a"].loaded || h.sessions["b"].loaded || !h.sessions["c"].loaded {
		t.Fatalf("loaded: a %v b %v c %v", h.sessions["a"].loaded, h.sessions["b"].loaded, h.sessions["c"].loaded)
	}
}
