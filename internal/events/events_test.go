package events

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMatch(t *testing.T) {
	e := Event{Source: "gitlab", Kind: "mr.note", Key: "group/repo!34"}
	for _, c := range []struct {
		pat  string
		want bool
	}{
		{"", true}, {"*", true}, {"gitlab/mr.note", true}, {"gitlab/mr.*", true}, {"*/mr.note", true},
		{"mr.note", true}, {"mr.*", true}, {"github/mr.note", false}, {"gitlab/pipeline.*", false}, {"pipeline.failed", false},
	} {
		if got := Match(c.pat, e); got != c.want {
			t.Errorf("Match(%q) = %v, want %v", c.pat, got, c.want)
		}
	}
	if !MatchKey("group/*", e.Key) || !MatchKey("*!34", e.Key) || MatchKey("other/*", e.Key) || !MatchKey("", e.Key) {
		t.Error("MatchKey: * should cross slashes")
	}
	if CheckPattern("a/b/c") == nil || CheckPattern("/x") == nil || CheckPattern("gitlab/*") != nil || CheckPattern("mr.note") != nil {
		t.Error("CheckPattern")
	}
}

func TestEmitDedupListAndReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []Event
	s.OnEvent(func(e Event) { got = append(got, e) })
	e := Event{ID: "gitlab:note:1", Source: "gitlab", Kind: "mr.note", Key: "g/r!1", Data: json.RawMessage(`{"n":1}`)}
	if _, fresh, err := s.Emit(e); err != nil || !fresh {
		t.Fatalf("first emit: fresh=%v err=%v", fresh, err)
	}
	if _, fresh, _ := s.Emit(e); fresh {
		t.Fatal("same id twice should be dropped")
	}
	if _, fresh, _ := s.Emit(Event{Source: "ci", Kind: "pipeline.failed"}); !fresh {
		t.Fatal("no id: always fresh")
	}
	if len(got) != 2 {
		t.Fatalf("listeners got %d events, want 2", len(got))
	}
	if _, _, err := s.Emit(Event{Source: "a/b", Kind: "x"}); err == nil {
		t.Fatal("a source with / should be refused")
	}
	l, _ := s.List(Query{On: "gitlab/*"})
	if len(l) != 1 || l[0].ID != "gitlab:note:1" {
		t.Fatalf("List: %+v", l)
	}
	// A restarted Gerdr still knows the id: a poll after the restart doesn't emit it again
	s2, _ := Open(dir)
	if _, fresh, _ := s2.Emit(e); fresh {
		t.Fatal("seen ids should survive a reopen")
	}
	if l, _ := s2.List(Query{Since: time.Now().Add(-time.Hour).UnixMilli(), Limit: 1}); len(l) != 1 || l[0].Source != "ci" {
		t.Fatalf("Limit keeps the newest: %+v", l)
	}
}

func TestWatches(t *testing.T) {
	s, _ := Open(t.TempDir())
	if _, err := s.AddWatch(Watch{On: "gitlab/mr.note", Key: "g/r!1"}); err == nil {
		t.Fatal("a watch needs a session")
	}
	a, _ := s.AddWatch(Watch{On: "gitlab/mr.note", Key: "g/r!1", Session: "s1"})
	s.AddWatch(Watch{On: "ci/pipeline.*", Session: "s2", Once: true})
	if m := s.Matching(Event{Source: "gitlab", Kind: "mr.note", Key: "g/r!2"}); len(m) != 0 {
		t.Fatal("other key should not match")
	}
	if m := s.Matching(Event{Source: "gitlab", Kind: "mr.note", Key: "g/r!1"}); len(m) != 1 || m[0].ID != a.ID {
		t.Fatalf("matching: %+v", m)
	}
	ev := Event{Source: "ci", Kind: "pipeline.done"}
	if m := s.Matching(ev); len(m) != 1 {
		t.Fatal("once watch should match the first time")
	}
	if m := s.Matching(ev); len(m) != 0 {
		t.Fatal("once watch should be gone after delivering")
	}
	s.DropSession("s1")
	if len(s.Watches("")) != 0 {
		t.Fatalf("left: %+v", s.Watches(""))
	}
}

// The catalog lists declared sources first (with when one was last seen), then what came lately, leaving out
// answers (a kind some event names as its Reply).
func TestCatalog(t *testing.T) {
	s, _ := Open(t.TempDir())
	s.Declared = func() []Source {
		return []Source{{On: "slack/dm", Title: "Slack DM", Addon: "slack", CanReply: true}, {On: "feishu/message"}}
	}
	s.Emit(Event{ID: "a", Source: "slack", Kind: "dm", Reply: &Reply{Kind: "slack.send", Data: json.RawMessage(`{"channel":"D1"}`)}})
	s.Emit(Event{Source: "board", Kind: "slack.send"})
	s.Emit(Event{Source: "gitlab", Kind: "mr.note"})
	c := s.Catalog()
	var ons []string
	for _, x := range c {
		ons = append(ons, x.On)
	}
	if len(c) != 3 || c[0].On != "slack/dm" || c[0].Seen == 0 || c[1].On != "feishu/message" || c[1].Seen != 0 || c[2].On != "gitlab/mr.note" {
		t.Fatalf("catalog: %v %+v", ons, c)
	}
	if _, _, err := s.Emit(Event{Source: "x", Kind: "y", Reply: &Reply{Kind: "bad kind"}}); err == nil {
		t.Fatal("a reply kind with a space should be refused")
	}
}
