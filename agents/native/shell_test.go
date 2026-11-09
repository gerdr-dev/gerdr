package native

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestPowerShellEncodedCommand(t *testing.T) {
	cmd := `Write-Output "a 'b' \"c\" 中文 😀"`
	b, err := base64.StdEncoding.DecodeString(encodePS(cmd))
	if err != nil || len(b)%2 != 0 {
		t.Fatalf("not base64 of UTF-16: %v", err)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8 // little-endian
	}
	if got := string(utf16.Decode(u)); got != cmd {
		t.Fatalf("round trip = %q, want %q", got, cmd)
	}
}

func TestGoGrep(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, s string) {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package a\nfunc Hello() {}\nfunc hello2() {}\n")
	write("sub/b.ts", "export const hello = 1\n")
	write("node_modules/x/c.go", "func Hello() {}\n")
	write("bin.dat", "Hello\x00binary")

	files, err := goGrep("hello", dir, "", "files", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(files, "a.go") || !strings.Contains(files, "b.ts") || strings.Contains(files, "node_modules") || strings.Contains(files, "bin.dat") {
		t.Fatalf("files mode: %q", files)
	}
	content, _ := goGrep(`func \w+`, dir, "*.go", "content", false)
	if !strings.Contains(content, "a.go:2:func Hello() {}") || !strings.Contains(content, "a.go:3:") || strings.Contains(content, "b.ts") {
		t.Fatalf("content mode: %q", content)
	}
	count, _ := goGrep("(?i)hello", dir, "", "count", false)
	if !strings.Contains(count, "a.go:2") {
		t.Fatalf("count mode: %q", count)
	}
	if _, err := goGrep("(", dir, "", "files", false); err == nil {
		t.Fatal("bad pattern should error")
	}
}
