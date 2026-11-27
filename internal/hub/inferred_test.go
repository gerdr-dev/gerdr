package hub

import (
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

func TestOpenCallCarriesPastPromptResponse(t *testing.T) {
	s, f := startFake(t, false)
	s.h.defsMu.Lock()
	s.h.defs = []AgentDef{{ID: "fake", Command: []string{"external-acp"}}}
	s.h.defsMu.Unlock()
	f.updates = []acp.SessionUpdate{{
		ToolCall: &acp.SessionUpdateToolCall{ToolCallId: "shell", Status: acp.ToolCallStatusInProgress},
	}}
	send(t, s, "start a shell")
	close(f.release)
	waitPrompt(t, f)
	for deadline := time.Now().Add(2 * time.Second); ; {
		events := s.Events()
		if last := events[len(events)-1]; last.Kind == EvTurnStart && last.Auto {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("prompt ended without handing off to an inferred turn")
		}
		time.Sleep(time.Millisecond)
	}
	if summary := s.Summary(); summary.Status != StRunning || summary.Turn == nil {
		t.Fatalf("open shell reported idle: %v", summary.Status)
	}
	s.endInferred() // sending a new prompt closes the visible inferred turn
	s.mu.Lock()
	carried := s.inferOpen["shell"]
	s.mu.Unlock()
	if !carried {
		t.Fatal("ending the inferred turn to send a new prompt forgot background work")
	}
	st := acp.ToolCallStatusCompleted
	s.onUpdate(acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{ToolCallId: "shell", Status: &st}})
	s.onUpdate(acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{ToolCallId: "shell"}})
	quietInferred(s)
	if summary := s.Summary(); summary.Turn != nil {
		t.Fatal("a status-less follow-up reopened the completed shell")
	}
}

func TestAgyChildKeepsInferredTurnPastQuiet(t *testing.T) {
	s, _ := startFake(t, false)
	var childRunning atomic.Bool
	childRunning.Store(true)
	s.agyACP = true
	s.agyProbe = func(id string) bool { return id == s.agentSID() && childRunning.Load() }
	title, st := "Run invoke_subagent", acp.ToolCallStatusCompleted
	s.noteActivity(acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{
		ToolCallId: "child-launch", Title: &title, Status: &st,
	}})
	quietInferred(s)
	if summary := s.Summary(); summary.Turn == nil {
		t.Fatal("inferred turn ended while the child was still running")
	}
	childRunning.Store(false)
	quietInferred(s)
	if summary := s.Summary(); summary.Turn != nil {
		t.Fatal("inferred turn did not end after the child became idle")
	}
}

func quietInferred(s *Session) {
	s.mu.Lock()
	s.inferLast = time.Now().Add(-2 * inferQuiet)
	s.mu.Unlock()
	s.inferCheck()
}

// agy answers session/prompt with end_turn and then keeps working (background subagents): what it sends after that
// opens an auto turn, which ends once it goes quiet. Gerdr's own adapters report their turns, so nothing is guessed.
func TestInferredTurn(t *testing.T) {
	h := &Hub{opt: Options{Dir: t.TempDir()}, sessions: map[string]*Session{}, subs: map[*Sub]struct{}{}, log: slog.Default()}
	h.defs = []AgentDef{{ID: "agy", Command: []string{"agy", "acp"}}}
	chunk := acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock("still going")}}
	kinds := func(s *Session) (out []string) {
		for _, e := range s.Events() {
			out = append(out, e.Kind)
		}
		return
	}

	own := &Session{h: h, loaded: true, rec: Record{ID: "s-claude", Agent: "claude"}, pending: map[string]*pending{}}
	own.onUpdate(chunk)
	if own.running {
		t.Fatal("a built-in agent's stray update opened a turn")
	}

	s := &Session{h: h, loaded: true, rec: Record{ID: "s-agy", Agent: "agy"}, pending: map[string]*pending{}}
	s.beginLoading()
	s.onUpdate(chunk)
	s.endLoading()
	if s.running {
		t.Fatal("a replayed update (session/load) opened a turn")
	}

	s.onUpdate(chunk)
	if !s.running || !s.infer || !s.auto {
		t.Fatalf("work after the turn ended didn't open one: running %v infer %v", s.running, s.infer)
	}
	call := acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{ToolCallId: "c1", Status: acp.ToolCallStatusInProgress}}
	s.onUpdate(call)

	// Quiet for longer than inferQuiet but a tool call is still open: keep running
	s.mu.Lock()
	s.inferLast = time.Now().Add(-2 * inferQuiet)
	s.mu.Unlock()
	s.inferCheck()
	if !s.running {
		t.Fatal("ended while a tool call was still open")
	}
	st := acp.ToolCallStatusCompleted
	s.onUpdate(acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{ToolCallId: "c1", Status: &st}})
	s.mu.Lock()
	s.inferLast = time.Now().Add(-2 * inferQuiet)
	s.mu.Unlock()
	s.inferCheck()
	if s.running || s.infer {
		t.Fatal("didn't end once quiet")
	}
	k := kinds(s)
	// the replayed update, then the inferred turn around the rest
	if len(k) != 6 || k[0] != EvUpdate || k[1] != EvTurnStart || k[5] != EvTurnEnd {
		t.Fatalf("events %v: want update, turn_start, 3 updates, turn_end", k)
	}
	s.mu.Lock()
	if s.inferTimer != nil {
		s.inferTimer.Stop()
	}
	s.mu.Unlock()
}
