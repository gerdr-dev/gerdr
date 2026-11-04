package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A resumed process's running total starts at what the transcript's last cost-state says (the CLI restores it), so the
// first turn after an idle reap counts only its own growth, not the session's whole history again.
func TestResumedCostSeedsDelta(t *testing.T) {
	root := t.TempDir()
	cwd := "/w/p"
	id := "2d2e5e1c-4d93-4c49-8c5a-07146b15fcf0"
	dir := projectDirFor(root, cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"` + strings.Repeat("x", 200<<10) + ` cost-state"}}`,
		`{"type":"cost-state","sessionId":"` + id + `","totalCostUSD":1.5}`,
		`{"type":"cost-state","sessionId":"other-session-0000","totalCostUSD":99}`,
		`{"type":"cost-state","sessionId":"` + id + `","totalCostUSD":22.25}`,
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	base := resumedCost(root, id, cwd)
	if base != 22.25 {
		t.Fatalf("resumedCost = %v, want 22.25", base)
	}
	p := &proc{pending: map[string]chan ctlResult{}, costUSD: base}
	if d := p.costDelta(23.5); d != 1.25 {
		t.Fatalf("first turn after resume = %v, want 1.25", d)
	}
	if got := resumedCost(root, "00000000-0000-0000-0000-000000000000", cwd); got != 0 {
		t.Fatalf("no transcript: %v, want 0", got)
	}
}
