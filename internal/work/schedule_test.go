package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeSchedule(t *testing.T, dir, id, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, SchedulesDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SchedulesDir, id+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Messages are zh under go test (internal/i18n), so the cases match the zh text.
func TestParseSchedules(t *testing.T) {
	for _, c := range []struct{ body, err string }{
		{`{"fn":"w.tick","every":"5m"}`, ""},
		{`{"prompt":"sum up","at":"09:00, 16:30"}`, ""},
		{`{"fn":"w.tick","every":"1d"}`, ""},
		{`{"fn":"w.tick","prompt":"x","every":"5m"}`, "只写一个"},
		{`{"every":"5m"}`, "只写一个"},
		{`{"fn":"tick","every":"5m"}`, "文件.函数"},
		{`{"fn":"w.tick","every":"1m"}`, "最短"},
		{`{"fn":"w.tick","every":"soon"}`, "不是时长"},
		{`{"fn":"w.tick","at":"25:00"}`, "HH:MM"},
		{`{"fn":"w.tick"}`, "要写 every"},
		{`{"fn":`, "格式不对"},
	} {
		dir := t.TempDir()
		writeSchedule(t, dir, "a", c.body)
		_, err := ParseSchedules(dir)
		if c.err == "" && err != nil || c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)) {
			t.Errorf("%s: err %v, want %q", c.body, err, c.err)
		}
	}
}

func TestScheduleNext(t *testing.T) {
	loc := time.Local
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, loc)
	every := Schedule{Fn: "w.tick", Every: "30m"}
	if err := every.check(); err != nil {
		t.Fatal(err)
	}
	if got := every.next(time.Time{}, now); !got.Equal(now) {
		t.Errorf("never ran: next %v, want now", got)
	}
	if got := every.next(now.Add(-10*time.Minute), now); !got.Equal(now.Add(20 * time.Minute)) {
		t.Errorf("ran 10m ago: next %v", got)
	}
	// Asleep for hours: caught up once, right away (not once per missed interval)
	if got := every.next(now.Add(-5*time.Hour), now); got.After(now) {
		t.Errorf("long missed: next %v should be due now", got)
	}

	at := Schedule{Fn: "w.tick", At: "09:00,16:00"}
	if err := at.check(); err != nil {
		t.Fatal(err)
	}
	// 09:00 passed today and hasn't run: due now
	if got := at.next(time.Time{}, now); !got.Equal(time.Date(2026, 11, 3, 9, 0, 0, 0, loc)) {
		t.Errorf("missed 09:00: next %v", got)
	}
	// Ran at 09:00: next is 16:00
	if got := at.next(time.Date(2026, 11, 3, 9, 0, 5, 0, loc), now); !got.Equal(time.Date(2026, 11, 3, 16, 0, 0, 0, loc)) {
		t.Errorf("after 09:00: next %v", got)
	}
	// Late at night after both ran: tomorrow 09:00, yesterday's aren't caught up
	late := time.Date(2026, 11, 3, 23, 0, 0, 0, loc)
	if got := at.next(time.Date(2026, 11, 3, 16, 0, 1, 0, loc), late); !got.Equal(time.Date(2026, 11, 4, 9, 0, 0, 0, loc)) {
		t.Errorf("late: next %v", got)
	}
}

// A due fn schedule runs with { scheduled: true } plus its input, the outcome is kept across a new Runner, a paused
// one doesn't run, and a schedule isn't started twice while running.
func TestScheduleRuns(t *testing.T) {
	r, dir := newProject(t, map[string]string{
		"w.ts": `export async function tick(input: any, ctx: any) { ctx.fs.write("data/seen.json", JSON.stringify(input)); return { n: input.n } }`,
	})
	writeSchedule(t, dir, "watch", `{"fn":"w.tick","every":"5m","input":{"n":3}}`)
	writeSchedule(t, dir, "off", `{"fn":"w.tick","every":"5m"}`)
	if err := r.PauseSchedule("", "off", true); err != nil {
		t.Fatal(err)
	}
	r.tickSchedules(time.Now())
	if r.startScheduled(DefaultProject, Schedule{ID: "watch", Fn: "w.tick"}) {
		t.Error("started a second run while the first is going")
	}
	wait := func(id string) ScheduleInfo {
		t.Helper()
		for i := 0; i < 200; i++ {
			l, err := r.Schedules("")
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range l {
				if s.ID == id && s.Last != nil && !s.Running {
					return s
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("%s never finished", id)
		return ScheduleInfo{}
	}
	got := wait("watch")
	if !got.Last.OK || got.Last.Result != `{"n":3}` || got.Last.RunID == "" {
		t.Errorf("last run %+v", got.Last)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "data", "seen.json"))
	if !strings.Contains(string(b), `"scheduled":true`) || !strings.Contains(string(b), `"n":3`) {
		t.Errorf("input was %s", b)
	}
	// State survives a restart (a new Runner on the same root); the paused one never ran
	r2 := New(r.Root, nil)
	l, err := r2.Schedules("")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range l {
		switch s.ID {
		case "watch":
			if s.Last == nil || !s.Last.OK || s.Next == nil || s.Next.Before(time.Now().Add(4*time.Minute)) {
				t.Errorf("watch after restart: %+v next %v", s.Last, s.Next)
			}
		case "off":
			if !s.Paused || s.Last != nil || s.Next != nil {
				t.Errorf("paused one: %+v", s)
			}
		}
	}
}
