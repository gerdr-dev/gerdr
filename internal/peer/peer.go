// Package peer lets this Gerdr (the console) drive the sessions of other computers running Gerdr (hosts).
// Design and why: docs/dev/MULTIHOST.md.
//
// A host is reached on its remote port (relay or LAN direct) with a device cookie it issued for a peer code, exactly
// like a paired phone: nothing new is opened on the host. Its sessions show up here as "<hostID>~<id>".
package peer

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Sep joins a host id and a session id. Gerdr's ids are UUIDs and host ids are [a-z0-9-], so it can't occur in
// either, and it is URL-safe, so /api/sessions/{id}/… routes don't change.
const Sep = "~"

// Join: the id the console shows for session id on host.
func Join(host, id string) string {
	if host == "" || id == "" {
		return id
	}
	return host + Sep + id
}

// Split: "<host>~<id>" → host, id. A local id gives "", id.
func Split(id string) (host, rest string) {
	if i := strings.Index(id, Sep); i > 0 {
		return id[:i], id[i+len(Sep):]
	}
	return "", id
}

// Host is one paired computer, as stored in peers.json. Token is the device credential the host issued: whoever
// reads it can run commands on that host (docs/dev/MULTIHOST.md, "Trust and credentials").
type Host struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"` // https://<the host's remote name>
	Token   string `json:"token"`
	Cookie  string `json:"cookie"` // the host's device cookie name
	AddedAt int64  `json:"addedAt"`
}

// Info is what the UI gets: no token.
type Info struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	AddedAt int64  `json:"addedAt"`
	Online  bool   `json:"online"`
	Error   string `json:"error,omitempty"`
}

type Manager struct {
	dir  string // <GERDR_DIR>; peers.json lives here
	self string // this computer's name, sent when pairing
	http *http.Client
	// tr is shared by the HTTP client and the upstream websockets. Tests point its dialer and TLS roots at a local
	// server; LAN direct (not done) would dial the host's LAN address here, keeping its name for TLS.
	tr *http.Transport

	mu      sync.Mutex
	hosts   []Host
	state   map[string]*hostState
	reports map[string]string // host session → console-side report target (reports.go)
	onHosts []func()          // Subscribe's resync, run when a host is added or removed

	// OnChange: the list of hosts or one's online state changed (the server broadcasts "hosts").
	OnChange func()
}

type hostState struct {
	online   bool
	err      string
	sessions []json.RawMessage // last list, already rewritten
	at       time.Time         // when sessions was fetched
}

func Open(dir string) (*Manager, error) {
	name, _ := os.Hostname()
	name = strings.TrimSuffix(name, ".local")
	tr := http.DefaultTransport.(*http.Transport).Clone()
	m := &Manager{dir: dir, self: name, state: map[string]*hostState{}, tr: tr,
		// No overall timeout: a proxied video streams for as long as it plays. Each request carries its own context.
		http: &http.Client{Transport: tr}}
	b, err := os.ReadFile(m.file())
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &m.hosts); err != nil {
			return nil, fmt.Errorf("peers.json: %w", err)
		}
	}
	m.loadReports()
	return m, nil
}

func (m *Manager) file() string { return filepath.Join(m.dir, "peers.json") }

func (m *Manager) saveLocked() error {
	b, err := json.MarshalIndent(m.hosts, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.file() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.file())
}

func (m *Manager) Hosts() []Host {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Host(nil), m.hosts...)
}

func (m *Manager) Host(id string) (Host, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, h := range m.hosts {
		if h.ID == id {
			return h, true
		}
	}
	return Host{}, false
}

func (m *Manager) Infos() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.hosts))
	for _, h := range m.hosts {
		i := Info{ID: h.ID, Name: h.Name, URL: h.URL, AddedAt: h.AddedAt}
		if st := m.state[h.ID]; st != nil {
			i.Online, i.Error = st.online, st.err
		}
		out = append(out, i)
	}
	return out
}

func (m *Manager) changed() {
	if m.OnChange != nil {
		m.OnChange()
	}
}

// hostsChanged: one was added or removed (not just gone on / offline)
func (m *Manager) hostsChanged() {
	m.mu.Lock()
	fs := append([]func(){}, m.onHosts...)
	m.mu.Unlock()
	for _, f := range fs {
		f()
	}
	m.changed()
}

var codeInURL = regexp.MustCompile(`[?&]peer=([A-Za-z0-9-]+)`)

// ParseInvite: what the person pasted (the host's "https://name/?peer=CODE", or an address plus a separate code).
func ParseInvite(invite, code string) (base, c string, err error) {
	invite = strings.TrimSpace(invite)
	if m := codeInURL.FindStringSubmatch(invite); m != nil && code == "" {
		code = m[1]
	}
	if !strings.Contains(invite, "://") {
		invite = "https://" + invite
	}
	u, err := url.Parse(invite)
	if err != nil || u.Host == "" {
		return "", "", errors.New(i18n.S("地址不对", "That address doesn't look right"))
	}
	if u.Scheme != "https" {
		// The device token goes in a cookie on every request: never over plain http
		return "", "", errors.New(i18n.S("要 https 地址（那台电脑「远程访问」里的地址）", "Needs an https address (the one in that computer's Remote access)"))
	}
	if strings.TrimSpace(code) == "" {
		return "", "", errors.New(i18n.S("缺配对码：在那台电脑的「设置 → 远程访问」里点「让另一台 Gerdr 控制这台」", "Missing the pairing code: on that computer, Settings → Remote access → \"Let another Gerdr control this computer\""))
	}
	return "https://" + u.Host, strings.TrimSpace(code), nil
}

// Add pairs with a host: redeems a peer code at its remote port and stores the device token it gets back.
func (m *Manager) Add(ctx context.Context, invite, code, name string) (Info, error) {
	base, code, err := ParseInvite(invite, code)
	if err != nil {
		return Info{}, err
	}
	body, _ := json.Marshal(map[string]any{"code": code, "peer": true, "name": m.self})
	req, _ := http.NewRequestWithContext(ctx, "POST", base+"/api/remote/pair", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gerdr", "1")
	req.Header.Set("User-Agent", "Gerdr console")
	resp, err := m.http.Do(req)
	if err != nil {
		return Info{}, fmt.Errorf(i18n.S("连不上 %s：%v", "Can't reach %s: %v"), base, err)
	}
	defer resp.Body.Close()
	var out struct {
		Token  string `json:"token"`
		Cookie string `json:"cookie"`
		Error  string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if resp.StatusCode != 200 || out.Token == "" {
		if out.Error == "" {
			out.Error = resp.Status
		}
		return Info{}, fmt.Errorf(i18n.S("配对失败：%s", "Pairing failed: %s"), out.Error)
	}
	if out.Cookie == "" {
		out.Cookie = "gerdr_dev"
	}
	u, _ := url.Parse(base)
	if strings.TrimSpace(name) == "" {
		name = strings.SplitN(u.Hostname(), ".", 2)[0]
	}
	m.mu.Lock()
	// Pairing the same address again replaces the old entry (a new token; the old device stays in the host's list
	// until removed there)
	id := ""
	for i, h := range m.hosts {
		if h.URL == base {
			id = h.ID
			m.hosts = append(m.hosts[:i], m.hosts[i+1:]...)
			break
		}
	}
	if id == "" {
		id = m.newIDLocked(name)
	}
	h := Host{ID: id, Name: strings.TrimSpace(name), URL: base, Token: out.Token, Cookie: out.Cookie, AddedAt: time.Now().UnixMilli()}
	m.hosts = append(m.hosts, h)
	sort.SliceStable(m.hosts, func(i, j int) bool { return m.hosts[i].AddedAt < m.hosts[j].AddedAt })
	err = m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		return Info{}, err
	}
	m.refresh(ctx, h)
	m.hostsChanged()
	for _, i := range m.Infos() {
		if i.ID == id {
			return i, nil
		}
	}
	return Info{ID: id, Name: h.Name, URL: base}, nil
}

var notID = regexp.MustCompile(`[^a-z0-9-]+`)

func (m *Manager) newIDLocked(name string) string {
	base := strings.Trim(notID.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if base == "" {
		base = "host"
	}
	if len(base) > 24 {
		base = base[:24]
	}
	taken := func(id string) bool {
		for _, h := range m.hosts {
			if h.ID == id {
				return true
			}
		}
		return false
	}
	id := base
	for i := 2; taken(id); i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	return id
}

// Remove forgets a host here. The device stays in the host's own list until it is removed there.
func (m *Manager) Remove(id string) bool {
	m.mu.Lock()
	ok := false
	for i, h := range m.hosts {
		if h.ID == id {
			m.hosts = append(m.hosts[:i], m.hosts[i+1:]...)
			ok = true
			break
		}
	}
	delete(m.state, id)
	if ok {
		_ = m.saveLocked()
	}
	m.mu.Unlock()
	if ok {
		m.hostsChanged()
	}
	return ok
}

func (m *Manager) Rename(id, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	m.mu.Lock()
	ok := false
	for i := range m.hosts {
		if m.hosts[i].ID == id {
			m.hosts[i].Name, ok = name, true
		}
	}
	if ok {
		_ = m.saveLocked()
	}
	m.mu.Unlock()
	if ok {
		m.changed()
	}
	return ok
}

// ErrNoHost: an id with a prefix of a host that isn't paired (any more).
var ErrNoHost = errors.New(i18n.S("没有这台电脑（可能已经移除了）", "No such computer (it may have been removed)"))

// Request builds a request to the host, credential and CSRF header included.
func (h Host) Request(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, h.URL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", h.Cookie+"="+h.Token)
	req.Header.Set("X-Gerdr", "1")
	req.Header.Set("User-Agent", "Gerdr console")
	return req, nil
}

// Do sends a request to a host and returns the response (the caller closes the body).
func (m *Manager) Do(ctx context.Context, hostID, method, path string, body io.Reader, hdr http.Header) (*http.Response, error) {
	h, ok := m.Host(hostID)
	if !ok {
		return nil, ErrNoHost
	}
	req, err := h.Request(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	for _, k := range []string{"Content-Type", "Range", "If-None-Match", "If-Modified-Since", "Accept"} {
		if v := hdr.Get(k); v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := m.http.Do(req)
	if err != nil {
		m.setState(hostID, func(st *hostState) { st.online, st.err = false, err.Error() })
		return nil, err
	}
	return resp, nil
}

func (m *Manager) setState(id string, f func(*hostState)) {
	m.mu.Lock()
	st := m.state[id]
	if st == nil {
		st = &hostState{}
		m.state[id] = st
	}
	was, wasErr := st.online, st.err
	f(st)
	now, nowErr := st.online, st.err
	m.mu.Unlock()
	if was != now || wasErr != nowErr {
		m.changed()
	}
}

// refresh fetches a host's session list and caches it rewritten. local=1: only that computer's own sessions, not
// the ones of hosts it drives itself (the star has one centre; chaining consoles isn't a thing).
func (m *Manager) refresh(ctx context.Context, h Host) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := h.Request(ctx, "GET", "/api/sessions?local=1", nil)
	resp, err := m.http.Do(req)
	if err != nil {
		m.setState(h.ID, func(st *hostState) { st.online, st.err = false, err.Error() })
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		msg := resp.Status
		if resp.StatusCode == 401 {
			msg = i18n.S("那台电脑不认这份凭据了（被移除了？）：重新配对", "That computer no longer accepts this credential (removed there?): pair again")
		}
		m.setState(h.ID, func(st *hostState) { st.online, st.err = false, msg })
		return
	}
	var sessions []json.RawMessage
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&sessions); err != nil {
		m.setState(h.ID, func(st *hostState) { st.online, st.err = false, err.Error() })
		return
	}
	list := make([]json.RawMessage, 0, len(sessions))
	for _, s := range sessions {
		if r := m.RewriteSummary(h, s); r != nil {
			list = append(list, r)
		}
	}
	m.setState(h.ID, func(st *hostState) {
		st.online, st.err = true, ""
		st.sessions, st.at = list, time.Now()
	})
}

// Sessions: every host's sessions, rewritten. A list younger than maxAge is used as is; otherwise the hosts are
// asked (in parallel, a few seconds at most) and an unreachable one gives its last known list.
func (m *Manager) Sessions(ctx context.Context, maxAge time.Duration) []json.RawMessage {
	hosts := m.Hosts()
	var wg sync.WaitGroup
	for _, h := range hosts {
		m.mu.Lock()
		st := m.state[h.ID]
		fresh := st != nil && st.online && time.Since(st.at) < maxAge
		m.mu.Unlock()
		if fresh {
			continue
		}
		wg.Add(1)
		go func(h Host) { defer wg.Done(); m.refresh(ctx, h) }(h)
	}
	wg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []json.RawMessage
	for _, h := range hosts {
		if st := m.state[h.ID]; st != nil {
			out = append(out, st.sessions...)
		}
	}
	return out
}

// NoteSession: a summary the console got outside the stream (a session it just started there), into the cached list.
func (m *Manager) NoteSession(hostID string, sum json.RawMessage) { m.noteSession(hostID, sum, "") }

// noteSession keeps the cached list current from the upstream ws (so the next hello needn't ask).
func (m *Manager) noteSession(hostID string, sum json.RawMessage, removed string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.state[hostID]
	if st == nil {
		return
	}
	id := removed
	if sum != nil {
		var x struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(sum, &x)
		id = x.ID
	}
	for i, s := range st.sessions {
		var x struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(s, &x)
		if x.ID == id {
			if sum != nil {
				st.sessions[i] = sum
			} else {
				st.sessions = append(st.sessions[:i], st.sessions[i+1:]...)
			}
			return
		}
	}
	if sum != nil {
		st.sessions = append(st.sessions, sum)
	}
}

// Route changes how hosts are dialled: tests point it at a local TLS server; LAN direct (not done yet) would dial
// the host's LAN address while TLS still checks the host's name.
func (m *Manager) Route(dial func(ctx context.Context, network, addr string) (net.Conn, error), tlsConfig *tls.Config) {
	m.tr.DialContext = dial
	m.tr.TLSClientConfig = tlsConfig
}

// Self: this computer's name, as hosts see it (the device label on pairing, a moved conversation's notice).
func (m *Manager) Self() string { return m.self }
