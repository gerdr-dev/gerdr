package codex

import (
	"context"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/rpcline"
)

// Waking on sub-agents: codex's own background tasks are its sub-agents (multi_agent, on by default). Out of the box
// a sub-agent the main agent didn't `wait` for finishes silently: the main thread gets a subAgentActivity "completed"
// item and nothing else, so a coordinator that ended its turn never hears back (measured, 0.166). The
// `wake_on_agent_messages` feature makes codex start a turn on the main thread when it does — what Claude Code does
// when a background task ends — so an agent can hand a long wait (`gerdr session wait`, a build) to a sub-agent,
// end its turn, and pick the result up when it comes.
//
// It is still "under development" in codex, so: only turned on when this codex lists it, per thread (thread/start |
// resume config, measured to work; experimentalFeature/enablement/set ignores it, and a process-wide `-c` would
// also change sessions that are not ours), and the agent is told in its instructions only then. Without it, agents
// fall back to Gerdr's reports (`--report-to self`).
//
// The turn codex starts by itself has no session/prompt behind it: the adapter tells the hub with
// _meta.gerdr.autoTurn, like the claude and native adapters (events.go).
const wakeFeature = "wake_on_agent_messages"

// wakeHint goes into developerInstructions when the feature is on: what the agent can now do, and how.
const wakeHint = "Sub-agents you start and don't wait for wake you when they finish: their result arrives in a new turn of yours. " +
	"So to wait for something long (a build, a deploy, another Gerdr session) without making the user wait, hand the waiting " +
	"to a sub-agent and end your turn. Tell it to wait inside one exec cell, polling in a JavaScript loop with long " +
	"yield_time_ms (300000), not one model call per 30 seconds."

// detectWake: does this codex have the feature, with sub-agents on? Asked once per app-server.
func (a *Agent) detectWake(ctx context.Context, rpc *rpcline.Conn) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var wake, multi bool
	cursor := ""
	for range 20 {
		p := map[string]any{"limit": 200}
		if cursor != "" {
			p["cursor"] = cursor
		}
		var res struct {
			Data []struct {
				Name    string `json:"name"`
				Enabled bool   `json:"enabled"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := rpc.Call(ctx, "experimentalFeature/list", p, &res); err != nil {
			a.log.Debug("experimentalFeature/list", "err", err)
			return false
		}
		for _, f := range res.Data {
			switch f.Name {
			case wakeFeature:
				wake = true
			case "multi_agent":
				multi = f.Enabled
			}
		}
		if res.NextCursor == nil || *res.NextCursor == "" {
			break
		}
		cursor = *res.NextCursor
	}
	return wake && multi
}

// autoStart: codex started a turn on the main thread by itself (a sub-agent finished and woke it).
func (a *Agent) autoStart(ctx context.Context, s *session, w *acpx.Wake) {
	m := map[string]any{acpx.AutoTurnKey: true}
	if w != nil {
		m[acpx.AutoTurnWakeKey] = w
	}
	a.send(ctx, s.id, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(m)}})
}

func (a *Agent) autoEnd(ctx context.Context, s *session) {
	a.send(ctx, s.id, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{acpx.AutoTurnKey: false})}})
}
