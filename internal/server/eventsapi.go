package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/internal/events"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Events (docs/dev/EVENTS.md): every new event goes to
//   - every open window ("event" broadcast: add-ons' g.on("event"));
//   - the sessions watching it (gerdr event watch), as a message;
//   - the add-on services that list a matching pattern in addon.json's service.events, as a stdin line;
//   - every Work / panel's triggers/ (work.Runner.DispatchEvent).

// RunEvents wires the event store to its subscribers. Call once, after the hub, the Work runner and the add-on
// services exist.
func (s *Server) RunEvents() {
	if s.Events == nil {
		return
	}
	s.Events.OnEvent(func(e events.Event) {
		s.Hub.Broadcast("event", e)
		if e.Depth > events.MaxDepth {
			// A trigger chain past its depth: kept and shown, but not handed on (it would go round again)
			s.Log.Warn("event too deep, not delivered", "source", e.Source, "kind", e.Kind, "depth", e.Depth)
			return
		}
		go s.deliverWatches(e)
		if s.AddonSvc != nil {
			line, _ := json.Marshal(map[string]any{"type": "event", "event": e})
			s.AddonSvc.SendWhere(func(patterns []string) bool {
				for _, p := range patterns {
					if events.Match(p, e) {
						return true
					}
				}
				return false
			}, line)
		}
		if s.Work != nil {
			s.Work.DispatchEvent(e)
		}
	})
}

// deliverWatches sends the event to each session watching it. A session that's gone takes its watches with it.
func (s *Server) deliverWatches(e events.Event) {
	for _, w := range s.Events.Matching(e) {
		sess, err := s.Hub.Session(w.Session)
		if err != nil {
			s.Events.DropSession(w.Session)
			continue
		}
		text := watchText(e, w)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		_, err = sess.SendFrom(ctx, []acp.ContentBlock{acp.TextBlock(text)}, "", "", false)
		cancel()
		if err != nil {
			s.Log.Warn("event watch: send failed", "session", w.Session, "watch", w.ID, "err", err)
		}
	}
}

// emitsSomewhere: does anything declare or lately emit events on could match?
func (s *Server) emitsSomewhere(on string) bool {
	for _, src := range s.Events.Catalog() {
		if events.Overlap(on, src.On) {
			return true
		}
	}
	return false
}

// watchText: the message a watching session gets — the event, why it watched, how to stop.
func watchText(e events.Event, w events.Watch) string {
	text := e.Summary(fmt.Sprintf(i18n.S("[事件 %s/%s]", "[Event %s/%s]"), e.Source, e.Kind))
	if w.Note != "" {
		text += "\n\n" + fmt.Sprintf(i18n.S("（你订阅它是为了：%s）", "(You watched for this to: %s)"), w.Note)
	}
	if w.Once {
		return text + "\n\n" + i18n.S("（一次性订阅，已经取消）", "(A one-time watch: it's gone now.)")
	}
	return text + "\n\n" + fmt.Sprintf(i18n.S("（不想再收：gerdr event unwatch %s）", "(To stop: gerdr event unwatch %s)"), w.ID)
}

func (s *Server) eventRoutes(mux *http.ServeMux) {
	need := func(w http.ResponseWriter) bool {
		if s.Events == nil {
			httpError(w, 503, "events are off")
			return false
		}
		return true
	}
	// ?since=<unix ms>&on=<pattern>&key=<pattern>&limit=<n>: oldest first, the newest limit of them
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		if !need(w) {
			return
		}
		q := r.URL.Query()
		since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		l, err := s.Events.List(events.Query{Since: since, On: q.Get("on"), Key: q.Get("key"), Limit: limit})
		if err != nil {
			httpError(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]any{"list": l})
	})
	// Emit one: { id?, source, kind, key?, title?, text?, url?, data? } → { event, fresh }
	mux.HandleFunc("POST /api/events", func(w http.ResponseWriter, r *http.Request) {
		if !need(w) {
			return
		}
		var e events.Event
		if err := decode(r, &e); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		e.At = 0
		out, fresh, err := s.Events.Emit(e)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"event": out, "fresh": fresh})
	})
	// What can be subscribed to: add-ons' declared sources, then source/kinds seen in the last 30 days
	mux.HandleFunc("GET /api/events/catalog", func(w http.ResponseWriter, r *http.Request) {
		if !need(w) {
			return
		}
		writeJSON(w, map[string]any{"list": s.Events.Catalog()})
	})
	mux.HandleFunc("GET /api/events/watches", func(w http.ResponseWriter, r *http.Request) {
		if !need(w) {
			return
		}
		writeJSON(w, map[string]any{"list": s.Events.Watches(r.URL.Query().Get("session"))})
	})
	// { on, key?, session, once?, note?, anyway? } → the watch. Refused when nothing declares or lately emitted
	// events on could match (unless anyway): a watch nothing will ever fire is worse than an error.
	mux.HandleFunc("POST /api/events/watches", func(w http.ResponseWriter, r *http.Request) {
		if !need(w) {
			return
		}
		var x struct {
			events.Watch
			Anyway bool `json:"anyway"`
		}
		if err := decode(r, &x); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if _, err := s.Hub.Session(x.Session); err != nil {
			httpError(w, 404, err.Error())
			return
		}
		if err := events.CheckPattern(x.On); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if !x.Anyway && !s.emitsSomewhere(x.On) {
			msg := fmt.Sprintf(i18n.S("没有事件源会发 %s 这类事件：开着的插件里没有声明会发它的，最近 30 天也没出现过。装一个会发它的插件（gerdr addon），或者确定有别的来源（比如面板自己的轮询）就加 --anyway",
				"Nothing emits %s: no switched-on add-on declares it and none came in the last 30 days. Install an add-on that emits it (gerdr addon), or if you know something else will (a panel's own poll), add --anyway"), x.On)
			// A source that reports states instead (GitLab): say how to wait on it
			if src, _, _ := strings.Cut(x.On, "/"); s.Addons != nil {
				for _, st := range s.Addons.States() {
					if st.Source == src {
						msg += fmt.Sprintf(i18n.S("。%s 报告的是状态、不发事件：用 gerdr wait %s <key> --until '<条件>'（gerdr wait sources 看有哪些字段）",
							". %s reports states, not events: use gerdr wait %s <key> --until '<condition>' (gerdr wait sources lists the fields)"), src, src)
						break
					}
				}
			}
			httpError(w, 400, msg)
			return
		}
		out, err := s.Events.AddWatch(x.Watch)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, out)
	})
	mux.HandleFunc("DELETE /api/events/watches/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !need(w) {
			return
		}
		ok, err := s.Events.RemoveWatch(r.PathValue("id"))
		if err != nil {
			httpError(w, 500, err.Error())
			return
		}
		if !ok {
			httpError(w, 404, i18n.S("没有这个订阅", "no such watch"))
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
}
