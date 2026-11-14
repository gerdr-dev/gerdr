package hub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunCommand(t *testing.T) {
	dir := t.TempDir()
	r, err := RunCommand(context.Background(), "cat; echo; pwd; exit 2", dir, strings.NewReader("from-stdin"), 0)
	if err != nil || r.Code != 2 || !strings.Contains(r.Output, "from-stdin") || r.Truncated {
		t.Fatalf("%+v %v", r, err)
	}
	// Stopped by the caller going away: the whole group, sleep included, and quickly
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	r, err = RunCommand(ctx, "sleep 30 & wait", "", nil, time.Minute)
	if err != nil || r.Code == 0 || r.TimedOut || time.Since(start) > 5*time.Second {
		t.Fatalf("cancel: %+v %v after %s", r, err, time.Since(start))
	}
	if _, err := RunCommand(context.Background(), "true", "relative/dir", nil, 0); err == nil {
		t.Fatal("a relative directory: want an error")
	}
}

func TestHeadTail(t *testing.T) {
	h := &headTail{}
	_, _ = h.Write([]byte("FIRST"))
	_, _ = h.Write([]byte(strings.Repeat("x", 3*execKeep)))
	_, _ = h.Write([]byte("LAST"))
	s, cut := h.text()
	if !cut || !strings.HasPrefix(s, "FIRST") || !strings.HasSuffix(s, "LAST") || len(s) > 2*execKeep+200 {
		t.Fatalf("cut=%v len=%d", cut, len(s))
	}
}

// Stdin is bytes, not text: NULs and invalid UTF-8 arrive as sent, past the old 32 MB cut, and what arrived is
// counted and hashed so the caller can check it (a 45 MB binary used to be mangled, or cut without a word).
func TestRunCommandBinaryStdin(t *testing.T) {
	data := make([]byte, 40<<20)
	for i := range data {
		data[i] = byte(i*7 + i>>11) // every byte value, 0x00 and 0xff included
	}
	copy(data, []byte{0x00, 0xff, 0xfe, 0xc3, 0x28, '\\', 'u', '0', 0x00})
	want := sha256.Sum256(data)
	dir := t.TempDir()
	out := filepath.Join(dir, "out.bin")
	r, err := RunCommand(context.Background(), "cat > out.bin", dir, bytes.NewReader(data), time.Minute)
	if err != nil || r.Code != 0 || r.Stdin == nil {
		t.Fatalf("%+v %v", r, err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, data) {
		t.Fatalf("written %d bytes, sent %d, differ", len(got), len(data))
	}
	if r.Stdin.Bytes != int64(len(data)) || r.Stdin.Sha256 != hex.EncodeToString(want[:]) {
		t.Fatalf("stdin info: %+v", r.Stdin)
	}
	// A command that doesn't read it: still all counted, so a short count only ever means a cut upload
	r, err = RunCommand(context.Background(), "head -c 10 >/dev/null", dir, bytes.NewReader(data), time.Minute)
	if err != nil || r.Code != 0 || r.Stdin == nil || r.Stdin.Bytes != int64(len(data)) || r.Stdin.Sha256 != hex.EncodeToString(want[:]) {
		t.Fatalf("early exit: %+v %v", r.Stdin, err)
	}
}
