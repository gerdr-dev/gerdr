package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ev "github.com/gerdr-dev/gerdr/internal/events"
)

func writeTrigger(t *testing.T, dir, id, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, TriggersDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, TriggersDir, id+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseTriggers(t *testing.T) {
	for _, c := range []struct{ body, err string }{
		{`{"on":"gitlab/mr.*","fn":"w.on"}`, ""},
		{`{"on":"mr.note","key":"g/*","prompt":"look"}`, ""},
		{`{"on":"gitlab/mr.*","fn":"w.on","prompt":"x"}`, "有且只有一个"},
		{`{"fn":"w.on"}`, "要写 on"},
		{`{"on":"a/b/c","fn":"w.on"}`, "one /"},
		{`{"on":`, "格式不对"},
	} {
		dir := t.TempDir()
		writeTrigger(t, dir, "a", c.body)
		_, err := ParseTriggers(dir)
		if c.err == "" && err != nil || c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)) {
			t.Errorf("%s: err %v, want %q", c.body, err, c.err)
		}
	}
}

// An event reaches a matching trigger's function with { event, trigger }; one emitted from inside a trigger run is
// one level deeper, and a trigger feeding itself stops after MaxDepth instead of looping forever.
func TestTriggersRunAndStopLoops(t *testing.T) {
	r, dir := newProject(t, map[string]string{
		"w.ts": `export async function on(input: any, ctx: any) {
  ctx.fs.write("data/last.json", JSON.stringify(input));
  if (input.event.kind === "mr.note") ctx.events.emit({ source: "gitlab", kind: "mr.echo", key: input.event.key });
  return input.event.depth ?? 0;
}`,
	})
	bus, err := ev.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r.Events = bus
	bus.OnEvent(r.DispatchEvent)
	writeTrigger(t, dir, "mr", `{"on":"gitlab/mr.*","key":"g/r!*","fn":"w.on"}`)
	writeTrigger(t, dir, "other", `{"on":"ci/*","fn":"w.on"}`)
	if _, _, err := bus.Emit(ev.Event{ID: "n1", Source: "gitlab", Kind: "mr.note", Key: "g/r!1", Title: "a note"}); err != nil {
		t.Fatal(err)
	}
	// mr.note runs (depth 0) and emits mr.echo (depth 1), which runs and emits nothing (only notes echo)
	var mr TriggerInfo
	for i := 0; i < 300; i++ {
		l, err := r.Triggers("")
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range l {
			if x.ID == "mr" {
				mr = x
			}
			if x.ID == "other" && x.Runs != 0 {
				t.Fatal("ci/* should not have run")
			}
		}
		if mr.Runs == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if mr.Runs != 2 || mr.Last == nil || !mr.Last.OK || mr.Last.Result != "1" {
		t.Fatalf("mr trigger: runs %d last %+v", mr.Runs, mr.Last)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "data", "last.json"))
	if !strings.Contains(string(b), `"trigger":"mr"`) || !strings.Contains(string(b), `"kind":"mr.echo"`) {
		t.Errorf("input was %s", b)
	}

	// A trigger that re-emits what it gets: bounded by depth
	writeTrigger(t, dir, "loop", `{"on":"loop/*","fn":"w.loop"}`)
	os.WriteFile(filepath.Join(dir, "local", "w.ts"), []byte(`export async function on() {}
export async function loop(input: any, ctx: any) { ctx.events.emit({ source: "loop", kind: "again" }) }`), 0o644)
	bus.Emit(ev.Event{Source: "loop", Kind: "start"})
	time.Sleep(time.Second)
	l, _ := r.Triggers("")
	for _, x := range l {
		if x.ID == "loop" && x.Runs != ev.MaxDepth+1 {
			t.Errorf("loop trigger ran %d times, want %d", x.Runs, ev.MaxDepth+1)
		}
	}
}

// ctx.events.reply answers through the event's own address: its reply kind and data, plus the text.
func TestEventsReplyAndCatalog(t *testing.T) {
	r, _ := newProject(t, map[string]string{
		"w.ts": `export async function answer(input: any, ctx: any) {
  const e = ctx.events.list({ on: "slack/dm" })[0];
  ctx.events.reply(e, "got it", { extra: 1 });
  try { ctx.events.reply({ source: "x", kind: "y" }, "no"); return "no throw" } catch (err) {}
  return ctx.events.catalog().map((c: any) => c.on);
}`,
	})
	bus, _ := ev.Open(t.TempDir())
	bus.Declared = func() []ev.Source { return []ev.Source{{On: "slack/dm", CanReply: true}} }
	r.Events = bus
	bus.Emit(ev.Event{ID: "m1", Source: "slack", Kind: "dm", Key: "D1", Reply: &ev.Reply{Kind: "slack.send", Data: []byte(`{"channel":"D1","threadTs":"1.1"}`)}})
	res, _, err := run(t, r, "w.answer", nil)
	if err != nil {
		t.Fatal(err)
	}
	if l, ok := res.([]any); !ok || len(l) != 1 || l[0] != "slack/dm" {
		t.Fatalf("catalog from a function: %#v", res)
	}
	got, _ := bus.List(ev.Query{On: "*/slack.send"})
	if len(got) != 1 {
		t.Fatalf("replies: %+v", got)
	}
	x := got[0]
	if x.Source != "default" || x.Key != "D1" || string(x.Data) != `{"channel":"D1","extra":1,"text":"got it","threadTs":"1.1"}` {
		t.Errorf("reply %+v data %s", x, x.Data)
	}
}
