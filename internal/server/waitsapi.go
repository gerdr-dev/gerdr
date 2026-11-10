package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/internal/addons"
	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/waits"
	"github.com/gerdr-dev/gerdr/internal/work"
)

// Waiting for a state (docs/dev/EVENTS.md, "Waiting for a state"). Three parts, none knowing the others' insides:
//   - a waiter — a session (gerdr wait) or a panel (a trigger with until) — says which thing and the condition;
//   - a source — an add-on whose addon.json has "states" — answers "what is this thing like now?" ({"type":"get"}
//     → {"type":"state"} / {"type":"error"}), nothing more: no list to keep, no timer, no memory;
//   - Gerdr asks about each thing waited on (at once for a new one, then every poll interval), keeps the latest
//     state, and checks every wait on it: one already true wakes at once, so waiting late misses nothing.

// signal: a channel closed and replaced on every Fire, so any number of waiters wake at once.
type signal struct {
	mu sync.Mutex
	ch chan struct{}
}

func (g *signal) C() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ch == nil {
		g.ch = make(chan struct{})
	}
	return g.ch
}

func (g *signal) Fire() {
	g.mu.Lock()
	if g.ch != nil {
		close(g.ch)
	}
	g.ch = make(chan struct{})
	g.mu.Unlock()
}

// pollKey is one thing waited on, as the poller keeps it.
type pollKey struct {
	next     time.Time // when to ask again
	inflight bool
	addon    string    // who answered last
	at       int64     // unix ms of the last answer (state or error)
	err      string    // the last answer's error ("" = it reported a state)
	retry    bool      // that error goes away by itself (the network), as the source says
	idle     time.Time // nobody waits on it since then (kept a while: waiting again right after shows the last answer)
}

// poller asks the sources about every thing waited on.
type poller struct {
	mu   sync.Mutex
	keys map[[2]string]*pollKey
	kick chan struct{}
	sem  chan struct{} // at most 4 asks at a time
}

// wireWaits connects the waits, the sources and the panels, and runs the poller until ctx ends.
func (s *Server) wireWaits(ctx context.Context) {
	if s.Waits == nil || s.AddonSvc == nil {
		return
	}
	s.poll = &poller{keys: map[[2]string]*pollKey{}, kick: make(chan struct{}, 1), sem: make(chan struct{}, 4)}
	if s.Work != nil {
		s.Work.States = func(source, key string) (work.StateNow, bool) {
			st, ok := s.Waits.StateOf(source, key)
			return work.StateNow{State: st.State, Detail: st.Detail, URL: st.URL, At: st.At}, ok
		}
	}
	s.AddonSvc.OnState = s.stateFromAddon
	s.Waits.OnChange(s.waitsChanged)
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		forget := time.NewTicker(time.Minute)
		defer forget.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = s.Waits.Flush()
				return
			case <-t.C:
			case <-s.poll.kick:
			case <-forget.C:
				s.forgetStates()
			}
			s.pollDue()
		}
	}()
}

// waitsChanged: a wait was added or removed, or a panel's trigger files changed: a new thing is asked about now,
// and a panel's new wait on something already known is checked now.
func (s *Server) waitsChanged() {
	if s.poll == nil {
		return
	}
	select {
	case s.poll.kick <- struct{}{}:
	default:
	}
	go s.checkPanelWaits()
}

// pollInterval: how often a source wants to be asked (addon.json states[].poll, seconds).
func (s *Server) pollInterval(source string) time.Duration {
	if s.Addons != nil {
		for _, d := range s.Addons.States() {
			if d.Source == source && d.Poll > 0 {
				return time.Duration(max(d.Poll, 10)) * time.Second
			}
		}
	}
	return time.Minute
}

// pollDue asks about every thing waited on whose time has come; forgets the ones nobody waits on any more.
func (s *Server) pollDue() {
	wanted := map[[2]string]bool{}
	for _, k := range s.waitedOn() {
		wanted[k] = true
	}
	now := time.Now()
	p := s.poll
	p.mu.Lock()
	for k, pk := range p.keys {
		switch {
		case wanted[k]:
		case pk.idle.IsZero():
			pk.idle = now
		case now.Sub(pk.idle) > 10*time.Minute:
			delete(p.keys, k)
		}
	}
	var due [][2]string
	for k := range wanted {
		pk := p.keys[k]
		if pk == nil {
			pk = &pollKey{}
			p.keys[k] = pk
		}
		if !pk.idle.IsZero() {
			// Waited on again: asked about at once, not at the next interval
			pk.idle, pk.next = time.Time{}, time.Time{}
		}
		if !pk.inflight && !now.Before(pk.next) {
			pk.inflight = true
			due = append(due, k)
		}
	}
	p.mu.Unlock()
	for _, k := range due {
		go s.pollOne(k[0], k[1])
	}
}

// pollOne asks a source about one thing and handles the answer.
func (s *Server) pollOne(source, key string) {
	p := s.poll
	p.sem <- struct{}{}
	defer func() { <-p.sem }()
	var addon, errMsg string
	retry := true
	next := s.pollInterval(source)
	var rep *addons.Reporter
	for _, r := range s.AddonSvc.Reporters(source) {
		if r.Running {
			rep = &r
			break
		}
	}
	starting := false
	for _, r := range s.AddonSvc.Reporters(source) {
		starting = starting || r.State == "starting"
	}
	if rep == nil && starting {
		// Its service is coming up (Gerdr just started): asked again in a second, still "waiting"
		p.mu.Lock()
		if pk := p.keys[[2]string{source, key}]; pk != nil {
			pk.inflight, pk.next = false, time.Now().Add(time.Second)
		}
		p.mu.Unlock()
		return
	}
	if rep == nil {
		errMsg = fmt.Sprintf(i18n.S("报告 %s 的插件没在运行", "the add-on reporting %s isn't running"), source)
		next = 20 * time.Second
	} else {
		addon = rep.ID
		raw, err := s.AddonSvc.Get(rep.ID, source, key, 90*time.Second)
		var m struct {
			Type   string         `json:"type"`
			State  map[string]any `json:"state"`
			Detail string         `json:"detail"`
			URL    string         `json:"url"`
			Reason string         `json:"reason"`
			Retry  bool           `json:"retry"`
		}
		if err == nil {
			err = json.Unmarshal(raw, &m)
		}
		switch {
		case err != nil:
			errMsg, next = err.Error(), 20*time.Second
		case m.Type == "error":
			errMsg, retry = m.Reason, m.Retry
			if errMsg == "" {
				errMsg = i18n.S("插件说查不了，没说为什么", "the add-on couldn't, and didn't say why")
			}
			if retry {
				next = 20 * time.Second
			}
		default:
			if e := s.putState(addon, source, key, m.State, m.Detail, m.URL); e != nil {
				errMsg, retry = e.Error(), false
			}
		}
	}
	p.mu.Lock()
	if pk := p.keys[[2]string{source, key}]; pk != nil {
		pk.inflight, pk.addon, pk.at, pk.err, pk.retry, pk.next = false, addon, time.Now().UnixMilli(), errMsg, retry, time.Now().Add(next)
	}
	p.mu.Unlock()
	s.stateSig.Fire()
}

// polled: what the poller last heard about a thing.
func (s *Server) polled(source, key string) (pollKey, bool) {
	if s.poll == nil {
		return pollKey{}, false
	}
	s.poll.mu.Lock()
	defer s.poll.mu.Unlock()
	pk, ok := s.poll.keys[[2]string{source, key}]
	if !ok {
		return pollKey{}, false
	}
	return *pk, true
}

// waitedOn: every (source, key) a session or a panel waits on.
func (s *Server) waitedOn() [][2]string {
	var out [][2]string
	for _, w := range s.Waits.List("") {
		out = append(out, [2]string{w.Source, w.Key})
	}
	if s.Work != nil {
		for _, t := range s.Work.StateWaits() {
			out = append(out, [2]string{t.Source, t.Key})
		}
	}
	return out
}

// forgetStates drops the states of things nobody waits on any more.
func (s *Server) forgetStates() {
	wanted := map[[2]string]bool{}
	for _, x := range s.waitedOn() {
		wanted[x] = true
	}
	s.Waits.Forget(func(source, key string) bool { return wanted[[2]string{source, key}] })
}

// stateFromAddon: a service reported a state by itself (no get asked for it: a source that can be told of
// changes). Its source must be one its addon.json declares (default: the add-on's id).
func (s *Server) stateFromAddon(id string, line json.RawMessage) {
	var m struct {
		Source string         `json:"source"`
		Key    string         `json:"key"`
		State  map[string]any `json:"state"`
		Detail string         `json:"detail"`
		URL    string         `json:"url"`
	}
	if err := json.Unmarshal(line, &m); err != nil {
		s.Log.Warn("addon service: bad state", "addon", id, "err", err)
		return
	}
	if m.Source == "" {
		m.Source = id
	}
	if err := s.putState(id, m.Source, m.Key, m.State, m.Detail, m.URL); err != nil {
		s.Log.Warn("addon service: state refused", "addon", id, "err", err)
	}
}

// putState keeps what an add-on says one thing is like, then checks the waits on it. One add-on can't speak for
// another's things: the source must be one of those its addon.json declares.
func (s *Server) putState(addon, source, key string, state map[string]any, detail, url string) error {
	ok := false
	for _, r := range s.AddonSvc.Reporters(source) {
		if r.ID == addon {
			ok = true
		}
	}
	if !ok {
		return fmt.Errorf("add-on %q doesn't declare the state of %q (addon.json states)", addon, source)
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = ""
	}
	if err := s.Waits.SetState(waits.State{Source: source, Key: key, State: state, Detail: detail, URL: url}); err != nil {
		return err
	}
	s.stateSig.Fire()
	s.stateChanged(source, key)
	return nil
}

// stateChanged checks every wait on this thing against its latest state: sessions' due ones are woken (and gone),
// panels' triggers run.
func (s *Server) stateChanged(source, key string) {
	due, st, errs := s.Waits.Due(source, key)
	// A condition that can't be evaluated on the state (a misspelt field) would never wake: the wait is dropped and
	// its session told why — unless its own request is still open, which answers with the error instead (POST).
	if len(errs) > 0 {
		for _, w := range s.Waits.List("") {
			err, ok := errs[w.ID]
			if !ok {
				continue
			}
			// Removed and recorded under one lock: an open request settling (posting) sees both or neither
			s.brokenMu.Lock()
			_, _ = s.Waits.Remove(w.ID)
			open := s.posting_[w.ID]
			if open {
				s.broken[w.ID] = err
			}
			s.brokenMu.Unlock()
			if !open {
				go s.tellBroken(w, err)
			}
		}
	}
	for _, w := range due {
		if s.wakeFn != nil {
			s.wakeFn(w, st)
			continue
		}
		go s.wake(w, st)
	}
	if s.Work != nil {
		if _, ok := s.Waits.StateOf(source, key); ok {
			s.Work.DispatchState(source, key, work.StateNow{State: st.State, Detail: st.Detail, URL: st.URL, At: st.At})
		}
	}
}

// posting marks a wait whose request is still open (true) or settles it (false): returns why it was dropped for a
// condition that can't be evaluated meanwhile, if it was. After settling, stateChanged tells the session instead.
func (s *Server) posting(id string, open bool) error {
	s.brokenMu.Lock()
	defer s.brokenMu.Unlock()
	if s.broken == nil {
		s.broken, s.posting_ = map[string]error{}, map[string]bool{}
	}
	if open {
		s.posting_[id] = true
		return nil
	}
	err := s.broken[id]
	delete(s.broken, id)
	delete(s.posting_, id)
	return err
}

// tellBroken tells a session its wait was dropped, and why.
func (s *Server) tellBroken(w waits.Wait, err error) {
	if s.wakeFn != nil {
		s.wakeFn(w, waits.State{Detail: "broken: " + err.Error()})
		return
	}
	sess, e := s.Hub.Session(w.Session)
	if e != nil {
		return
	}
	text := fmt.Sprintf(i18n.S("[等待取消了 %s %s] 条件在它的状态上算不出来，等不到：%s\n\n条件：%s\n\n改好条件再等一次：gerdr wait sources 看有哪些字段，gerdr wait state %s '%s' 看现在的值。",
		"[Wait dropped %s %s] the condition can't be evaluated on its state, so it would never wake: %s\n\nCondition: %s\n\nFix it and wait again: gerdr wait sources lists the fields, gerdr wait state %s '%s' shows the values now."),
		w.Source, w.Key, s.condHelp(w.Source, err), w.Until, w.Source, w.Key)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, _ = sess.SendFrom(ctx, []acp.ContentBlock{acp.TextBlock(text)}, "", "", false)
}

// checkPanelWaits runs the panels' waits whose thing has a known state already (a trigger just written on
// something already true runs now, not at the next poll).
func (s *Server) checkPanelWaits() {
	if s.Work == nil {
		return
	}
	done := map[[2]string]bool{}
	for _, t := range s.Work.StateWaits() {
		k := [2]string{t.Source, t.Key}
		if done[k] {
			continue
		}
		done[k] = true
		if st, ok := s.Waits.StateOf(t.Source, t.Key); ok {
			s.Work.DispatchState(t.Source, t.Key, work.StateNow{State: st.State, Detail: st.Detail, URL: st.URL, At: st.At})
		}
	}
}

// wake sends a session the state its wait was for. A session that's gone takes its waits with it.
func (s *Server) wake(w waits.Wait, st waits.State) {
	sess, err := s.Hub.Session(w.Session)
	if err != nil {
		s.Waits.DropSession(w.Session)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := sess.SendFrom(ctx, []acp.ContentBlock{acp.TextBlock(wakeText(w, st))}, "", "", false); err != nil {
		s.Log.Warn("wait: send failed", "session", w.Session, "wait", w.ID, "err", err)
	}
}

// fieldsLine: a state's fields as one line, sorted (what a next condition starts from).
func fieldsLine(st map[string]any) string {
	keys := make([]string, 0, len(st))
	for k := range st {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := st[k]
		switch x := v.(type) {
		case float64:
			v = strconv.FormatFloat(x, 'f', -1, 64)
		case nil:
			v = "null"
		case []any:
			// A list (an MR's notes) is for conditions to filter, not to read here: its length
			v = fmt.Sprintf(i18n.S("[%d 项]", "[%d items]"), len(x))
		case map[string]any:
			v = "{…}"
		}
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return strings.Join(parts, " ")
}

// wakeText: what a woken session reads — the condition that holds, the state now, the source's detail, why it
// waited, and how to wait for what comes next.
func wakeText(w waits.Wait, st waits.State) string {
	var b strings.Builder
	fmt.Fprintf(&b, i18n.S("[等到了 %s %s] 条件成立：%s", "[Wait over %s %s] the condition holds: %s"), w.Source, w.Key, w.Until)
	fmt.Fprintf(&b, "\n\n"+i18n.S("现在：%s", "Now: %s"), fieldsLine(st.State))
	if st.Detail != "" {
		b.WriteString("\n\n" + st.Detail)
	}
	if st.URL != "" {
		b.WriteString("\n\n" + st.URL)
	}
	if w.Note != "" {
		fmt.Fprintf(&b, "\n\n"+i18n.S("（你等它是为了：%s）", "(You waited for this to: %s)"), w.Note)
	}
	fmt.Fprintf(&b, "\n\n"+i18n.S("（这次等待结束了。还要接着等，就照上面现在的值写下一个条件再等一次：gerdr wait %s '%s' --until '…'）",
		"(This wait is over. To wait for what comes next, write the next condition from the values above and wait again: gerdr wait %s '%s' --until '…')"), w.Source, w.Key)
	return b.String()
}

// SourceState is how asking about a key goes.
type SourceState struct {
	Addon string `json:"addon,omitempty"`
	// State: serving (it reported the state) / waiting (not asked yet, or no answer yet) / retrying (a passing
	// error: the network) / refused (it can't: Reason says why; asked again every minute) / stopped (no add-on
	// reporting the source is running)
	State   string `json:"state"`
	Reason  string `json:"reason,omitempty"`
	Checked int64  `json:"checked,omitempty"` // unix ms of the last answer
}

func (s *Server) sourceState(source, key string) SourceState {
	running := false
	for _, r := range s.AddonSvc.Reporters(source) {
		if r.Running || r.State == "starting" {
			running = true
		}
	}
	pk, ok := s.polled(source, key)
	switch {
	case !running:
		return SourceState{State: "stopped", Reason: fmt.Sprintf(i18n.S("报告 %s 的插件没在运行", "the add-on reporting %s isn't running"), source)}
	case !ok || pk.at == 0:
		return SourceState{State: "waiting"}
	case pk.err == "":
		return SourceState{Addon: pk.addon, State: "serving", Checked: pk.at}
	case pk.retry:
		return SourceState{Addon: pk.addon, State: "retrying", Reason: pk.err, Checked: pk.at}
	default:
		return SourceState{Addon: pk.addon, State: "refused", Reason: pk.err, Checked: pk.at}
	}
}

// waitStatus: how a wait on (source, key, until) stands — what the sources say, the state now, whether the
// condition holds (or why it can't be evaluated), and a warning when it can't ever wake.
type waitStatus struct {
	Reporter SourceState    `json:"reporter"`      // how asking the source about it goes
	Now      map[string]any `json:"now,omitempty"` // the state's fields, as last reported
	At       int64          `json:"at,omitempty"`  // when
	Holds    *bool          `json:"holds,omitempty"`
	Error    string         `json:"error,omitempty"` // the condition can't be evaluated on that state
	Warning  string         `json:"warning,omitempty"`
}

func (s *Server) statusOf(source, key, until string) waitStatus {
	var v waitStatus
	v.Reporter = s.sourceState(source, key)
	if st, ok := s.Waits.StateOf(source, key); ok {
		v.Now, v.At = st.State, st.At
		if c, err := waits.Parse(until); err == nil {
			h, err := c.Eval(st.State)
			if err != nil {
				v.Error = err.Error()
			} else {
				v.Holds = &h
			}
		}
	}
	switch {
	case len(s.AddonSvc.Reporters(source)) == 0:
		v.Warning = fmt.Sprintf(i18n.S("没有开着的插件报告 %s 的状态：等不到", "No switched-on add-on reports %s: this can't wake"), source)
	case v.Reporter.State == "refused":
		v.Warning = i18n.S("插件查不了它（原因见 reporter.reason）：改好之前等不到", "The add-on can't look at it (see reporter.reason): this can't wake until that's fixed")
	case v.Error != "":
		v.Warning = i18n.S("条件在现在的状态上算不出来（见 error）：改好条件再等", "The condition can't be evaluated on the state (see error): fix it and wait again")
	}
	return v
}

type waitView struct {
	waits.Wait
	waitStatus
}

// triggerView: a panel's trigger; one waiting for a state comes with its status.
type triggerView struct {
	work.TriggerInfo
	*waitStatus
}

func (s *Server) viewTriggers(list []work.TriggerInfo) []triggerView {
	out := make([]triggerView, 0, len(list))
	for _, t := range list {
		v := triggerView{TriggerInfo: t}
		if t.Until != "" && !t.Off && s.Waits != nil && s.AddonSvc != nil {
			st := s.statusOf(t.Source, t.Key, t.Until)
			v.waitStatus = &st
		}
		out = append(out, v)
	}
	return out
}

// condHelp: why a condition can't be evaluated, with the fields the source declares.
func (s *Server) condHelp(source string, err error) string {
	msg := err.Error()
	if s.Addons == nil {
		return msg
	}
	for _, d := range s.Addons.States() {
		if d.Source == source && len(d.Fields) > 0 {
			names := make([]string, 0, len(d.Fields))
			for k := range d.Fields {
				names = append(names, k)
			}
			sort.Strings(names)
			return msg + fmt.Sprintf(i18n.S("（%s 的字段：%s；gerdr wait sources 看每个是什么意思）", " (%s's fields: %s; gerdr wait sources says what each means)"), source, strings.Join(names, ", "))
		}
	}
	return msg
}

func containsStr(l []string, x string) bool {
	for _, y := range l {
		if y == x {
			return true
		}
	}
	return false
}

func (s *Server) waitRoutes(mux *http.ServeMux) {
	need := func(w http.ResponseWriter) bool {
		if s.Waits == nil || s.AddonSvc == nil {
			httpError(w, 503, "waits are off")
			return false
		}
		return true
	}
	// ?session= → the waits with how each stands
	mux.HandleFunc("GET /api/waits", func(w http.ResponseWriter, r *http.Request) {
		if !need(w) {
			return
		}
		list := s.Waits.List(r.URL.Query().Get("session"))
		out := make([]waitView, 0, len(list))
		for _, x := range list {
			out = append(out, waitView{Wait: x, waitStatus: s.statusOf(x.Source, x.Key, x.Until)})
		}
		writeJSON(w, map[string]any{"list": out})
	})
	// { source, key, until, session, note? } → the wait and how it stands; "woken": it held already, the session
	// has the message. Refused when no add-on reports the source, or the condition reads fields it doesn't have.
	mux.HandleFunc("POST /api/waits", func(w http.ResponseWriter, r *http.Request) {
		if !need(w) {
			return
		}
		var x waits.Wait
		if err := decode(r, &x); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if _, err := s.Hub.Session(x.Session); err != nil {
			httpError(w, 404, err.Error())
			return
		}
		c, err := waits.Parse(x.Until)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		// Known already: a condition that can't be evaluated on it (a misspelt field) is refused now
		if st, ok := s.Waits.StateOf(x.Source, x.Key); ok {
			if _, err := c.Eval(st.State); err != nil {
				httpError(w, 400, s.condHelp(x.Source, err))
				return
			}
		}
		if len(s.AddonSvc.Reporters(x.Source)) == 0 {
			httpError(w, 400, fmt.Sprintf(i18n.S("没有开着的插件报告 %s 的状态（gerdr wait sources 看有哪些；装插件用 gerdr addon）", "No switched-on add-on reports %s (gerdr wait sources lists what can be waited on; gerdr addon installs one)"), x.Source))
			return
		}
		// Marked before it exists, so a broken condition found meanwhile is this request's to answer
		out, err := s.Waits.AddWith(x, func(id string) { s.posting(id, true) })
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		gone := func() bool {
			for _, y := range s.Waits.List(out.Session) {
				if y.ID == out.ID {
					return false
				}
			}
			return true
		}
		// Known already: checked now. Otherwise it is asked about at once (OnChange → the poller); give that a few
		// seconds, so the caller knows where it stands
		s.stateChanged(out.Source, out.Key)
		deadline := time.After(10 * time.Second)
	wait:
		for !gone() {
			c := s.stateSig.C()
			if _, ok := s.Waits.StateOf(out.Source, out.Key); ok {
				break
			}
			if st := s.sourceState(out.Source, out.Key); st.State == "refused" || st.State == "stopped" {
				break
			}
			select {
			case <-c:
			case <-deadline:
				break wait
			case <-r.Context().Done():
				break wait
			}
		}
		// The state may have come in between: check once more (a wait already woken is simply not there any more)
		if !gone() {
			s.stateChanged(out.Source, out.Key)
		}
		// The first state is in and the condition can't be evaluated on it: refused, not left waiting forever
		if st, ok := s.Waits.StateOf(out.Source, out.Key); ok && !gone() {
			if _, err := c.Eval(st.State); err != nil {
				_, _ = s.Waits.Remove(out.ID)
				s.posting(out.ID, false)
				httpError(w, 400, s.condHelp(out.Source, err))
				return
			}
		}
		woken := gone()
		if err := s.posting(out.ID, false); err != nil {
			httpError(w, 400, s.condHelp(out.Source, err))
			return
		}
		writeJSON(w, map[string]any{"wait": out, "woken": woken, "status": s.statusOf(out.Source, out.Key, out.Until)})
	})
	mux.HandleFunc("DELETE /api/waits/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !need(w) {
			return
		}
		ok, err := s.Waits.Remove(r.PathValue("id"))
		if err != nil {
			httpError(w, 500, err.Error())
			return
		}
		if !ok {
			httpError(w, 404, i18n.S("没有这个等待", "no such wait"))
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	// ?source=&key= → the latest state of one thing (404 when none was reported)
	mux.HandleFunc("GET /api/states", func(w http.ResponseWriter, r *http.Request) {
		if !need(w) {
			return
		}
		st, ok := s.Waits.StateOf(r.URL.Query().Get("source"), r.URL.Query().Get("key"))
		if !ok {
			httpError(w, 404, i18n.S("还没有它的状态：没人等它时插件不查（等它就会查）", "no state of it yet: add-ons only look at what is waited on (waiting on it makes them look)"))
			return
		}
		writeJSON(w, st)
	})
	// What can be waited on: the switched-on add-ons' declared states (fields and what they mean, how a key looks)
	mux.HandleFunc("GET /api/waits/sources", func(w http.ResponseWriter, r *http.Request) {
		if !need(w) {
			return
		}
		list := []addons.StateSource{}
		if s.Addons != nil {
			list = s.Addons.States()
		}
		writeJSON(w, map[string]any{"list": list})
	})
}
