package native

import (
	"testing"

	"github.com/google/uuid"
)

// A CRLF file with a BOM: edit matches the model's LF old_string, write keeps the old file's style.
func TestEditWriteKeepCRLFAndBOM(t *testing.T) {
	e := setup(t, func(n int, msgs []any) reply {
		switch n {
		case 0:
			return reply{calls: [][2]string{{"read", `{"path":"/store/a.txt"}`}, {"read", `{"path":"/store/b.txt"}`}}}
		case 1:
			return reply{calls: [][2]string{
				{"edit", `{"path":"/store/a.txt","old_string":"two\nthree","new_string":"2\n3\n3.5"}`},
				{"write", `{"path":"/store/b.txt","content":"x\ny\n"}`},
			}}
		}
		return reply{text: "done"}
	})
	fs := &memFiles{root: "/store", data: map[string]string{
		"/store/a.txt": "\ufeffone\r\ntwo\r\nthree\r\n",
		"/store/b.txt": "old\r\n",
	}, ver: map[string]int{}}
	e.ag.opt.Files = fs
	sid := e.newSession(modeBypass)
	if _, err := e.prompt(sid, uuid.NewString(), "go"); err != nil {
		t.Fatal(err)
	}
	if got, want := fs.data["/store/a.txt"], "\ufeffone\r\n2\r\n3\r\n3.5\r\n"; got != want {
		t.Errorf("edit: %q, want %q", got, want)
	}
	if got, want := fs.data["/store/b.txt"], "x\r\ny\r\n"; got != want {
		t.Errorf("write: %q, want %q", got, want)
	}
}

func TestLineEndHelpers(t *testing.T) {
	if !isCRLF("a\r\nb\n") || isCRLF("a\nb\r\n") || isCRLF("no newline") {
		t.Error("isCRLF")
	}
	if got := likeOld("a\nb\n", "x\ny"); got != "x\ny" {
		t.Errorf("LF file stays LF: %q", got)
	}
	if got := likeOld("a\r\n", "x\r\ny\n"); got != "x\r\ny\n" {
		t.Errorf("content with its own CRLF is left alone: %q", got)
	}
	if got := likeOld("\ufeffa", "\ufeffx"); got != "\ufeffx" {
		t.Errorf("BOM not doubled: %q", got)
	}
}

func TestGlobMatchSlashes(t *testing.T) {
	for _, c := range []struct {
		pat, p string
		ok     bool
	}{
		{"*.ts", "src/a/b.ts", true},
		{"src/**/*.ts", "src/a/b.ts", true},
		{"src/*.ts", "src/a/b.ts", false}, // "*" must not cross "/"
		{`src\**\*.ts`, "src/a/b.ts", false},
	} {
		if got := globMatch(c.pat, c.p); got != c.ok {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pat, c.p, got, c.ok)
		}
	}
}
