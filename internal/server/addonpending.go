package server

// What agents wait on a person for, handed to add-on services that asked for it (addon.json "service": {"pending":
// true}), and their answers taken back. Built for answering from somewhere else — a chat (Slack, Feishu) through an
// add-on that knows nothing of either: it posts through a source's reply address on the event bus and reads the
// person's reply from the same conversation (docs/dev/EVENTS.md). Gerdr only says what is pending and checks answers.
//
// Not on the event bus itself: whoever can emit an event (any panel function, `gerdr event emit`) could then approve
// commands. Not in `gerdr session` either: an agent must not approve its own (or another agent's) requests. An add-on
// gets it because the person installed one that asks for it.
//
// A panel gets the same for the conversations it started (ctx.sessions.pending / answer, workhost.go): the team panel
// shows a teammate's agent waiting on an approval and passes the click back. Refused for a call an agent made (a panel
// tool, `gerdr panel run` from a conversation), so an agent can't reach approvals through a panel either.

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// svcPending is one request as a service sees it: the agent's request taken apart, so an add-on needn't know ACP.
type svcPending struct {
	Session string `json:"session"`
	Title   string `json:"title"` // the session's name
	Agent   string `json:"agent"`
	Cwd     string `json:"cwd"`
	ReqID   string `json:"reqId"`
	Kind    string `json:"kind"`    // permission | ask
	Summary string `json:"summary"` // one line ("审批：Write x.txt"), in the computer's language
	At      int64  `json:"at"`      // unix ms
	// Muted: the session sits in a do-not-disturb group (ui Workspace.muted). Still listed — it is still waiting —
	// but an add-on that notifies should leave it alone, as Gerdr does.
	Muted bool `json:"muted,omitempty"`

	// permission: what it wants to do, and the choices (answer with one's id)
	Tool    *svcTool    `json:"tool,omitempty"`
	Options []svcOption `json:"options,omitempty"`
	// ask: the questions (answer each by id with option labels, or own text where allowOther)
	Questions []acpx.AskQuestion `json:"questions,omitempty"`
}

type svcTool struct {
	Title string          `json:"title"`
	Kind  string          `json:"kind,omitempty"`  // read / edit / execute / fetch…
	Input json.RawMessage `json:"input,omitempty"` // the tool's arguments as the agent gave them (a command, a path…)
}

type svcOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"` // allow_once / allow_always / reject_once / reject_always
}

// permRequest is the part of an ACP session/request_permission request read here.
type permRequest struct {
	ToolCall struct {
		Title    string          `json:"title"`
		Kind     string          `json:"kind"`
		RawInput json.RawMessage `json:"rawInput"`
	} `json:"toolCall"`
	Options []permOption `json:"options"`
}

type permOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

func (s *Server) pendingList() []svcPending {
	muted := MutedSessions(s.Layout.Get())
	out := []svcPending{}
	for _, r := range s.Hub.Pending() {
		out = append(out, toSvcPending(r, muted[r.SessionID]))
	}
	return out
}

func toSvcPending(r hub.PendingReq, muted bool) svcPending {
	v := svcPending{Session: r.SessionID, Title: r.Title, Agent: r.Agent, Cwd: r.Cwd, ReqID: r.ReqID, Kind: r.Kind,
		Summary: r.Summary, At: r.At, Muted: muted}
	if v.Title == "" {
		v.Title = filepath.Base(r.Cwd)
	}
	switch r.Kind {
	case "permission":
		var p permRequest
		_ = json.Unmarshal(r.Request, &p)
		v.Tool = &svcTool{Title: p.ToolCall.Title, Kind: p.ToolCall.Kind, Input: p.ToolCall.RawInput}
		for _, o := range p.Options {
			v.Options = append(v.Options, svcOption{ID: o.OptionID, Name: o.Name, Kind: o.Kind})
		}
	case "ask":
		var a acpx.AskRequest
		_ = json.Unmarshal(r.Request, &a)
		v.Questions = a.Questions
	}
	return v
}

func (s *Server) pendingLine() []byte {
	b, _ := json.Marshal(map[string]any{"type": "pending", "pending": s.pendingList()})
	return b
}

// svcAnswer is a service's {"type":"answer",…}.
type svcAnswer struct {
	Session  string              `json:"session"`
	ReqID    string              `json:"reqId"`
	OptionID string              `json:"optionId"`
	Answers  map[string][]string `json:"answers"`
	Skip     bool                `json:"skip"`
}

// answerFromAddon checks an answer against the request it answers and gives it to the session, as the UI's card
// would (the same response shapes: {optionId} / {answers} / {cancelled, reason: skipped}).
func (s *Server) answerFromAddon(addon string, line json.RawMessage) error {
	var a svcAnswer
	if err := json.Unmarshal(line, &a); err != nil {
		return err
	}
	if a.Session == "" || a.ReqID == "" {
		return errors.New("answer needs session and reqId")
	}
	sess, err := s.Hub.Session(a.Session)
	if err != nil {
		return err
	}
	pending := sess.Pending()
	i := slices.IndexFunc(pending, func(r hub.PendingReq) bool { return r.ReqID == a.ReqID })
	if i < 0 {
		return errors.New(i18n.S("这个请求已经处理过了，或者过期了", "This request was already handled, or has expired"))
	}
	b, err := answerResponse(pending[i], a)
	if err != nil {
		return err
	}
	s.Log.Info("request answered by add-on", "addon", addon, "session", a.Session, "kind", pending[i].Kind)
	return sess.Answer(a.ReqID, b)
}

// answerResponse: the response the session takes for this answer, the same shapes the UI's cards send ({optionId} /
// {answers} / {cancelled, reason: skipped}); an error when it doesn't fit the request.
func answerResponse(req hub.PendingReq, a svcAnswer) (json.RawMessage, error) {
	var resp any
	switch req.Kind {
	case "permission":
		var p permRequest
		_ = json.Unmarshal(req.Request, &p)
		if !slices.ContainsFunc(p.Options, func(o permOption) bool { return o.OptionID == a.OptionID }) {
			return nil, fmt.Errorf("optionId %q is not one of this request's options", a.OptionID)
		}
		resp = map[string]string{"optionId": a.OptionID}
	case "ask":
		if a.Skip {
			resp = acpx.AskResponse{Cancelled: true, Reason: "skipped"}
			break
		}
		var r acpx.AskRequest
		_ = json.Unmarshal(req.Request, &r)
		if err := checkAnswers(r.Questions, a.Answers); err != nil {
			return nil, err
		}
		resp = acpx.AskResponse{Answers: a.Answers}
	default:
		return nil, fmt.Errorf("can't answer a %q request", req.Kind)
	}
	return json.Marshal(resp)
}

// checkAnswers: every question answered, by its id, with its options' labels (or own text where it allows other).
func checkAnswers(qs []acpx.AskQuestion, answers map[string][]string) error {
	for _, q := range qs {
		got := answers[q.Id]
		if len(got) == 0 {
			return fmt.Errorf("question %q has no answer", q.Id)
		}
		if len(got) > 1 && !q.MultiSelect {
			return fmt.Errorf("question %q takes one answer", q.Id)
		}
		if q.AllowOther || len(q.Options) == 0 {
			continue
		}
		for _, v := range got {
			if !slices.ContainsFunc(q.Options, func(o acpx.AskOption) bool { return o.Label == v }) {
				return fmt.Errorf("%q is not one of question %q's options", v, q.Id)
			}
		}
	}
	for id := range answers {
		if !slices.ContainsFunc(qs, func(q acpx.AskQuestion) bool { return q.Id == id }) {
			return fmt.Errorf("no question %q", id)
		}
	}
	return nil
}
