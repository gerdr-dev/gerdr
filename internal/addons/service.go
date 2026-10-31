package addons

// Add-on services: an add-on's addon.json can name a long-running process ({"service": {"command": …}}). gerdr's
// backend starts it, restarts it when it dies, and stops it when the add-on is removed, switched off or its
// addon.json changes.
//
// Why the backend and not the UI: an add-on's UI half runs in every window that is open — the App, a remote browser,
// the phone — and none of them while no window is. Something that owns a device (a Bluetooth wristband accepts one
// connection at a time) or has to keep running needs exactly one copy, alive as long as gerdr is: the backend.
//
// Protocol: newline-delimited JSON over stdin / stdout, one object per line with a "type".
//
//	gerdr → service (stdin)
//	  {"type":"hello","version":"…","addon":"<id>"}     first line of every start
//	  {"type":"sessions","sessions":[…]}                the open sessions; right after hello, then whenever one of
//	                                                    the fields changes (not on every token: see server/addonsvc.go)
//	  {"type":"message","data":…}                       sent by the add-on's UI half (g.service.send)
//	service → gerdr (stdout)
//	  {"type":"status","text":"…"}                      one line shown under the add-on in Settings → Add-ons
//	  {"type":"message","data":…}                       to the add-on's UI half in every open window (g.service.on)
//
// stdin closing means gerdr is going away: the service should clean up (close its device) and exit. gerdr waits a few
// seconds for that before it kills the process group. stderr goes to <dir>/logs/addons/<id>.log.
//
// GERDR_ADDON_DATA (<dir>/addon-data/<id>) is where a service keeps its files. Not the add-on's folder: every change
// there counts as the add-on being edited (Manager.Watch), so a service writing into it would reload the add-ons in
// every open window once a second — and an update from a store replaces that folder.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
)

type ServiceSpec struct {
	// Command: a name looked up on PATH (the login shell's, like agents get), an absolute path, or a path relative to
	// the add-on's folder ("./bin/x"). ~ is expanded.
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// ServiceStatus is what Settings → Add-ons and g.service.status() show.
type ServiceStatus struct {
	State    string `json:"state"`           // starting / running / exited (waiting to restart) / stopped
	PID      int    `json:"pid,omitempty"`   // while running
	Text     string `json:"text,omitempty"`  // the service's own one-liner ({"type":"status"})
	Error    string `json:"error,omitempty"` // why it is not running: command not found, last exit + stderr tail
	Restarts int    `json:"restarts,omitempty"`
	Since    int64  `json:"since"` // unix ms of the last state change
}

// Services runs the add-ons' services. The zero value is not usable: NewServices.
type Services struct {
	m       *Manager
	log     *slog.Logger
	logDir  string
	dataDir string
	// Env is the environment a service starts with (sysenv.ChildEnv: login PATH, gerdr on PATH, GERDR_DIR, no secrets).
	Env func() []string
	// Greeting: the lines a freshly started service gets first (hello, the current sessions).
	Greeting func(id string) [][]byte
	// OnStatus / OnMessage: pushed to the UI (hub broadcasts).
	OnStatus  func(id string, st ServiceStatus)
	OnMessage func(id string, data json.RawMessage)

	syncMu sync.Mutex // one Sync at a time: an old process must be gone before its replacement starts
	mu     sync.Mutex
	procs  map[string]*svcProc
	closed bool
}

type svcProc struct {
	id, dir string
	spec    ServiceSpec
	stop    chan struct{}
	done    chan struct{}

	// guarded by Services.mu
	st ServiceStatus
	in chan []byte // stdin of the current run; nil while not running
}

// NewServices: logs and per-add-on data go under the manager's root (gerdr's data directory).
func NewServices(m *Manager, log *slog.Logger) *Services {
	return &Services{m: m, log: log, logDir: filepath.Join(m.root, "logs", "addons"), dataDir: filepath.Join(m.root, "addon-data"),
		procs: map[string]*svcProc{}}
}

// Sync starts, stops and restarts services to match the add-on directory. Call it at startup and whenever the
// directory changes (Manager.Watch).
func (s *Services) Sync() {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	want := s.m.services()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	var stopping []*svcProc
	for id, p := range s.procs {
		w, ok := want[id]
		if !ok || !reflect.DeepEqual(w.spec, p.spec) || w.dir != p.dir {
			stopping = append(stopping, p)
			delete(s.procs, id)
		}
	}
	var starting []*svcProc
	for id, w := range want {
		if _, ok := s.procs[id]; ok {
			continue
		}
		p := &svcProc{id: id, dir: w.dir, spec: w.spec, stop: make(chan struct{}), done: make(chan struct{}),
			st: ServiceStatus{State: "starting", Since: time.Now().UnixMilli()}}
		s.procs[id] = p
		starting = append(starting, p)
	}
	s.mu.Unlock()
	for _, p := range stopping {
		close(p.stop)
	}
	// A changed addon.json: the old process has to be gone before the new one opens the same device.
	for _, p := range stopping {
		<-p.done
		if _, ok := want[p.id]; !ok {
			s.emit(p.id, ServiceStatus{State: "stopped", Since: time.Now().UnixMilli()})
		}
	}
	for _, p := range starting {
		go s.run(p)
	}
}

// Close stops every service and waits for them (each gets its stdin closed and a few seconds to exit).
func (s *Services) Close() {
	s.mu.Lock()
	s.closed = true
	procs := s.procs
	s.procs = map[string]*svcProc{}
	s.mu.Unlock()
	for _, p := range procs {
		close(p.stop)
	}
	for _, p := range procs {
		<-p.done
	}
}

// Status of one add-on's service; nil when it has none.
func (s *Services) Status(id string) *ServiceStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.procs[id]; p != nil {
		st := p.st
		return &st
	}
	return nil
}

// Send writes one line (a JSON object, no newline) to a service's stdin.
func (s *Services) Send(id string, line []byte) error {
	s.mu.Lock()
	p := s.procs[id]
	var in chan []byte
	if p != nil {
		in = p.in
	}
	s.mu.Unlock()
	if p == nil {
		return fmt.Errorf("add-on %q has no service", id)
	}
	if in == nil {
		return fmt.Errorf("add-on %q: service is not running", id)
	}
	select {
	case in <- line:
		return nil
	default:
		return fmt.Errorf("add-on %q: service is not reading its stdin", id)
	}
}

// Broadcast sends a line to every running service. A service that is not keeping up loses it (logged).
func (s *Services) Broadcast(line []byte) {
	s.mu.Lock()
	var ins []chan []byte
	var ids []string
	for id, p := range s.procs {
		if p.in != nil {
			ins = append(ins, p.in)
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	for i, in := range ins {
		select {
		case in <- line:
		default:
			s.log.Warn("addon service not reading stdin, dropped a line", "addon", ids[i])
		}
	}
}

func (s *Services) setStatus(p *svcProc, f func(st *ServiceStatus)) {
	s.mu.Lock()
	f(&p.st)
	st := p.st
	s.mu.Unlock()
	s.emit(p.id, st)
}

func (s *Services) emit(id string, st ServiceStatus) {
	if s.OnStatus != nil {
		s.OnStatus(id, st)
	}
}

// run keeps one service alive until p.stop: restart after it exits, backing off 1s → 2s → … → 1min, back to 1s once
// a run lasted a minute (a service that crashes at once shouldn't spin; one that crashed after an hour should be back fast).
func (s *Services) run(p *svcProc) {
	defer close(p.done)
	backoff := time.Second
	for {
		started := time.Now()
		err := s.runOnce(p)
		select {
		case <-p.stop:
			return
		default:
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		msg := "exited"
		if err != nil {
			msg = err.Error()
		}
		s.log.Warn("addon service stopped, restarting", "addon", p.id, "err", msg, "in", backoff)
		s.setStatus(p, func(st *ServiceStatus) {
			st.State, st.PID, st.Error, st.Since = "exited", 0, msg, time.Now().UnixMilli()
			st.Restarts++
		})
		select {
		case <-p.stop:
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

func (s *Services) resolve(p *svcProc) (string, error) {
	c := p.spec.Command
	if home, _ := os.UserHomeDir(); home != "" && (c == "~" || strings.HasPrefix(c, "~/")) {
		c = filepath.Join(home, c[1:])
	}
	if strings.ContainsRune(c, '/') || strings.ContainsRune(c, filepath.Separator) {
		if !filepath.IsAbs(c) {
			c = filepath.Join(p.dir, c)
		}
		if _, err := os.Stat(c); err != nil {
			return "", fmt.Errorf("command %s not found", c)
		}
		return c, nil
	}
	path, err := exec.LookPath(c)
	if err != nil {
		return "", fmt.Errorf("command %q not found on PATH", c)
	}
	return path, nil
}

func (s *Services) runOnce(p *svcProc) error {
	bin, err := s.resolve(p)
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, p.spec.Args...)
	cmd.Dir = p.dir
	var env []string
	if s.Env != nil {
		env = s.Env()
	} else {
		env = os.Environ()
	}
	data := filepath.Join(s.dataDir, p.id)
	_ = os.MkdirAll(data, 0o700)
	env = append(env, "GERDR_ADDON="+p.id, "GERDR_ADDON_DIR="+p.dir, "GERDR_ADDON_DATA="+data)
	for k, v := range p.spec.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	setProcGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	tail := &tailWriter{}
	var logf io.WriteCloser = nopCloser{}
	if f, err := openRotated(filepath.Join(s.logDir, p.id+".log")); err == nil {
		logf = f
		fmt.Fprintf(f, "--- %s start %s %s\n", time.Now().Format(time.RFC3339), bin, strings.Join(p.spec.Args, " "))
	}
	defer logf.Close()
	cmd.Stderr = io.MultiWriter(logf, tail)
	if err := cmd.Start(); err != nil {
		return err
	}
	in := make(chan []byte, 256)
	s.mu.Lock()
	p.in = in
	s.mu.Unlock()
	s.setStatus(p, func(st *ServiceStatus) {
		st.State, st.PID, st.Error, st.Text, st.Since = "running", cmd.Process.Pid, "", "", time.Now().UnixMilli()
	})
	s.log.Info("addon service started", "addon", p.id, "cmd", bin, "pid", cmd.Process.Pid)

	// stdin: greeting first, then whatever arrives. Closing stdin is the "please exit" signal.
	stopWriting := make(chan struct{})
	go func() {
		defer stdin.Close()
		w := bufio.NewWriter(stdin)
		write := func(b []byte) bool {
			if _, err := w.Write(append(b, '\n')); err != nil {
				return false
			}
			return w.Flush() == nil
		}
		if s.Greeting != nil {
			for _, l := range s.Greeting(p.id) {
				if !write(l) {
					return
				}
			}
		}
		for {
			select {
			case <-stopWriting:
				return
			case b := <-in:
				if !write(b) {
					return
				}
			}
		}
	}()

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		s.readStdout(p, stdout)
	}()

	exited := make(chan error, 1)
	go func() {
		<-readDone // Wait closes stdout; read it to the end first
		exited <- cmd.Wait()
	}()

	var werr error
	select {
	case werr = <-exited:
	case <-p.stop:
		close(stopWriting) // EOF on stdin: the service closes its device and exits
		select {
		case werr = <-exited:
		case <-time.After(3 * time.Second):
			terminate(cmd)
			select {
			case werr = <-exited:
			case <-time.After(2 * time.Second):
				killGroup(cmd)
				werr = <-exited
			}
		}
	}
	s.mu.Lock()
	p.in = nil
	s.mu.Unlock()
	select {
	case <-stopWriting:
	default:
		close(stopWriting)
	}
	if werr == nil {
		return nil
	}
	if t := tail.String(); t != "" {
		return fmt.Errorf("%v\n%s", werr, t)
	}
	return werr
}

func (s *Services) readStdout(p *svcProc, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var m struct {
			Type string          `json:"type"`
			Text string          `json:"text"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(line, &m) != nil || m.Type == "" {
			s.log.Info("addon service", "addon", p.id, "stdout", string(line))
			continue
		}
		switch m.Type {
		case "status":
			s.setStatus(p, func(st *ServiceStatus) { st.Text = m.Text })
		case "message":
			if s.OnMessage != nil {
				data := m.Data
				if len(data) == 0 {
					data = json.RawMessage("null")
				}
				s.OnMessage(p.id, append(json.RawMessage(nil), data...))
			}
		default:
			s.log.Info("addon service: unknown message type", "addon", p.id, "type", m.Type)
		}
	}
	// Drain anything after an over-long line so the process doesn't block writing to a full pipe.
	_, _ = io.Copy(io.Discard, r)
}

// tailWriter keeps the last few lines of stderr: the reason shown when a service dies.
type tailWriter struct {
	mu  sync.Mutex
	buf []byte
}

const tailMax = 2000

func (t *tailWriter) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > tailMax {
		t.buf = t.buf[len(t.buf)-tailMax:]
	}
	return len(b), nil
}

func (t *tailWriter) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

type nopCloser struct{}

func (nopCloser) Write(b []byte) (int, error) { return len(b), nil }
func (nopCloser) Close() error                { return nil }

// openRotated appends to path; past 2MB the file moves to path.1 first (one old file kept).
func openRotated(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if st, err := os.Stat(path); err == nil && st.Size() > 2<<20 {
		_ = os.Rename(path, path+".1")
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}
