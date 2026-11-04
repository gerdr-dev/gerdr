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
//	  {"type":"status","text":"…","links":{"panel":"http://127.0.0.1:7858"}}
//	                                                    also the addresses of the add-on's "link" settings, by key
//	                                                    (http/https only): pages the service serves on a port only it
//	                                                    knows. A status line without "links" keeps the last ones;
//	                                                    a restart clears them.
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
	// Events: event patterns (docs/dev/EVENTS.md: "source/kind" with * globs) the service gets on its stdin as
	// {"type":"event","event":{…}}.
	Events []string `json:"events,omitempty"`
}

// ServiceStatus is what Settings → Add-ons and g.service.status() show.
type ServiceStatus struct {
	State    string            `json:"state"`           // starting / running / exited (waiting to restart) / stopped
	PID      int               `json:"pid,omitempty"`   // while running
	Text     string            `json:"text,omitempty"`  // the service's own one-liner ({"type":"status"})
	Links    map[string]string `json:"links,omitempty"` // "link" settings' addresses, by key ({"type":"status","links"})
	Error    string            `json:"error,omitempty"` // why it is not running: command not found, last exit + stderr tail
	Restarts int               `json:"restarts,omitempty"`
	Since    int64             `json:"since"` // unix ms of the last state change
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
	// OnEvent: the service emitted an event ({"type":"event","event":{…}}); its source defaults to the add-on's id.
	OnEvent func(id string, event json.RawMessage)

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
	st       ServiceStatus
	in       chan []byte // stdin of the current run; nil while not running
	settings string      // the settings line last sent to the current run (sendSettings)
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
	// Settings → Add-ons changed (the state file is watched too): running services get their new values
	s.mu.Lock()
	running := make([]*svcProc, 0, len(s.procs))
	for _, p := range s.procs {
		running = append(running, p)
	}
	s.mu.Unlock()
	for _, p := range running {
		s.sendSettings(p, false)
	}
}

// settingsLine: {"type":"settings","settings":{…}}, what a service reads its add-on's settings from (tokens too: a
// service can't call g.settings.get, it isn't a page).
func (s *Services) settingsLine(id string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "settings", "settings": s.m.SettingsOf(id)})
	return b
}

// sendSettings gives a running service its settings when they changed since the last time (always, with force).
func (s *Services) sendSettings(p *svcProc, force bool) {
	line := s.settingsLine(p.id)
	s.mu.Lock()
	in := p.in
	same := p.settings == string(line)
	if in != nil && (force || !same) {
		p.settings = string(line)
	}
	s.mu.Unlock()
	if in == nil || (same && !force) {
		return
	}
	select {
	case in <- line:
	default:
		s.log.Warn("addon service not reading stdin, dropped its settings", "addon", p.id)
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

// SendWhere sends a line to every running service whose addon.json service.events pick says yes to (an event, to
// the services subscribed to it). A service that is not keeping up loses it (logged).
func (s *Services) SendWhere(pick func(patterns []string) bool, line []byte) {
	s.mu.Lock()
	var ins []chan []byte
	var ids []string
	for id, p := range s.procs {
		if p.in != nil && len(p.spec.Events) > 0 && pick(p.spec.Events) {
			ins = append(ins, p.in)
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	for i, in := range ins {
		select {
		case in <- line:
		default:
			s.log.Warn("addon service not reading stdin, dropped an event", "addon", ids[i])
		}
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
		st.State, st.PID, st.Error, st.Text, st.Links, st.Since = "running", cmd.Process.Pid, "", "", nil, time.Now().UnixMilli()
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
		// Its settings right after the greeting (Sync sends them again when they change)
		line := s.settingsLine(p.id)
		s.mu.Lock()
		p.settings = string(line)
		s.mu.Unlock()
		if !write(line) {
			return
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
			Type  string            `json:"type"`
			Text  string            `json:"text"`
			Links map[string]string `json:"links"`
			Data  json.RawMessage   `json:"data"`
			Event json.RawMessage   `json:"event"`
		}
		if json.Unmarshal(line, &m) != nil || m.Type == "" {
			s.log.Info("addon service", "addon", p.id, "stdout", string(line))
			continue
		}
		switch m.Type {
		case "status":
			// Only http(s): the UI opens them from a link, and a javascript: URL there would run in gerdr's page
			var links map[string]string
			for k, u := range m.Links {
				if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
					if links == nil {
						links = map[string]string{}
					}
					links[k] = u
				}
			}
			s.setStatus(p, func(st *ServiceStatus) {
				st.Text = m.Text
				if m.Links != nil {
					st.Links = links
				}
			})
		case "message":
			if s.OnMessage != nil {
				data := m.Data
				if len(data) == 0 {
					data = json.RawMessage("null")
				}
				s.OnMessage(p.id, append(json.RawMessage(nil), data...))
			}
		case "event":
			if s.OnEvent != nil && len(m.Event) > 0 {
				s.OnEvent(p.id, append(json.RawMessage(nil), m.Event...))
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
