package hub

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/chatlog"
	"github.com/gerdr-dev/gerdr/internal/agenthost"
)

// fakeAgent: just enough of an agent for a turn that runs until released, and steers.
type fakeAgent struct {
	acp.Agent // unused methods panic
	commands  bool

	conn     *acp.AgentSideConnection
	mu       sync.Mutex
	prompts  []string
	steers   []string
	release  chan struct{}
	finished chan string
}

func (f *fakeAgent) SetConn(c *acp.AgentSideConnection) { f.conn = c }
func (f *fakeAgent) Close()                             {}

func (f *fakeAgent) Initialize(ctx context.Context, p acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{ProtocolVersion: acp.ProtocolVersionNumber,
		Meta: map[string]any{"steering": map[string]any{"supported": true, "commands": f.commands}}}, nil
}

func (f *fakeAgent) ResumeSession(ctx context.Context, p acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, nil
}

func (f *fakeAgent) Prompt(ctx context.Context, p acp.PromptRequest) (acp.PromptResponse, error) {
	t := contentText(mustRaw(p.Prompt))
	f.mu.Lock()
	f.prompts = append(f.prompts, t)
	first := len(f.prompts) == 1
	f.mu.Unlock()
	if first {
		// say something first: a turn the agent already answered in (Escape takes back only what's queued)
		_ = f.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: p.SessionId, Update: acp.UpdateAgentMessageText("on it")})
		<-f.release
	}
	f.finished <- t
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (f *fakeAgent) Cancel(ctx context.Context, p acp.CancelNotification) error {
	select {
	case <-f.release:
	default:
		close(f.release)
	}
	return nil
}

func (f *fakeAgent) HandleExtensionMethod(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case acpx.MethodSteer:
		var p acpx.SteerRequest
		_ = json.Unmarshal(params, &p)
		f.mu.Lock()
		f.steers = append(f.steers, contentText(mustRaw(p.Prompt)))
		f.mu.Unlock()
		return acpx.SteerResponse{Outcome: acpx.SteerInjected}, nil
	case acpx.MethodWithdraw:
		return acpx.WithdrawResponse{}, nil
	}
	return nil, acp.NewMethodNotFound(method)
}

func mustRaw(blocks []acp.ContentBlock) []json.RawMessage {
	var out []json.RawMessage
	for _, b := range blocks {
		j, _ := json.Marshal(b)
		out = append(out, j)
	}
	return out
}

func startFake(t *testing.T, commands bool) (*Session, *fakeAgent) {
	t.Helper()
	h, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeAgent{commands: commands, release: make(chan struct{}), finished: make(chan string, 4)}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := agenthost.StartInProc(context.Background(), f, &agentHandler{h: h, agent: "fake"}, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); h.Close() })
	h.mu.Lock()
	h.hosts["fake"] = c
	h.mu.Unlock()
	s := newSession(h, Record{ID: "tab", Agent: "fake", Cwd: t.TempDir(), AgentSessionID: "a1"})
	s.commands = []acp.AvailableCommand{{Name: "compact"}, {Name: "rename", Meta: map[string]any{"midTurn": true}}}
	h.register(s)
	return s, f
}

func send(t *testing.T, s *Session, text string) SendResult {
	t.Helper()
	r, err := s.Send(context.Background(), []acp.ContentBlock{acp.TextBlock(text)}, "")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func waitPrompt(t *testing.T, f *fakeAgent) string {
	t.Helper()
	select {
	case p := <-f.finished:
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("no turn finished")
		return ""
	}
}

// waitAnswer: the agent's "on it" is in the log.
func waitAnswer(t *testing.T, s *Session) {
	t.Helper()
	for i := 0; i < 200; i++ {
		for _, e := range s.Events() {
			if e.Kind == EvUpdate {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no answer")
}

func waitIdle(t *testing.T, s *Session) {
	t.Helper()
	for i := 0; i < 200; i++ {
		s.mu.Lock()
		r := s.running
		s.mu.Unlock()
		if !r {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("still running")
}

// A slash command sent mid-turn to an agent that reads commands only from a prompt waits for the turn to end, then
// goes as a turn of its own; text and midTurn commands still go in as steers.
func TestDeferredCommand(t *testing.T) {
	s, f := startFake(t, false)
	send(t, s, "work")
	if r := send(t, s, "/compact keep the plan"); !r.Steered {
		t.Fatal("held command should report steered")
	}
	send(t, s, "/rename x")
	send(t, s, "and this")
	f.mu.Lock()
	steers := append([]string(nil), f.steers...)
	f.mu.Unlock()
	if !eq(steers, []string{"/rename x", "and this"}) {
		t.Fatalf("steers %q", steers)
	}
	close(f.release)
	if p := waitPrompt(t, f); p != "work" {
		t.Fatalf("first turn %q", p)
	}
	if p := waitPrompt(t, f); p != "/compact keep the plan" {
		t.Fatalf("held command ran as %q", p)
	}
	waitIdle(t, s)
	evs := s.Events()
	if got := texts(evs); !eq(got, []string{"work", "/rename x", "and this", "/compact keep the plan"}) {
		t.Fatalf("log %q", got)
	}
	// the file says the same (the queued copy was rewritten away while the agent kept writing)
	s.flush(true)
	disk, err := chatlog.ReadFile(s.lf.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(disk); !eq(got, []string{"work", "/rename x", "and this", "/compact keep the plan"}) {
		t.Fatalf("log file %q", got)
	}
	if len(disk) != len(s.Events()) {
		t.Fatalf("file has %d events, memory %d", len(disk), len(s.Events()))
	}
	for _, e := range evs {
		if e.Kind == EvUser && contentText(e.Content) == "/compact keep the plan" && e.Steer {
			t.Fatal("the command is logged again as a turn of its own, not a steer")
		}
	}
}

// Stopping takes a held command back into the composer like any queued steer; it doesn't run afterwards.
func TestDeferredCommandEscape(t *testing.T) {
	s, f := startFake(t, false)
	send(t, s, "work")
	waitAnswer(t, s)
	send(t, s, "/compact")
	res, err := s.Escape(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := contentText(res.Content); got != "/compact" {
		t.Fatalf("taken back %q", got)
	}
	waitPrompt(t, f)
	waitIdle(t, s)
	time.Sleep(100 * time.Millisecond)
	f.mu.Lock()
	n := len(f.prompts)
	f.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d prompts, the withdrawn command ran", n)
	}
}

// Withdraw (the queued row's button, ↑) takes a held command back without the agent.
func TestDeferredCommandWithdraw(t *testing.T) {
	s, f := startFake(t, false)
	send(t, s, "work")
	r := send(t, s, "/compact")
	held := false
	for _, e := range s.Events() {
		held = held || (e.Kind == EvUser && e.MessageID == r.MessageID && e.Steer && e.Held)
	}
	if !held {
		t.Fatal("the held command isn't marked held (the UI offers Send now on it)")
	}
	res, err := s.Withdraw(context.Background(), r.MessageID)
	if err != nil || res.Withdrawn != 1 {
		t.Fatalf("withdraw: %v %+v", err, res)
	}
	close(f.release)
	waitPrompt(t, f)
	waitIdle(t, s)
	time.Sleep(100 * time.Millisecond)
	if got := texts(s.Events()); !eq(got, []string{"work"}) {
		t.Fatalf("log %q", got)
	}
}

// An agent that handles commands in steers (Claude Code) gets them as steers.
func TestSteerCommandsAgent(t *testing.T) {
	s, f := startFake(t, true)
	send(t, s, "work")
	send(t, s, "/compact")
	f.mu.Lock()
	steers := append([]string(nil), f.steers...)
	f.mu.Unlock()
	if !eq(steers, []string{"/compact"}) {
		t.Fatalf("steers %q", steers)
	}
	close(f.release)
	waitPrompt(t, f)
	waitIdle(t, s)
}
