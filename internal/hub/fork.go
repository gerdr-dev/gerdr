package hub

import (
	"context"
	"errors"
	"maps"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/internal/agenthost"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Forking with the same engine (claude → claude, a claude role → claude…): the agent copies its own session
// (acpx.MethodFork), so the fork's prompt starts with the original's word for word. The prompt cache hits, and the
// agent has the whole context, tool output included, instead of the text summary a handoff carries (handoff.go),
// which is what a fork to another engine still gets: no agent can read another's transcript.

// forkLevel marks where a same-engine fork starts. Unlike handoffLevel nothing rides along with the next message
// (the agent already has the history) and rewinding past it works (the copy has those messages too).
const forkLevel = "fork"

var errOtherEngine = errors.New("another engine")

// engine: what runs an agent: a role's or variant's base, else the agent itself (an external ACP agent).
func (h *Hub) engine(agent string) string {
	if d, ok := h.agentDef(agent); ok && d.Base != "" {
		return d.Base
	}
	return agent
}

// forkSame makes o's session as a copy of src's agent session. src's title fields are the fork's already
// (continueFrom); evs are copies of src's log. errOtherEngine when it doesn't apply; any other error means the agent
// couldn't copy it (nothing written yet, an engine without MethodFork, a variant with its own records folder), and
// the caller hands the history over as text instead.
// before: the running turn's first message, when src is mid-turn (acpx.ForkRequest.Before).
func (h *Hub) forkSame(ctx context.Context, o CreateOptions, src Record, evs []*Event, before string) (*Session, error) {
	if src.AgentSessionID == "" || h.engine(o.Agent) != h.engine(src.Agent) {
		return nil, errOtherEngine
	}
	tags, err := cleanTags(o.Tags)
	if err != nil {
		return nil, err
	}
	c, err := h.host(ctx, o.Agent)
	if err != nil {
		return nil, err
	}
	res, err := agenthost.Call[acpx.ForkResponse](ctx, c, acpx.MethodFork, acpx.ForkRequest{SessionId: acp.SessionId(src.AgentSessionID), Cwd: src.Cwd, Before: before})
	if err != nil {
		return nil, err
	}
	if res.SessionId == "" {
		return nil, errors.New("the agent returned no session")
	}
	// The copy goes on as the original was set up (mode, model, effort: the same context deserves the same model),
	// where the original was (the agent's copy is filed under that folder), in the original's project.
	mode, config := src.Mode, maps.Clone(src.Config)
	if o.Mode != "" {
		mode = o.Mode
	}
	if len(o.Config) > 0 {
		if config == nil {
			config = map[string]string{}
		}
		maps.Copy(config, o.Config)
	}
	t := now()
	rec := Record{
		ID: newID(), Agent: o.Agent, AgentSessionID: string(res.SessionId), Cwd: src.Cwd, Workdir: src.Workdir,
		CreatedAt: t, UpdatedAt: t,
		Title: src.Title, CustomTitle: src.CustomTitle, TitleFixed: src.TitleFixed,
		Mode: mode, Config: config, SkillDirs: src.SkillDirs, SystemFile: src.SystemFile,
		Project: o.Project, ArtifactsDir: o.ArtifactsDir, SkillUpdates: o.SkillUpdates,
		Tags: tags, ReportTo: o.ReportTo, Parent: o.Parent, Muted: o.Muted,
		CtxTokens: src.CtxTokens, CtxWindow: src.CtxWindow,
		GerdrOnly: true, // the copy is only Gerdr's
	}
	if h.skillUpdatesOn(rec) {
		rec.SkillsSeen = rec.CreatedAt
	}
	s := newSession(h, rec)
	h.register(s)
	h.saveIndex()
	h.broadcastSession(s)
	all := s.rewriteLog(func() { s.events = append(evs, s.events...) })
	h.broadcastEvents(rec.ID, all, true)
	s.emit(&Event{Kind: EvNotice, Level: forkLevel, Text: i18n.S("从原来的对话分叉出来：上面是分叉前的对话，接着往下聊就行；原来的对话不受影响", "Forked from the original conversation: above is the conversation up to the fork; just carry on. The original is unaffected")})
	h.saveIndex()
	h.broadcastSession(s)
	return s, nil
}
