package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	ev "github.com/gerdr-dev/gerdr/internal/events"
	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/waits"
)

// Triggers: what a project's Work (a panel) does when an event comes (docs/dev/EVENTS.md). The schedules' twin —
// schedules/ runs on time, triggers/ on events — declared the same way, one file per trigger, the file name its id:
//
//	triggers/mr.json      { "on": "gitlab/mr.*", "fn": "mrwatch.onEvent", "name": "MR news to the worker" }
//	triggers/alerts.json  { "on": "grafana/alert.firing", "key": "prod/*", "prompt": "Look into it and tell me" }
//
// on is an event pattern (source/kind with * globs, or a kind alone); key narrows it to what the event is about.
// fn runs a local function with { event, trigger } (plus input); prompt hands the event and the text to the Work
// agent. Exactly one of the two. A trigger's runs go one after another, in the order the events came.
//
// Or it waits for a state (docs/dev/EVENTS.md, "Waiting for a state") instead of events:
//
//	user/triggers/mr-34.json  { "source": "gitlab", "key": "gitlab.example.com/g/r!34",
//	                            "until": "state != 'opened' || reviews > 2", "fn": "waits.onState" }
//
// fn runs once with { state, detail, url, source, key, until, trigger } when the condition holds for the source's
// latest state — at once if it already does. Then it is spent: it runs again only after the condition stopped
// holding, or the file changed (the usual next step: the function trashes it, or rewrites it with a new until).

// TriggersDir is the Work directory's folder of trigger files.
const TriggersDir = "triggers"

// EventBus is the event store as ctx.events and the triggers see it (events.Store).
type EventBus interface {
	Emit(e ev.Event) (ev.Event, bool, error)
	List(q ev.Query) ([]ev.Event, error)
	Catalog() []ev.Source
}

// Trigger is one triggers/<id>.json.
type Trigger struct {
	ID     string `json:"id"`
	On     string `json:"on"`
	Key    string `json:"key,omitempty"`
	Fn     string `json:"fn,omitempty"`
	Prompt string `json:"prompt,omitempty"`
	Name   string `json:"name,omitempty"`
	Input  any    `json:"input,omitempty"`
	// Off: kept but not listening — no events, and no subscription for sources that only produce what is
	// subscribed to. How a person switches off one a panel ships: a copy in user/triggers/ with "off": true.
	Off bool `json:"off,omitempty"`
	// Source + Until: a wait for a state instead of events (Key names the one thing, no *)
	Source string `json:"source,omitempty"`
	Until  string `json:"until,omitempty"`
}

// TriggerInfo is a trigger with how its last run went (kept while Gerdr runs), for `gerdr panel triggers`.
type TriggerInfo struct {
	Trigger
	Runs int          `json:"runs"` // since Gerdr started
	Last *ScheduleRun `json:"last,omitempty"`
}

func (t *Trigger) check() error {
	if (t.Fn == "") == (t.Prompt == "") {
		return errors.New(i18n.S("fn 和 prompt 要有且只有一个", "exactly one of fn and prompt"))
	}
	if t.Until != "" || t.Source != "" {
		if err := waits.CheckKey(t.Source, t.Key); err != nil {
			return fmt.Errorf(i18n.S("等状态的触发器要写 source 和 key（一个东西）：%w", "a trigger waiting for a state needs source and key (one thing): %w"), err)
		}
		if _, err := waits.Parse(t.Until); err != nil {
			return fmt.Errorf("until: %w", err)
		}
		return nil
	}
	if strings.TrimSpace(t.On) == "" {
		return errors.New(i18n.S("要写 on：哪类事件（source/kind，可以用 *）", "on is required: which events (source/kind, * allowed)"))
	}
	return ev.CheckPattern(t.On)
}

// ParseTriggers reads a Work directory's triggers/; like schedules, a broken file stops them all, loudly.
func ParseTriggers(dir string) ([]Trigger, error) {
	files, err := overlayJSON(dir, TriggersDir)
	if err != nil {
		return nil, err
	}
	var out []Trigger
	for _, id := range overlayIDs(files) {
		f := files[id]
		if !scheduleIDRe.MatchString(id) {
			return nil, fmt.Errorf(i18n.S("%s：文件名只能用字母、数字、. - _", "%s: the file name may only use letters, digits, . - _"), f.rel)
		}
		b, err := os.ReadFile(f.abs)
		if err != nil {
			return nil, err
		}
		var t Trigger
		if err := json.Unmarshal(b, &t); err != nil {
			return nil, fmt.Errorf(i18n.S("%s 格式不对：%w", "%s is malformed: %w"), f.rel, err)
		}
		t.ID, t.Prompt = id, strings.TrimSpace(t.Prompt)
		if err := t.check(); err != nil {
			return nil, fmt.Errorf("%s: %w", f.rel, err)
		}
		out = append(out, t)
	}
	return out, nil
}

type trigProject struct {
	sig  string
	defs []Trigger
	err  error
	// per trigger: runs go one at a time, in event order (a queue drained by one goroutine)
	queue map[string]chan ev.Event
	runs  map[string]int
	last  map[string]*ScheduleRun
	// spent: until-triggers that ran for their condition holding, by id → the definition they ran as; cleared when
	// the condition stops holding or the definition changes (in memory: a restart may run one again once)
	spent map[string]string
	// held: triggers with a conversation change queued and not started yet (DispatchSession): more changes meanwhile
	// fold into that one run instead of queueing a run each
	held map[string]bool
}

type triggers struct {
	mu       sync.Mutex
	projects map[string]*trigProject
}

// trig is the project's triggers, re-read when the folder changed. Caller holds r.trig_.mu.
func (r *Runner) trig(project string) *trigProject {
	if project == "" {
		project = DefaultProject
	}
	if r.trig_.projects == nil {
		r.trig_.projects = map[string]*trigProject{}
	}
	p := r.trig_.projects[project]
	if p == nil {
		p = &trigProject{queue: map[string]chan ev.Event{}, runs: map[string]int{}, last: map[string]*ScheduleRun{}, spent: map[string]string{}, held: map[string]bool{}}
		r.trig_.projects[project] = p
	}
	dir, err := r.Dir(project)
	if err != nil {
		p.defs, p.err = nil, err
		return p
	}
	if sig := overlaySig(dir, TriggersDir); sig != p.sig || sig == "" {
		p.sig = sig
		prev := p.err
		p.defs, p.err = ParseTriggers(dir)
		if p.err != nil && (prev == nil || prev.Error() != p.err.Error()) && r.Log != nil {
			r.Log.Warn("work: triggers not run", "project", project, "err", p.err)
		}
	}
	return p
}

// DispatchEvent hands an event to every project's matching triggers (queued: it returns at once).
func (r *Runner) DispatchEvent(e ev.Event) {
	if e.Depth > ev.MaxDepth {
		if r.Log != nil {
			r.Log.Warn("work: event too deep, not given to triggers (triggers feeding each other?)", "source", e.Source, "kind", e.Kind, "depth", e.Depth)
		}
		return
	}
	r.trig_.mu.Lock()
	defer r.trig_.mu.Unlock()
	for _, project := range r.workProjects() {
		p := r.trig(project)
		for _, t := range p.defs {
			if t.Off || t.Until != "" || !ev.Match(t.On, e) || !ev.MatchKey(t.Key, e.Key) {
				continue
			}
			q := p.queue[t.ID]
			if q == nil {
				q = make(chan ev.Event, 256)
				p.queue[t.ID] = q
				go r.drainTrigger(project, t.ID, q)
			}
			select {
			case q <- e:
			default:
				if r.Log != nil {
					r.Log.Warn("work: trigger falling behind, dropped an event", "project", project, "trigger", t.ID, "event", e.ID)
				}
			}
		}
	}
}

// SessionSource is the source of the events about a panel's own conversations (DispatchSession).
const SessionSource = "gerdr"

// IsSessionEvent: one of DispatchSession's.
func IsSessionEvent(e ev.Event) bool {
	return e.Source == SessionSource && strings.HasPrefix(e.Kind, "session.")
}

// DispatchSession: one of panel's conversations (one it started, or one working for one of those) changed status.
// Its triggers on "gerdr/session.*" (kind session.<status>: running, idle, waiting, error…; key the session's id) run,
// so a board follows its workers the moment they stop, ask or fail instead of polling every minute. Only that
// panel's triggers, and not through the event store: these are Gerdr's own, many, and of no use to anyone else.
// Folded like a stream's messages: while a run is queued and not started, further changes add nothing, so a burst is
// one run and a change after a run started is one more after it. The event says what moved; the function reads the
// state as it is now.
func (r *Runner) DispatchSession(panel string, e ev.Event) {
	r.trig_.mu.Lock()
	defer r.trig_.mu.Unlock()
	p := r.trig(panel)
	for _, t := range p.defs {
		if t.Off || t.Until != "" || !ev.Match(t.On, e) || !ev.MatchKey(t.Key, e.Key) || p.held[t.ID] {
			continue
		}
		q := p.queue[t.ID]
		if q == nil {
			q = make(chan ev.Event, 256)
			p.queue[t.ID] = q
			go r.drainTrigger(panel, t.ID, q)
		}
		select {
		case q <- e:
			p.held[t.ID] = true
		default:
		}
	}
}

// drainTrigger runs one trigger's queued events one by one, with its current definition (it may have been edited
// since the event was queued; removed = the event is dropped).
func (r *Runner) drainTrigger(project, id string, q chan ev.Event) {
	for e := range q {
		r.trig_.mu.Lock()
		p := r.trig(project)
		if IsSessionEvent(e) {
			delete(p.held, id) // starting now: a change from here on is one more run after this one
		}
		var t *Trigger
		for i := range p.defs {
			if p.defs[i].ID == id {
				t = &p.defs[i]
			}
		}
		var def Trigger
		if t != nil {
			def = *t
		}
		r.trig_.mu.Unlock()
		if t == nil {
			continue
		}
		run := r.runTrigger(project, def, e)
		r.trig_.mu.Lock()
		p.runs[id]++
		p.last[id] = run
		r.trig_.mu.Unlock()
	}
}

func (r *Runner) runTrigger(project string, t Trigger, e ev.Event) *ScheduleRun {
	start := time.Now()
	run := &ScheduleRun{StartedAt: start}
	var err error
	if t.Fn != "" {
		input := map[string]any{}
		if m, ok := t.Input.(map[string]any); ok {
			for k, v := range m {
				input[k] = v
			}
		}
		input["event"], input["trigger"] = eventMap(e), t.ID
		if t.Until != "" {
			// A state, not an event: what it is like now, as the source said (stateRun)
			var d struct {
				State  map[string]any `json:"state"`
				Detail string         `json:"detail"`
				URL    string         `json:"url"`
			}
			_ = json.Unmarshal(e.Data, &d)
			delete(input, "event")
			input["state"], input["detail"], input["url"] = d.State, d.Detail, d.URL
			input["source"], input["key"], input["until"] = t.Source, t.Key, t.Until
		}
		rn := r.Start(project, t.Fn, input)
		run.RunID = rn.ID
		var res any
		if res, err = rn.Wait(context.Background()); err == nil {
			b, _ := json.Marshal(res)
			run.Result = cut(string(b), 500)
		}
	} else {
		err = r.triggerPrompt(project, t, e)
	}
	run.Ms, run.OK = time.Since(start).Milliseconds(), err == nil
	if err != nil {
		run.Error = err.Error()
		if r.Log != nil {
			r.Log.Warn("work: trigger failed", "project", project, "id", t.ID, "event", e.ID, "err", err)
		}
	}
	return run
}

// eventMap: the event as the function sees it (data parsed, not a JSON string).
func eventMap(e ev.Event) map[string]any {
	b, _ := json.Marshal(e)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func (r *Runner) triggerPrompt(project string, t Trigger, e ev.Event) error {
	if r.Host == nil {
		return errNoHost
	}
	name := t.Name
	if name == "" {
		name = t.ID
	}
	head := fmt.Sprintf(i18n.S("[触发器「%s」：%s/%s]", "[Trigger %s: %s/%s]"), name, e.Source, e.Kind)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err := r.Host.Send(ctx, project, "work", e.Summary(head)+"\n\n"+t.Prompt, SendOptions{Fold: head})
	return err
}

// Triggers lists a project's triggers with their last run.
func (r *Runner) Triggers(project string) ([]TriggerInfo, error) {
	r.trig_.mu.Lock()
	defer r.trig_.mu.Unlock()
	p := r.trig(project)
	if p.err != nil {
		return nil, p.err
	}
	out := []TriggerInfo{}
	for _, t := range p.defs {
		out = append(out, TriggerInfo{Trigger: t, Runs: p.runs[t.ID], Last: p.last[t.ID]})
	}
	return out, nil
}

// TriggerWait is a trigger waiting for a state (until), as the sources reporting it see the keys waited on.
type TriggerWait struct {
	Panel  string `json:"panel"` // the Work key (default/lb看板)
	ID     string `json:"id"`
	Source string `json:"source"`
	Key    string `json:"key"`
	Until  string `json:"until"`
}

// StateWaits: every panel's triggers waiting for a state that are on. Cheap to call often: each folder is re-read
// only when it changed (overlaySig).
func (r *Runner) StateWaits() []TriggerWait {
	r.trig_.mu.Lock()
	defer r.trig_.mu.Unlock()
	var out []TriggerWait
	for _, project := range r.workProjects() {
		for _, t := range r.trig(project).defs {
			if !t.Off && t.Until != "" {
				out = append(out, TriggerWait{Panel: project, ID: t.ID, Source: t.Source, Key: t.Key, Until: t.Until})
			}
		}
	}
	return out
}

// StateNow is a source's latest state of one thing, as DispatchState gets it.
type StateNow struct {
	State  map[string]any `json:"state"`
	Detail string         `json:"detail,omitempty"`
	URL    string         `json:"url,omitempty"`
	At     int64          `json:"at"`
}

// DispatchState runs the triggers waiting on this thing whose condition holds for its state now, each once
// (spent until the condition stops holding or its file changes). Call it whenever a source reports a state, and
// after trigger files changed (a new wait on something already true runs at once). Returns the errors of
// conditions that can't be evaluated, by panel/id.
func (r *Runner) DispatchState(source, key string, st StateNow) map[string]error {
	r.trig_.mu.Lock()
	defer r.trig_.mu.Unlock()
	var errs map[string]error
	data, _ := json.Marshal(st)
	for _, project := range r.workProjects() {
		p := r.trig(project)
		for _, t := range p.defs {
			if t.Off || t.Until == "" || t.Source != source || t.Key != key {
				continue
			}
			c, err := waits.Parse(t.Until)
			var hit bool
			if err == nil {
				hit, err = c.Eval(st.State)
			}
			if err != nil {
				if errs == nil {
					errs = map[string]error{}
				}
				errs[project+"/"+t.ID] = err
			}
			def, _ := json.Marshal(t)
			if !hit {
				delete(p.spent, t.ID)
				continue
			}
			if p.spent[t.ID] == string(def) {
				continue
			}
			p.spent[t.ID] = string(def)
			q := p.queue[t.ID]
			if q == nil {
				q = make(chan ev.Event, 256)
				p.queue[t.ID] = q
				go r.drainTrigger(project, t.ID, q)
			}
			e := ev.Event{ID: fmt.Sprintf("state:%s:%s:%d", source, key, st.At), Source: source, Kind: "state", Key: key, Text: st.Detail, URL: st.URL, Data: data, At: st.At}
			select {
			case q <- e:
			default:
				if r.Log != nil {
					r.Log.Warn("work: trigger falling behind, dropped a state", "project", project, "trigger", t.ID)
				}
			}
		}
	}
	return errs
}

// TriggersPath: is rel (a path in a panel's folder) one of its trigger files' folders? A function writing there
// may have added or removed a wait for a state.
func TriggersPath(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, d := range []string{TriggersDir, UserDir + "/" + TriggersDir} {
		if rel == d || strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}

// depthOf: the depth an event emitted by this run gets — one more than the event that started it (a trigger's
// input.event), 0 for a run nothing triggered.
func depthOf(input any) int {
	m, ok := input.(map[string]any)
	if !ok {
		return 0
	}
	e, ok := m["event"].(map[string]any)
	if !ok {
		return 0
	}
	d, _ := e["depth"].(float64)
	return int(d) + 1
}

// eventsObject is ctx.events: emit what a poll found, look back at what came.
func (r *runner) eventsObject() map[string]any {
	bus := r.r.Events
	return map[string]any{
		// ctx.events.emit({ id?, source, kind, key?, title?, text?, url?, data? }) → { event, fresh }
		"emit": func(arg any) map[string]any {
			if bus == nil {
				r.throw(errors.New("ctx.events: not available here (needs Gerdr's server)"))
			}
			b, err := json.Marshal(plain(toPlain(arg)))
			if err != nil {
				r.throw(err)
			}
			var e ev.Event
			if err := json.Unmarshal(b, &e); err != nil {
				r.throw(fmt.Errorf("ctx.events.emit: %w", err))
			}
			e.At, e.Depth = 0, r.depth
			out, fresh, err := bus.Emit(e)
			if err != nil {
				r.throw(fmt.Errorf("ctx.events.emit: %w", err))
			}
			return map[string]any{"event": eventMap(out), "fresh": fresh}
		},
		// ctx.events.catalog() → what can be subscribed to: add-ons' declared sources, then what came lately
		"catalog": func() []ev.Source {
			if bus == nil {
				r.throw(errors.New("ctx.events: not available here (needs Gerdr's server)"))
			}
			return bus.Catalog()
		},
		// ctx.events.reply(event, text, data?) → { event, fresh }: answers through the event's own reply address, so
		// a panel answers a Slack DM and a Feishu message the same way. Throws when the event has none.
		"reply": func(evArg any, text string, extra jsValue) map[string]any {
			if bus == nil {
				r.throw(errors.New("ctx.events: not available here (needs Gerdr's server)"))
			}
			b, _ := json.Marshal(plain(toPlain(evArg)))
			var src ev.Event
			_ = json.Unmarshal(b, &src)
			if src.Reply == nil || src.Reply.Kind == "" {
				r.throw(fmt.Errorf("ctx.events.reply: %s/%s has no reply address", src.Source, src.Kind))
			}
			data := map[string]any{}
			if len(src.Reply.Data) > 0 {
				_ = json.Unmarshal(src.Reply.Data, &data)
			}
			var more map[string]any
			if err := r.opts(extra, &more); err != nil {
				r.throw(fmt.Errorf("ctx.events.reply: %w", err))
			}
			for k, v := range more {
				data[k] = v
			}
			data["text"] = text
			db, _ := json.Marshal(data)
			// From this project, about the event answered (key kept, so a source can thread it)
			out, fresh, err := bus.Emit(ev.Event{Source: replySource(r.project), Kind: src.Reply.Kind, Key: src.Key, Text: text, Data: db, Depth: r.depth})
			if err != nil {
				r.throw(fmt.Errorf("ctx.events.reply: %w", err))
			}
			return map[string]any{"event": eventMap(out), "fresh": fresh}
		},
		// ctx.events.list({ since?, on?, key?, limit? }) → events, oldest first
		"list": func(arg jsValue) []map[string]any {
			if bus == nil {
				r.throw(errors.New("ctx.events: not available here (needs Gerdr's server)"))
			}
			var q ev.Query
			if err := r.opts(arg, &q); err != nil {
				r.throw(fmt.Errorf("ctx.events.list: %w", err))
			}
			l, err := bus.List(q)
			if err != nil {
				r.throw(err)
			}
			out := make([]map[string]any, 0, len(l))
			for _, e := range l {
				out = append(out, eventMap(e))
			}
			return out
		},
	}
}

// replySource: the source name a project's replies go out under — its key with what a source name can't hold
// replaced ("default/lb看板" → "default-lb-"), so the add-on delivering it can tell who answered.
func replySource(project string) string {
	if project == "" {
		project = DefaultProject
	}
	var b strings.Builder
	for _, c := range project {
		if c < 128 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	s := strings.TrimLeft(b.String(), "-._")
	if s == "" {
		s = "panel"
	}
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}
