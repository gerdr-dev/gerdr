package peer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Reports across computers (docs/dev/MULTIHOST.md, phase 2). A host session reporting to a session on the same
// host is the host's own business (its Record.ReportTo, its reporter). One reporting to the console, or to another
// host, can't be stored there: the host can't reach anything but itself (one-way trust). So the console keeps those
// in reports.json and its own reporter watches host sessions (Subscribe) and delivers.

func (m *Manager) reportsFile() string { return filepath.Join(m.dir, "peer-reports.json") }

func (m *Manager) loadReports() {
	m.reports = map[string]string{}
	if b, err := os.ReadFile(m.reportsFile()); err == nil {
		_ = json.Unmarshal(b, &m.reports)
	}
}

// ReportTo: where host session id ("<host>~<id>") reports to, as kept on the console. "" = nothing here (it may
// still report to a session on its own host, which that host handles).
func (m *Manager) ReportTo(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reports[id]
}

// SetReportTo keeps (to != "") or drops a console-side report target for a host session.
func (m *Manager) SetReportTo(id, to string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if to == "" {
		if _, ok := m.reports[id]; !ok {
			return nil
		}
		delete(m.reports, id)
	} else {
		m.reports[id] = to
	}
	b, _ := json.MarshalIndent(m.reports, "", "  ")
	tmp := m.reportsFile() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.reportsFile())
}

// RewriteSummary: RewriteSummary plus the console-side report target, which is the one the console shows (a host
// session reporting to the console says so in `gerdr session list` here).
func (m *Manager) RewriteSummary(h Host, raw json.RawMessage) json.RawMessage {
	out := RewriteSummary(h, raw)
	if out == nil {
		return nil
	}
	if to := m.ReportTo(idOf(out)); to != "" {
		var x map[string]any
		if json.Unmarshal(out, &x) == nil {
			x["reportTo"] = to
			if b, err := json.Marshal(x); err == nil {
				return b
			}
		}
	}
	return out
}

// Known: whether id is a host session this console has seen, and where it reports to (for loop checks).
func (m *Manager) Known(id string) (next string, ok bool) {
	h, _ := Split(id)
	if h == "" {
		return "", false
	}
	m.mu.Lock()
	st := m.state[h]
	var hit json.RawMessage
	if st != nil {
		for _, s := range st.sessions {
			if idOf(s) == id {
				hit = s
				break
			}
		}
	}
	m.mu.Unlock()
	if hit == nil {
		return "", false
	}
	var x struct {
		ReportTo string `json:"reportTo"`
	}
	_ = json.Unmarshal(hit, &x)
	return x.ReportTo, true
}

// Status: a host session's status from the last list / stream ("", false = not known).
func (m *Manager) Status(id string) (string, bool) {
	h, _ := Split(id)
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.state[h]
	if st == nil {
		return "", false
	}
	for _, s := range st.sessions {
		if idOf(s) == id {
			var x struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(s, &x)
			return x.Status, true
		}
	}
	return "", false
}

// Get fetches one host session (summary, rewritten) and its events (rewritten) for a report.
func (m *Manager) Get(ctx context.Context, id string) (json.RawMessage, json.RawMessage, error) {
	hostID, rest := Split(id)
	h, ok := m.Host(hostID)
	if !ok {
		return nil, nil, ErrNoHost
	}
	fetch := func(path string) ([]byte, error) {
		resp, err := m.Do(ctx, hostID, "GET", path, nil, http.Header{})
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
		}
		return b, err
	}
	sum, err := fetch("/api/sessions/" + rest)
	if err != nil {
		return nil, nil, err
	}
	evs, err := fetch("/api/sessions/" + rest + "/events")
	if err != nil {
		return nil, nil, err
	}
	return m.RewriteSummary(h, sum), RewriteEvents(hostID, evs), nil
}

// Send posts a message to a host session (a report the console delivers). from: the session it is about.
func (m *Manager) Send(ctx context.Context, id, text, from string) error {
	hostID, rest := Split(id)
	body, _ := json.Marshal(map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "from": from})
	body = RewriteBody(hostID, body)
	resp, err := m.Do(ctx, hostID, "POST", "/api/sessions/"+rest+"/send", strings.NewReader(string(body)), http.Header{"Content-Type": {"application/json"}})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

// Subscribe keeps one upstream per paired host (following Add / Remove) until ctx ends, and hands every session
// summary that comes in ({"t":"session",…}, rewritten) to fn. For the console's reporter, which must see host
// sessions stop even when no window is open.
func (m *Manager) Subscribe(ctx context.Context, fn func(msg json.RawMessage)) {
	out := make(chan json.RawMessage, 256)
	var mu sync.Mutex
	running := map[string]context.CancelFunc{}
	sync1 := func() {
		mu.Lock()
		defer mu.Unlock()
		want := map[string]bool{}
		for _, h := range m.Hosts() {
			want[h.ID] = true
			if running[h.ID] == nil {
				c, cancel := context.WithCancel(ctx)
				running[h.ID] = cancel
				m.Connect(c, h.ID, out)
			}
		}
		for id, cancel := range running {
			if !want[id] {
				cancel()
				delete(running, id)
			}
		}
	}
	m.mu.Lock()
	m.onHosts = append(m.onHosts, sync1)
	m.mu.Unlock()
	sync1()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-out:
				fn(b)
			}
		}
	}()
}
