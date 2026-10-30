package work

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// A run outlives the request that started it (as in Annulo): closing or reloading the page doesn't stop it, the page can
// pick it up again by id, and only Abort (or the run timeout) stops it. Its events stay in memory for keepDone after it
// ends, and in a log file (<GERDR_DIR>/logs/work/<project>/<id>.jsonl) for logKeep. The log has the phases, progress, logs
// and the error with its stack — not the input nor the returned value (they may hold data that shouldn't sit on disk).

const (
	runTimeout = 60 * time.Minute // a function can download, convert, wait on a slow API: give it time, Abort is there
	keepDone   = 10 * time.Minute
	logKeep    = 30 * 24 * time.Hour
	logMax     = 4 << 20 // progress / log lines past this size aren't written
)

// Run is one run of a function.
type Run struct {
	ID      string    `json:"id"`
	Project string    `json:"project"`
	Fn      string    `json:"fn"`
	Started time.Time `json:"started"`
	cancel  context.CancelFunc

	mu     sync.Mutex
	events []Event // progress / log, then one result or error
	done   bool
	wake   chan struct{} // closed and replaced on every event
}

func (rn *Run) push(e Event, final bool) {
	rn.mu.Lock()
	rn.events = append(rn.events, e)
	rn.done = rn.done || final
	close(rn.wake)
	rn.wake = make(chan struct{})
	rn.mu.Unlock()
}

// Since is the events from the nth on, whether the run has ended, and a channel closed at the next event.
func (rn *Run) Since(n int) ([]Event, bool, <-chan struct{}) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	return append([]Event(nil), rn.events[min(n, len(rn.events)):]...), rn.done, rn.wake
}

// Wait blocks until the run ends and returns its result or error.
func (rn *Run) Wait(ctx context.Context) (any, error) {
	n := 0
	for {
		evs, done, wake := rn.Since(n)
		n += len(evs)
		if done {
			last := evs[len(evs)-1]
			d, _ := last.Data.(map[string]any)
			if last.Type == "error" {
				msg, _ := d["message"].(string)
				return nil, errors.New(msg)
			}
			return d["value"], nil
		}
		select {
		case <-wake:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (rn *Run) Abort() { rn.cancel() }

// Start runs a function in the background and returns at once.
func (r *Runner) Start(project, name string, input any) *Run {
	if project == "" {
		project = DefaultProject
	}
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	rn := &Run{ID: fmt.Sprintf("r%d", time.Now().UnixNano()), Project: project, Fn: name, Started: time.Now(), cancel: cancel, wake: make(chan struct{})}
	r.mu.Lock()
	if r.runs == nil {
		r.runs = map[string]*Run{}
	}
	r.runs[rn.ID] = rn
	r.mu.Unlock()
	go func() {
		defer cancel()
		res, err := r.runSafe(ctx, project, name, input, rn.ID, func(e Event) { rn.push(e, false) })
		ms := time.Since(rn.Started).Milliseconds()
		if err != nil {
			d := map[string]any{"message": err.Error(), "ms": ms}
			var fe *Error
			if errors.As(err, &fe) && fe.Stack != "" {
				d["stack"] = fe.Stack
			}
			if r.Log != nil {
				r.Log.Info("local function failed", "project", project, "fn", name, "ms", ms, "err", err)
			}
			rn.push(Event{Type: "error", Data: d}, true)
		} else {
			rn.push(Event{Type: "result", Data: map[string]any{"value": res, "ms": ms}}, true)
		}
		time.AfterFunc(keepDone, func() {
			r.mu.Lock()
			delete(r.runs, rn.ID)
			r.mu.Unlock()
		})
	}()
	return rn
}

// runSafe is run with a panic turned into the run's error, so a waiting page always gets an end.
func (r *Runner) runSafe(ctx context.Context, project, name string, input any, id string, emit func(Event)) (res any, err error) {
	defer func() {
		if p := recover(); p != nil {
			res, err = nil, fmt.Errorf("the function runtime crashed: %v", p)
		}
	}()
	return r.run(ctx, project, name, input, id, emit)
}

// Get is a run that's going or ended less than keepDone ago.
func (r *Runner) Get(id string) *Run {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runs[id]
}

func (r *Runner) logDir(project string) string {
	if project == "" {
		project = DefaultProject
	}
	return filepath.Join(r.Root, "logs", "work", project)
}

// runLog opens the run's log file; the returned report wraps emit, finish writes the end and closes it. Failing to
// open the log never fails the run.
func (r *Runner) runLog(project, id, name string, emit func(Event)) (func(Event), func(error)) {
	dir := r.logDir(project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return emit, func(error) {}
	}
	f, err := os.OpenFile(filepath.Join(dir, id+".jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return emit, func(error) {}
	}
	r.pruneLogs(dir)
	start := time.Now()
	size := 0
	var mu sync.Mutex
	closed := false
	write := func(kind string, data any) {
		if closed || size > logMax && (kind == "progress" || kind == "log") {
			return
		}
		b, err := json.Marshal(map[string]any{"at": time.Now().UTC().Format(time.RFC3339Nano), "type": kind, "data": data})
		if err != nil {
			return
		}
		n, _ := f.Write(append(b, '\n'))
		size += n
	}
	write("start", map[string]any{"run_id": id, "fn": name})
	report := func(e Event) {
		mu.Lock()
		write(e.Type, e.Data)
		mu.Unlock()
		if emit != nil {
			emit(e)
		}
	}
	finish := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		data := map[string]any{"ms": time.Since(start).Milliseconds()}
		if err != nil {
			data["message"] = err.Error()
			var fe *Error
			if errors.As(err, &fe) {
				data["stack"] = fe.Stack
			}
			write("error", data)
		} else {
			write("result", data)
		}
		f.Close()
		closed = true
	}
	return report, finish
}

// pruneLogs drops logs older than logKeep, at most once an hour per directory.
func (r *Runner) pruneLogs(dir string) {
	r.mu.Lock()
	if r.pruned == nil {
		r.pruned = map[string]time.Time{}
	}
	if time.Since(r.pruned[dir]) < time.Hour {
		r.mu.Unlock()
		return
	}
	r.pruned[dir] = time.Now()
	r.mu.Unlock()
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".jsonl" {
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > logKeep {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
}

// LogLine is one line of a run log.
type LogLine struct {
	At   string `json:"at"`
	Type string `json:"type"`
	Data any    `json:"data"`
}

// RunSummary is one run in the log list.
type RunSummary struct {
	ID      string `json:"id"`
	Fn      string `json:"fn"`
	At      string `json:"at"`
	Ms      int64  `json:"ms"`
	Status  string `json:"status"` // ok / error / running (no end line: still going, or gerdr quit meanwhile)
	Message string `json:"message,omitempty"`
}

// Logs lists the project's recent runs, newest first (fn filters by function name, "file" or "file.export").
func (r *Runner) Logs(project, fn string, limit int) ([]RunSummary, error) {
	dir := r.logDir(project)
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []RunSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() > ents[j].Name() }) // ids are r<nanoseconds>
	out := []RunSummary{}
	for _, e := range ents {
		if limit > 0 && len(out) >= limit {
			break
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		if e.IsDir() || id == e.Name() {
			continue
		}
		lines, err := r.LogOf(project, id)
		if err != nil || len(lines) == 0 {
			continue
		}
		s := RunSummary{ID: id, At: lines[0].At, Status: "running"}
		if d, ok := lines[0].Data.(map[string]any); ok {
			s.Fn, _ = d["fn"].(string)
		}
		if fn != "" && s.Fn != fn && !strings.HasPrefix(s.Fn, fn+".") {
			continue
		}
		last := lines[len(lines)-1]
		if d, ok := last.Data.(map[string]any); ok && (last.Type == "result" || last.Type == "error") {
			ms, _ := d["ms"].(float64)
			s.Ms = int64(ms)
			s.Status = map[string]string{"result": "ok", "error": "error"}[last.Type]
			s.Message, _ = d["message"].(string)
		}
		out = append(out, s)
	}
	return out, nil
}

// LogOf is one run's log lines.
func (r *Runner) LogOf(project, id string) ([]LogLine, error) {
	if !strings.HasPrefix(id, "r") || strings.ContainsAny(id, `/\.`) {
		return nil, fmt.Errorf("bad run id %q", id)
	}
	f, err := os.Open(filepath.Join(r.logDir(project), id+".jsonl"))
	if err != nil {
		return nil, fmt.Errorf("no log for run %s", id)
	}
	defer f.Close()
	var out []LogLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var l LogLine
		if json.Unmarshal(sc.Bytes(), &l) == nil {
			out = append(out, l)
		}
	}
	return out, nil
}
