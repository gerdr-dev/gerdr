package native

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/agents/native/llm"
)

// compactEnv: a session whose model reads a big file four times, the last request reporting more input than the
// compaction threshold (900), then answers "done". compactReply answers the summary requests (i = 0, 1, …).
type compactEnv struct {
	*env
	mu       sync.Mutex
	compacts []int // indexes of the summary requests among all requests
	mains    []int // indexes of the normal requests
}

func setupCompact(t *testing.T, compactReply func(i int) reply) *compactEnv {
	t.Helper()
	ce := &compactEnv{}
	big := strings.Repeat(strings.Repeat("x", 99)+"\n", 300) // 30k characters
	ce.env = setupModel(t, Model{ID: "fake-model", API: "chat", Window: 100_000, CompactAt: 900}, func(n int, msgs []any) reply {
		ce.mu.Lock()
		defer ce.mu.Unlock()
		if lastRole(msgs) == "user" && strings.Contains(lastUser(msgs), "Summarize the conversation so far") {
			ce.compacts = append(ce.compacts, n)
			return compactReply(len(ce.compacts) - 1)
		}
		ce.mains = append(ce.mains, n)
		if k := len(ce.mains); k <= 4 {
			return reply{calls: [][2]string{{"read", `{"path":"big.txt"}`}}, input: []int{200, 400, 600, 950}[k-1]}
		}
		return reply{text: "done", input: 300}
	})
	if err := os.WriteFile(filepath.Join(ce.cwd, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	return ce
}

func (ce *compactEnv) size(i int) int {
	b, _ := json.Marshal(ce.llm.request(i)["messages"])
	return len(b)
}

func (ce *compactEnv) text(i int) string {
	b, _ := json.Marshal(ce.llm.request(i)["messages"])
	return string(b)
}

func (h *handler) notices() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, n := range h.updates {
		if u := n.Update.SessionInfoUpdate; u != nil {
			if v, ok := acpx.GetMeta(u.Meta, "notice"); ok {
				if s, _ := v.(string); s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// The summary request keeps coming back empty: first a 200 with nothing in the stream (as in the real report), then
// a finished reply with no text — an empty summary, which must not replace the history. It used
// to be taken as a failed turn and sent again, unchanged, on every following turn. Now: one try as is, one smaller,
// then the oldest messages are dropped locally, the turn carries on with a smaller request, the user is told the
// real reason, and later turns don't compact again.
func TestCompactEmptySummaryFallsBack(t *testing.T) {
	ce := setupCompact(t, func(i int) reply { return reply{empty: i == 0} })
	sid := ce.newSession(modeBypass)
	if _, err := ce.prompt(sid, uuid.NewString(), "go"); err != nil {
		t.Fatalf("the turn should carry on after a failed compaction: %v", err)
	}
	if len(ce.compacts) != 2 {
		t.Fatalf("want 2 summary attempts (as is, then smaller), got %d", len(ce.compacts))
	}
	if a, b := ce.size(ce.compacts[0]), ce.size(ce.compacts[1]); b >= a*3/4 {
		t.Fatalf("the second summary request should be much smaller: %d → %d", a, b)
	}
	if len(ce.mains) != 5 {
		t.Fatalf("want 5 normal requests, got %d", len(ce.mains))
	}
	before, after := ce.mains[3], ce.mains[4]
	if a, b := ce.size(before), ce.size(after); b >= a/2 {
		t.Fatalf("the request after the fallback should be much smaller: %d → %d", a, b)
	}
	s := ce.text(after)
	if !strings.Contains(s, "Summarizing the earlier part of this conversation failed") || !strings.Contains(s, "empty summary") || !strings.Contains(s, `go`) {
		t.Fatalf("request after the fallback: %s", s)
	}
	var told bool
	for _, n := range ce.h.notices() {
		told = told || strings.Contains(n, "empty summary")
	}
	if !told {
		t.Fatalf("the user wasn't told why: %v", ce.h.notices())
	}
	// The next turn starts below the threshold: no new compaction.
	if _, err := ce.prompt(sid, uuid.NewString(), "again"); err != nil {
		t.Fatal(err)
	}
	if len(ce.compacts) != 2 {
		t.Fatalf("compaction ran again: %d summary requests", len(ce.compacts))
	}
}

// An error from the summary request (not retried by the client: a 400) → the smaller request is tried and its
// summary used; no messages are dropped.
func TestCompactRetriesSmaller(t *testing.T) {
	ce := setupCompact(t, func(i int) reply {
		if i == 0 {
			return reply{status: 400}
		}
		return reply{text: "SUMMARY-OK"}
	})
	sid := ce.newSession(modeBypass)
	if _, err := ce.prompt(sid, uuid.NewString(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(ce.compacts) != 2 {
		t.Fatalf("want 2 summary attempts, got %d", len(ce.compacts))
	}
	if a, b := ce.size(ce.compacts[0]), ce.size(ce.compacts[1]); b >= a*3/4 {
		t.Fatalf("the second summary request should be much smaller: %d → %d", a, b)
	}
	s := ce.text(ce.mains[4])
	if !strings.Contains(s, "SUMMARY-OK") || strings.Contains(s, "xxxxxxxxxx") {
		t.Fatalf("request after compaction should hold only the summary: %s", s)
	}
	var told bool
	for _, n := range ce.h.notices() {
		told = told || strings.Contains(n, "400")
	}
	if !told {
		t.Fatalf("the first failure wasn't reported: %v", ce.h.notices())
	}
}

// /compact typed by the user doesn't drop messages when no summary can be made: it reports the error.
func TestManualCompactFailureReported(t *testing.T) {
	ce := setupCompact(t, func(int) reply { return reply{empty: true} })
	sid := ce.newSession(modeBypass)
	if _, err := ce.prompt(sid, uuid.NewString(), "/compact"); err == nil || !strings.Contains(err.Error(), "no finish_reason") {
		t.Fatalf("want the upstream's error, got %v", err)
	}
}

func TestBuildContextKeep(t *testing.T) {
	msg := func(role, text string) entry {
		return entry{T: "msg", Msg: &llm.Message{Role: role, Content: []llm.Part{llm.TextPart(text)}}}
	}
	es := []entry{msg("user", "u1"), msg("assistant", "a1"), msg("user", "u2"), msg("assistant", "a2"),
		{T: "compact", Summary: "S", Keep: 2}, msg("user", "u3")}
	var got []string
	for _, m := range buildContext(es) {
		got = append(got, userText(m))
	}
	if want := []string{summaryPrefix + "S", "u2", "a2", "u3"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
}

// Neither the smaller summary request nor what dropOldest keeps may start with tool results whose call is gone.
func TestCutNeverStartsWithToolResult(t *testing.T) {
	long := strings.Repeat("y", 10_000)
	var ms []llm.Message
	ms = append(ms, llm.Message{Role: "user", Content: []llm.Part{llm.TextPart("task")}})
	for range 20 {
		ms = append(ms, llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c", Name: "read", Args: "{}"}}},
			llm.Message{Role: "tool", CallID: "c", Content: []llm.Part{llm.TextPart(long)}})
	}
	small, changed := shrinkForSummary(ms, 0.25, 3000)
	if !changed || small[0].Role != "user" || small[1].Role == "tool" || !strings.Contains(userText(small[0]), "left out") {
		t.Fatalf("shrunk: changed=%v, %d messages, roles %s %s", changed, len(small), small[0].Role, small[1].Role)
	}
	if e := estimate("", small, 0.25); e > 3000 {
		t.Fatalf("still %d tokens", e)
	}
	keep, text := dropOldest(ms, 0.25, 6000, "boom")
	if keep == 0 || keep >= len(ms) || ms[len(ms)-keep].Role == "tool" {
		t.Fatalf("keep %d", keep)
	}
	if !strings.Contains(text, "boom") || !strings.Contains(text, "task") {
		t.Fatalf("text: %s", text)
	}
}
