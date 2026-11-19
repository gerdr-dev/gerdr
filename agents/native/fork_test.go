package native

import (
	"testing"

	"github.com/gerdr-dev/gerdr/agents/native/llm"
)

// A fork's record has the original's messages and settings, without its /rename title.
func TestStoreFork(t *testing.T) {
	st := store{dir: t.TempDir()}
	src := []entry{
		{T: "meta", Meta: &sessionMeta{Cwd: "/tmp", Model: "m", Title: "原来的名字"}},
		{T: "msg", ID: "u1", Msg: &llm.Message{Role: "user"}},
		{T: "msg", ID: "a1", Msg: &llm.Message{Role: "assistant"}},
	}
	if err := st.rewrite("old", src); err != nil {
		t.Fatal(err)
	}
	id, err := st.fork("old")
	if err != nil {
		t.Fatal(err)
	}
	es, err := st.load(id)
	if err != nil || len(es) != 3 || es[1].ID != "u1" || es[2].ID != "a1" {
		t.Fatalf("copy: %v %+v", err, es)
	}
	if m := lastMeta(es); m.Title != "" || m.Model != "m" || m.Cwd != "/tmp" {
		t.Fatalf("meta: %+v", m)
	}
	if old, _ := st.load("old"); lastMeta(old).Title != "原来的名字" {
		t.Fatal("the original changed")
	}
	if _, err := st.fork("missing"); err == nil {
		t.Fatal("forked a session that was never written")
	}
}
