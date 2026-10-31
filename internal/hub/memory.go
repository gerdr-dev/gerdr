package hub

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"
)

// Keeping the backend small no matter how many conversations there are.
//
// Measured (2026-10, 171 sessions, 280 MB of logs): loading every log at startup and keeping it held ~400 MB live,
// and with Go's GC letting the heap grow to twice that, the process sat at a ~900 MB footprint. Two thirds of the
// bytes were pasted screenshots (base64), the rest mostly tool output of conversations nobody had open.
//
//   - Images never stay in an event: emit and loadLocked swap them for blobs/<sha256> (slim, blob.go), and a log that
//     still had them inline is rewritten slim the first time it's loaded. Where the bytes are needed again (a message
//     put back into the composer by rewind / withdraw, then sent), inflate reads them back.
//   - A session's events are loaded on first use and dropped again once it has been idle a while (unloadIdle), or
//     sooner, oldest first, when the loaded ones add up to more than loadedBudget. Startup only scans each log for
//     what recoverAfterRestart needs (scanLog) instead of loading it.
//   - main sets a soft memory limit (GOMEMLIMIT) so the GC doesn't let garbage grow to twice the live heap.

const (
	// unloadAfter: a session nobody touched for this long drops its events (re-reading a slim log is a few ms).
	unloadAfter = 3 * time.Minute
	// loadedBudget: evSize total of all loaded sessions before the least recently used idle ones are dropped early.
	loadedBudget = 32 << 20
	// unloadGrace: never drop a session used this recently, even over budget (an operation between two locks — rewind
	// waiting on the agent — would just load it again, but there's no point churning).
	unloadGrace = 10 * time.Second
)

// loadLocked makes s.events hold the log (reading it from disk if it was dropped). Every access to s.events calls it.
func (s *Session) loadLocked() {
	s.lastUse = time.Now()
	if s.loaded {
		return
	}
	evs, slimmed, err := s.h.readSlim(s.lf.Path)
	if err != nil {
		s.h.log.Warn("read session log", "id", s.rec.ID, "err", err)
	}
	s.events, s.loaded = evs, true
	s.lf.Flushed = len(evs)
	if slimmed {
		// A log from before images moved out: write it back without them, so the next load is cheap too.
		if err := s.lf.Rewrite(evs); err != nil {
			s.h.log.Warn("rewrite log", "id", s.rec.ID, "err", err)
		}
	}
}

// unloadableLocked: nothing in flight could need the events in memory, and all of them are on disk.
func (s *Session) unloadableLocked() bool {
	return s.loaded && !s.running && !s.catchingUp && !s.adopting && len(s.pending) == 0 && len(s.shells) == 0 &&
		len(s.sending) == 0 && s.retryCancel == nil && s.lf.Flushed == len(s.events)
}

func (s *Session) unloadLocked() {
	if n := len(s.events); n > 0 {
		s.lastSeq = s.events[n-1].Seq
	}
	s.events, s.loaded = nil, false
}

// unloadIdle drops the events of sessions idle longer than unloadAfter, then, while the loaded ones still add up to
// more than loadedBudget, those of the least recently used. Runs from the flusher.
func (h *Hub) unloadIdle() {
	h.mu.Lock()
	ss := make([]*Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		ss = append(ss, s)
	}
	h.mu.Unlock()
	type cand struct {
		s    *Session
		used time.Time
		size int
	}
	var cands []cand
	total := 0
	for _, s := range ss {
		s.mu.Lock()
		if !s.loaded {
			s.mu.Unlock()
			continue
		}
		size := 0
		for _, e := range s.events {
			size += evSize(e)
		}
		total += size
		idle := time.Since(s.lastUse)
		switch {
		case !s.unloadableLocked() || idle < unloadGrace:
		case idle > unloadAfter:
			s.unloadLocked()
			total -= size
		default:
			cands = append(cands, cand{s, s.lastUse, size})
		}
		s.mu.Unlock()
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].used.Before(cands[j].used) })
	for _, c := range cands {
		if total <= loadedBudget {
			break
		}
		c.s.mu.Lock()
		if c.s.unloadableLocked() && c.s.lastUse.Equal(c.used) {
			c.s.unloadLocked()
			total -= c.size
		}
		c.s.mu.Unlock()
	}
}

// readSlim reads a log with its images moved to blobs/, one line at a time so the inline base64 is garbage right away
// instead of all of it being held until the end. slimmed: some event still had an image inline.
func (h *Hub) readSlim(path string) (evs []*Event, slimmed bool, err error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 128<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		p := h.slim(&e)
		if p != &e {
			slimmed = true
		}
		evs = append(evs, p)
	}
	return evs, slimmed, sc.Err()
}

// logScan is what startup needs from a log without loading it (recoverAfterRestart).
type logScan struct {
	lastSeq, lastTS int64
	open            bool     // a turn_start without its turn_end
	shells          []string // `!command`s with no shell_done
	unanswered      []*Event // permission / ask with no _done (Kind and ReqID only)
}

func scanLog(path string) (logScan, error) {
	var out logScan
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	defer f.Close()
	shellDone, answered := map[string]bool{}, map[string]bool{}
	var shells []string
	var reqs []*Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 128<<20)
	for sc.Scan() {
		// Only the small fields: Unmarshal skips the big ones without copying them.
		var e struct {
			Seq       int64  `json:"seq"`
			TS        int64  `json:"ts"`
			Kind      string `json:"kind"`
			MessageID string `json:"messageId"`
			ReqID     string `json:"reqId"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		out.lastSeq = max(out.lastSeq, e.Seq)
		out.lastTS = e.TS
		switch e.Kind {
		case EvTurnStart:
			out.open = true
		case EvTurnEnd:
			out.open = false
		case EvShell:
			shells = append(shells, e.MessageID)
		case EvShellDone:
			shellDone[e.MessageID] = true
		case EvPermission, EvAsk:
			reqs = append(reqs, &Event{Kind: e.Kind, ReqID: e.ReqID})
		case EvPermissionDone, EvAskDone:
			answered[e.ReqID] = true
		}
	}
	for _, id := range shells {
		if !shellDone[id] {
			out.shells = append(out.shells, id)
		}
	}
	for _, r := range reqs {
		if !answered[r.ReqID] {
			out.unanswered = append(out.unanswered, r)
		}
	}
	return out, sc.Err()
}

// inflate puts the bytes back into image blocks that slim pointed at blobs/ (content going back to the composer,
// which sends it to the agent as is). A blob that's gone leaves the block as it was.
func (h *Hub) inflate(blocks []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, len(blocks))
	for i, raw := range blocks {
		out[i] = raw
		if !bytes.Contains(raw, []byte(`"blob"`)) {
			continue
		}
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil || m["type"] != "image" {
			continue
		}
		url, _ := m["blob"].(string)
		p := h.BlobPath(strings.TrimPrefix(url, BlobURL))
		if p == "" {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		delete(m, "blob")
		m["data"] = base64.StdEncoding.EncodeToString(b)
		if nb, err := json.Marshal(m); err == nil {
			out[i] = nb
		}
	}
	return out
}
