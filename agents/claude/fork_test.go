package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A fork's transcript is the original's line for line, with only the session id swapped and the title lines left
// out; the side folder comes along.
func TestCopyTranscript(t *testing.T) {
	dir := t.TempDir()
	src, id := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	lines := []string{
		`{"type":"session-owner","sessionId":"` + src + `"}`,
		`{"parentUuid":null,"type":"user","uuid":"u1","sessionId":"` + src + `","message":{"role":"user","content":"look at \"sessionId\":\"` + src + `\" here"}}`,
		`{"type":"custom-title","customTitle":"原来的名字","sessionId":"` + src + `"}`,
		`{"type":"ai-title","aiTitle":"修登录页","sessionId":"` + src + `"}`,
		`{"parentUuid":"u1","type":"assistant","uuid":"a1","sessionId":"` + src + `","message":{"role":"assistant","content":"ok"}}`,
	}
	path := filepath.Join(dir, src+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, src, "tool-results"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, src, "tool-results", "x.txt"), []byte("big output"), 0o600)

	if err := copyTranscript(path, src, id, ""); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(b)), "\n")
	want := []string{
		strings.ReplaceAll(lines[0], src, id),
		// the field is swapped; the same text inside the message (escaped) is left alone
		`{"parentUuid":null,"type":"user","uuid":"u1","sessionId":"` + id + `","message":{"role":"user","content":"look at \"sessionId\":\"` + src + `\" here"}}`,
		strings.ReplaceAll(lines[4], src, id),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("copy:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if x, err := os.ReadFile(filepath.Join(dir, id, "tool-results", "x.txt")); err != nil || string(x) != "big output" {
		t.Fatalf("side folder: %v %q", err, x)
	}
	if orig, _ := os.ReadFile(path); string(orig) != strings.Join(lines, "\n")+"\n" {
		t.Fatal("the original changed")
	}
	if _, err := os.Stat(filepath.Join(dir, id+".jsonl.tmp")); err == nil {
		t.Fatal("temp file left")
	}
}

// A fork while a turn runs stops at the line of the message that started the turn, and leaves out a last line still
// being written when that message isn't in the file yet.
func TestCopyTranscriptBefore(t *testing.T) {
	dir := t.TempDir()
	src, id := "11111111-1111-1111-1111-111111111111", "33333333-3333-3333-3333-333333333333"
	lines := []string{
		`{"parentUuid":null,"type":"user","uuid":"u1","sessionId":"` + src + `","message":{"role":"user","content":"hi"}}`,
		`{"parentUuid":"u1","type":"assistant","uuid":"a1","sessionId":"` + src + `","message":{"role":"assistant","content":"ok"}}`,
		`{"parentUuid":"a1","type":"user","uuid":"u2","sessionId":"` + src + `","message":{"role":"user","content":"next"}}`,
		`{"parentUuid":"u2","type":"assistant","uuid":"a2","sessionId":"` + src + `","message":{"role":"assistant","content":"work"}}`,
	}
	path := filepath.Join(dir, src+".jsonl")
	read := func() string {
		b, err := os.ReadFile(filepath.Join(dir, id+".jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	_ = os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	if err := copyTranscript(path, src, id, "u2"); err != nil {
		t.Fatal(err)
	}
	if got, want := read(), strings.ReplaceAll(strings.Join(lines[:2], "\n"), src, id); got != want {
		t.Fatalf("cut at u2:\n%s\nwant:\n%s", got, want)
	}
	// u3 not written yet; the last line has no newline (half written)
	_ = os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"+`{"parentUuid":"a2","type":"assis`), 0o600)
	if err := copyTranscript(path, src, id, "u3"); err != nil {
		t.Fatal(err)
	}
	if got, want := read(), strings.ReplaceAll(strings.Join(lines, "\n"), src, id); got != want {
		t.Fatalf("not written yet:\n%s\nwant:\n%s", got, want)
	}
}
