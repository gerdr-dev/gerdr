package work

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func textResult(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	out, _ := json.Marshal(map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}})
	return out
}

func TestOKRSummary(t *testing.T) {
	now := time.Now()
	mon := time.Date(now.Year(), now.Month(), now.Day()-(int(now.Weekday())+6)%7, 0, 0, 0, 0, time.Local)
	thisWeek := mon.Format("2006") + "W" + mon.Format("0102") + "-" + mon.AddDate(0, 0, 6).Format("0102")
	calls := map[string]int{}
	r := New(t.TempDir(), func(ctx context.Context, server, tool string, args map[string]any) (json.RawMessage, error) {
		calls[tool]++
		if server != "okr" {
			t.Fatalf("server %q", server)
		}
		switch tool {
		case "get_current_user":
			return textResult(map[string]any{"id": 7, "name": "Me", "first_name": "Me"}), nil
		case "list_quarters":
			return textResult(map[string]any{"quarters": []any{
				map[string]any{"name": "2026Q4", "is_active": true, "start_date": "2026-10-01", "end_date": "2026-12-31"},
				map[string]any{"name": "2026Q3", "is_active": false},
			}}), nil
		case "list_okr_objects":
			return textResult(map[string]any{"objects": []any{
				map[string]any{"id": 1, "name": "O1", "label": "O", "progress_percent": 30},
				map[string]any{"id": 2, "name": "KR1", "label": "KR", "parent_id": 1, "progress_percent": 30},
				map[string]any{"id": 3, "name": "KR2", "label": "KR", "parent_id": 1},
				map[string]any{"id": 4, "name": "Aligned KR", "label": "KR", "parent_id": 99, "linked_object": map[string]any{"id": 99, "name": "Boss O", "owner_name": "Boss"}},
			}}), nil
		case "list_progress_reports":
			if args["owner_id"] != float64(7) {
				t.Fatalf("owner_id %v", args["owner_id"])
			}
			return textResult(map[string]any{"reports": []any{
				map[string]any{"id": 10, "object_id": 2, "object_name": "KR1", "week": "2026W1019-1025", "content": "old", "create_time": "2026-10-23T11:06:03Z"},
				map[string]any{"id": 11, "object_id": 2, "object_name": "KR1", "week": thisWeek, "content": "new", "create_time": "2026-10-30T11:06:03Z"},
			}}), nil
		}
		t.Fatalf("unexpected tool %s", tool)
		return nil, nil
	})
	res, err := r.Run(context.Background(), "", "okr.summary", nil)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["submittedThisWeek"] != true {
		t.Fatalf("submittedThisWeek = %v", m["submittedThisWeek"])
	}
	if w := m["week"].(map[string]any)["key"]; w != thisWeek {
		t.Fatalf("week %v, want %s", w, thisWeek)
	}
	if lr := m["lastReport"].(map[string]any); lr["time"] != "2026-10-30T11:06:03Z" {
		t.Fatalf("lastReport %v", lr)
	}
	objs := m["objectives"].([]any)
	if len(objs) != 2 {
		t.Fatalf("objectives %v", objs)
	}
	krs := objs[0].(map[string]any)["krs"].([]any)
	kr1 := krs[0].(map[string]any)
	if kr1["reportedThisWeek"] != true || kr1["lastReport"].(map[string]any)["content"] != "new" {
		t.Fatalf("kr1 %v", kr1)
	}
	if krs[1].(map[string]any)["lastReport"] != nil {
		t.Fatalf("kr2 should have no report")
	}
	if !strings.Contains(objs[1].(map[string]any)["name"].(string), "Boss O") {
		t.Fatalf("aligned group %v", objs[1])
	}
}

func TestErrorsAndInstall(t *testing.T) {
	dir := t.TempDir()
	r := New(dir, func(ctx context.Context, server, tool string, args map[string]any) (json.RawMessage, error) {
		return json.RawMessage(`{"content":[{"type":"text","text":"denied"}],"isError":true}`), nil
	})
	if _, err := r.Run(context.Background(), "", "okr.summary", nil); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("want the MCP error, got %v", err)
	}
	if _, err := r.Run(context.Background(), "", "okr.nope", nil); err == nil {
		t.Fatal("missing export should fail")
	}
	if _, err := r.Run(context.Background(), "", "../x.y", nil); err == nil {
		t.Fatal("bad name should fail")
	}
	// A file the user edited survives the next install (a new process).
	p := filepath.Join(dir, "work", "local", "okr.ts")
	os.WriteFile(p, []byte("export function summary() { return 1 }"), 0o644)
	r = New(dir, r.MCP)
	if res, err := r.Run(context.Background(), "", "okr.summary", nil); err != nil || res != float64(1) {
		t.Fatalf("edited file got replaced: %v %v", res, err)
	}
}

func TestProjects(t *testing.T) {
	root := t.TempDir()
	r := New(root, nil)
	for _, c := range []struct{ project, dir string }{
		{"", "work"}, {"default", "work"}, {"s3k2", "projects/s3k2/work"}, {"客户 A", "projects/客户 A/work"},
	} {
		d, err := r.Dir(c.project)
		if err != nil || d != filepath.Join(root, c.dir) {
			t.Fatalf("%q: %s %v", c.project, d, err)
		}
		want := c.project
		if want == "" {
			want = DefaultProject
		}
		if got := r.ProjectOf(filepath.Join(d, "ui", "src")); got != want {
			t.Fatalf("ProjectOf(%s) = %s", d, got)
		}
	}
	for _, bad := range []string{"../x", "a/b", ".hidden", "v1.2", " pad", "a\\b"} {
		if _, err := r.Dir(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	d, err := r.Ensure("p1")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"CLAUDE.md", "ui/src/layout.tsx", "ui/src/pages/index.tsx", "ui/package.json"} {
		if _, err := os.Stat(filepath.Join(d, f)); err != nil {
			t.Fatal(err)
		}
	}
	// The OKR page is the default project's only: a new project starts with the blank home page.
	if _, err := os.Stat(filepath.Join(d, "local", "okr.ts")); err == nil {
		t.Fatal("okr.ts installed into a non-default project")
	}
	def, _ := r.Ensure("")
	if b, _ := os.ReadFile(filepath.Join(def, "ui/src/pages/index.tsx")); !strings.Contains(string(b), "okr.summary") {
		t.Fatal("default project should get the OKR page")
	}
}

// A project that got the OKR files from an older gerdr loses them on upgrade unless they were edited.
func TestInstallRemovesStale(t *testing.T) {
	root := t.TempDir()
	r := New(root, nil)
	d, _ := r.Dir("p")
	okr := bundled(true)
	marks := map[string]string{}
	for rel, b := range okr {
		p := filepath.Join(d, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, b, 0o644)
		marks[rel] = hash(b)
	}
	os.WriteFile(filepath.Join(d, "local", "okr.ts"), []byte("// mine now"), 0o644) // edited: stays
	mb, _ := json.Marshal(marks)
	os.WriteFile(filepath.Join(d, ".builtin.json"), mb, 0o644)
	if _, err := r.Ensure("p"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(d, "ui/src/pages/index.tsx")); strings.Contains(string(b), "okr.summary") {
		t.Fatal("untouched OKR page should become the home page")
	}
	if b, _ := os.ReadFile(filepath.Join(d, "local", "okr.ts")); string(b) != "// mine now" {
		t.Fatal("edited file removed")
	}
}
