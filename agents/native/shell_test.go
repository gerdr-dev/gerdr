package native

import (
	"bytes"
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

func TestPowerShellPreamble(t *testing.T) {
	for _, sh := range []string{`C:\Program Files\PowerShell\7\pwsh.exe`, `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, "powershell.exe"} {
		if !isPowerShell(sh) {
			t.Fatalf("%s is PowerShell", sh)
		}
	}
	if isPowerShell(`C:\Program Files\Git\bin\bash.exe`) {
		t.Fatal("bash is not PowerShell")
	}
	// on the command's first line, so PowerShell's error line numbers match the model's command
	if strings.Contains(psPreamble, "\n") {
		t.Fatal("preamble spans lines")
	}
}

func TestWinShellFromEnv(t *testing.T) {
	for sh, ok := range map[string]bool{
		`C:\Program Files\Git\bin\bash.exe`:                      true,
		`C:\msys64\usr\bin\sh.exe`:                               true,
		`C:\Program Files\PowerShell\7\pwsh.exe`:                 true,
		`C:\Windows\System32\cmd.exe`:                            false, // takes /c, not -c
		`C:\Windows\System32\CMD.EXE`:                            false,
		`C:\Users\me\AppData\Local\Microsoft\WindowsApps\nu.exe`: false,
	} {
		if winShellOK(sh) != ok {
			t.Errorf("winShellOK(%s) = %v", sh, !ok)
		}
	}
}

func TestCLIXML(t *testing.T) {
	// what Windows PowerShell 5.1 writes to a piped stderr for `Write-Warning hi; Get-Item nope`, progress included
	const doc = "#< CLIXML\r\n<Objs Version=\"1.1.0.1\" xmlns=\"http://schemas.microsoft.com/powershell/2004/04\">" +
		"<Obj S=\"progress\" RefId=\"0\"><TN RefId=\"0\"><T>System.Management.Automation.PSCustomObject</T><T>System.Object</T></TN><MS><I64 N=\"SourceId\">1</I64><PR N=\"Record\"><AV>Preparing modules for first use.</AV><AI>0</AI><Nil /><PI>-1</PI><PC>-1</PC><T>Completed</T><SR>-1</SR><SD> </SD></PR></MS></Obj>" +
		"<S S=\"warning\">hi</S>" +
		"<S S=\"Error\">Get-Item : Cannot find path 'C:\\x\\nope' because it does not exist._x000D__x000A_</S>" +
		"<S S=\"Error\">At line:1 char:1 &lt;a&gt; _x005F_x_x000D__x000A_</S>" +
		"</Objs>"
	var out bytes.Buffer
	w := &clixmlWriter{w: &out}
	_, _ = w.Write([]byte("before\r\n"))
	// split anywhere, the marker included
	for _, chunk := range []string{doc[:4], doc[4:60], doc[60:200], doc[200:]} {
		_, _ = w.Write([]byte(chunk))
	}
	_, _ = w.Write([]byte("\r\nafter #<"))
	w.flush()
	want := "before\r\nWARNING: hi\r\nGet-Item : Cannot find path 'C:\\x\\nope' because it does not exist.\r\nAt line:1 char:1 <a> _x\r\nafter #<"
	if out.String() != want {
		t.Fatalf("got  %q\nwant %q", out.String(), want)
	}
	// text that is not CLIXML passes through as it came
	out.Reset()
	w = &clixmlWriter{w: &out}
	_, _ = w.Write([]byte("plain <S S=\"Error\">x</S>\n"))
	w.flush()
	if out.String() != "plain <S S=\"Error\">x</S>\n" {
		t.Fatalf("plain text changed: %q", out.String())
	}
}
