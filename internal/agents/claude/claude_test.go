package claude

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/gerdr-dev/gerdr/internal/acpx"
)

func TestDescribeTool(t *testing.T) {
	ti := describeTool("Edit", map[string]any{"file_path": "/p/a.go", "old_string": "a", "new_string": "b"}, "/p")
	if ti.title != "Edit a.go" || ti.kind != acp.ToolKindEdit || len(ti.content) != 1 || ti.content[0].Diff == nil {
		t.Fatalf("edit: %+v", ti)
	}
	if ti := describeTool("Bash", map[string]any{"command": "go test ./..."}, ""); ti.title != "go test ./..." || ti.kind != acp.ToolKindExecute {
		t.Fatalf("bash: %+v", ti)
	}
	if ti := describeTool("mcp__github__create_issue", nil, ""); ti.title != "github · create_issue" {
		t.Fatalf("mcp: %+v", ti)
	}
}

func TestUserMessageUUID(t *testing.T) {
	m := userMessage([]acp.ContentBlock{acp.TextBlock("hi")}, "7b0e3f62-1111-4222-8333-944455556666", "now")
	if m["uuid"] != "7b0e3f62-1111-4222-8333-944455556666" || m["priority"] != "now" {
		t.Fatalf("%v", m)
	}
	// 不是 UUID 的 messageId 不能塞给 CLI（它要求 uuid 字段是 UUID）。
	if _, ok := userMessage(nil, "m1", "")["uuid"]; ok {
		t.Fatal("non-uuid id leaked")
	}
}

func TestSubReport(t *testing.T) {
	in := "  <tools-used-before-summary>Bash</tools-used-before-summary>\n  \n[Subagent hand-back] blah. The report follows:\n  SUBMARK\n  second line\nagentId: acb (use SendMessage)\n<usage>x</usage>"
	if got := subReport(in); got != "SUBMARK\nsecond line" {
		t.Fatalf("%q", got)
	}
	if got := subReport("plain"); got != "plain" {
		t.Fatalf("%q", got)
	}
}

func TestTitles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	_ = os.WriteFile(p, []byte(`{"type":"user","message":{"role":"user","content":"hi"}}
{"type":"custom-title","customTitle":"first","sessionId":"x"}
{"type":"ai-title","aiTitle":"auto 1","sessionId":"x"}
{"type":"custom-title","customTitle":"second","sessionId":"x"}
{"type":"ai-title","aiTitle":"auto 2","sessionId":"x"}
`), 0o644)
	if c, ai := titles(p); c != "second" || ai != "auto 2" {
		t.Fatalf("%q %q", c, ai)
	}
}

func TestAllowRules(t *testing.T) {
	cases := map[string][]string{
		`git push --force origin main`:         {"Bash(git push --force origin main)"},
		`echo "a && b" && touch x; ls | wc -l`: {`Bash(echo "a && b")`, "Bash(touch x)", "Bash(ls)", "Bash(wc -l)"},
		"cd d\ncd d\nmake":                     {"Bash(cd d)", "Bash(make)"},
		`echo $(rm -rf x) && ls`:               {"Bash(echo $(rm -rf x) && ls)"},
		`echo 'unterminated && x`:              {"Bash(echo 'unterminated && x)"},
		`printf a\|b`:                          {`Bash(printf a\|b)`},
	}
	for cmd, want := range cases {
		if got := allowRules("Bash", map[string]any{"command": cmd}); !slices.Equal(got, want) {
			t.Errorf("%q: got %q want %q", cmd, got, want)
		}
	}
	if got := allowRules("Write", map[string]any{"file_path": "/etc/x"}); !slices.Equal(got, []string{"Edit(//etc/x)"}) {
		t.Errorf("write: %q", got)
	}
	if got := allowRules("WebFetch", map[string]any{"url": "https://a.example.com/p?q"}); !slices.Equal(got, []string{"WebFetch(domain:a.example.com)"}) {
		t.Errorf("webfetch: %q", got)
	}
	if got := allowRules("Agent", map[string]any{}); got != nil {
		t.Errorf("agent: %q", got)
	}
}

func TestAddAllowRules(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".claude", "settings.local.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(`{"env":{"A":"1"},"permissions":{"allow":["Bash(ls)"],"deny":["Read(x)"]}}`), 0o644)
	if err := addAllowRules(dir, []string{"Bash(ls)", "Bash(make)"}); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Env         map[string]string `json:"env"`
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	b, _ := os.ReadFile(p)
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Env["A"] != "1" || !slices.Equal(doc.Permissions.Allow, []string{"Bash(ls)", "Bash(make)"}) || len(doc.Permissions.Deny) != 1 {
		t.Fatalf("%s", b)
	}
	// 坏的 JSON 不覆盖（那是用户的文件）。
	os.WriteFile(p, []byte(`{oops`), 0o644)
	if addAllowRules(dir, []string{"Bash(x)"}) == nil {
		t.Fatal("overwrote invalid settings")
	}
}

// 重放带截图的用户消息：文字和图都要发出来，而且是同一个 MessageId（界面按它拼回一条）。
// 原来只发文字，会话在别处接着聊过、整份重放一次之后截图就没了。
func TestReplayUserImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	line := `{"type":"user","uuid":"u1","message":{"role":"user","content":[` +
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}},` +
		`{"type":"text","text":"看这张图"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []acp.SessionUpdate
	if err := replayTranscript(path, func(u acp.SessionUpdate) { got = append(got, u) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("要 2 块（文字 + 图），拿到 %d", len(got))
	}
	txt, img := got[0].UserMessageChunk, got[1].UserMessageChunk
	if txt == nil || txt.Content.Text == nil || txt.Content.Text.Text != "看这张图" {
		t.Fatalf("第一块应该是文字：%+v", got[0])
	}
	if img == nil || img.Content.Image == nil || img.Content.Image.MimeType != "image/png" || img.Content.Image.Data != "iVBORw0KGgo=" {
		t.Fatalf("第二块应该是图：%+v", got[1])
	}
	if txt.MessageId == nil || img.MessageId == nil || *txt.MessageId != "u1" || *img.MessageId != "u1" {
		t.Fatalf("两块的 MessageId 都应该是 u1")
	}
}

// After an auto-compaction Claude Code writes the summary as a user message (isCompactSummary). Replay must not
// show it as something the person said (it did: a huge "This session is being continued…" bubble), nor count it as
// a message sent elsewhere; the compact_boundary before it becomes the same note the live stream shows.
func TestReplayCompaction(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	lines := `{"type":"user","uuid":"u1","message":{"role":"user","content":"hi"}}
{"type":"system","subtype":"compact_boundary","uuid":"b1","content":"Conversation compacted"}
{"type":"user","uuid":"u2","isCompactSummary":true,"isVisibleInTranscriptOnly":true,"message":{"role":"user","content":"This session is being continued from a previous conversation…"}}
`
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []acp.SessionUpdate
	if err := replayTranscript(p, func(u acp.SessionUpdate) { got = append(got, u) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].UserMessageChunk == nil || got[1].AgentThoughtChunk == nil {
		t.Fatalf("want the user message then the compaction note, got %+v", got)
	}
	if texts := userTextsFrom(p, 0); !slices.Equal(texts, []string{"hi"}) {
		t.Fatalf("userTextsFrom = %q", texts)
	}
}

// claude 自己补写的那几行不是人说的话：被打断的记号、后台任务的汇报。算进去的话追上就误报「在别处接着聊过」。
func TestUserTextsFromSkipsInterrupt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	lines := `{"type":"user","message":{"role":"user","content":"hi"}}
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}
{"type":"user","message":{"role":"user","content":"[Request interrupted by user]"}}
{"type":"user","message":{"role":"user","content":"<task-notification>\n<task-id>b1</task-id>\n<status>killed</status>\n</task-notification>"},"origin":{"kind":"task-notification"}}
`
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := userTextsFrom(p, 0); !slices.Equal(got, []string{"hi"}) {
		t.Fatalf("%q", got)
	}
}

// A steer that landed mid-turn is a queued_command attachment, found by source_uuid (our messageId);
// one that the CLI picked up after the turn ended is a plain user message, found by uuid.
func TestParentOfSteer(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	lines := `{"type":"user","uuid":"u1","parentUuid":null,"message":{"role":"user","content":"go"}}
{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1"}]}}
{"type":"user","uuid":"r1","parentUuid":"a1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1"}]}}
{"type":"attachment","uuid":"x1","parentUuid":"r1","attachment":{"type":"queued_command","prompt":"stop","source_uuid":"steer-1"}}
{"type":"system","uuid":"s1","parentUuid":"x1","subtype":"stop_hook_summary"}
{"type":"user","uuid":"steer-2","parentUuid":"s1","message":{"role":"user","content":"next"}}
`
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"steer-1": "r1", "steer-2": "s1"} {
		if got, found, err := parentOf(p, id); err != nil || !found || got != want {
			t.Fatalf("%s: %q %v %v", id, got, found, err)
		}
	}
}

// 回退之后还没说过话的会话重新载入：只重放原会话到分叉点为止。
func TestReplayTranscriptUntil(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	lines := `{"type":"user","uuid":"u1","message":{"role":"user","content":"one"}}
{"type":"user","uuid":"u2","message":{"role":"user","content":"two"}}
{"type":"user","uuid":"u3","message":{"role":"user","content":"three"}}
`
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	err := replayTranscriptUntil(p, "u2", func(u acp.SessionUpdate) {
		if u.UserMessageChunk != nil && u.UserMessageChunk.Content.Text != nil {
			got = append(got, u.UserMessageChunk.Content.Text.Text)
		}
	})
	if err != nil || !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("%v %q", err, got)
	}
}

// 回退出来、还没说过话的会话：agent 进程换了（gerdr 重启）也要照样分叉起，不能起成空会话（踩过：claude 说「这是第一条消息」）。
// 新会话的记录写出来以后才算分叉成了，改按 id --resume。
func TestPendingForkSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{opts: Options{Env: []string{"CLAUDE_CONFIG_DIR=" + dir}}, sessions: map[string]*session{}}
	const id, cwd = "11111111-2222-4333-8444-555555555555", "/p"
	meta := acpx.Meta(map[string]any{"fork": &acpx.PendingFork{From: "aaaaaaaa-0000-4000-8000-000000000000", At: "u9"}})
	// 走一趟 JSON，和真的从 hub 传过来一样
	b, _ := json.Marshal(meta)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	s := a.newSession(id, cwd, m)
	s.mu.Lock()
	fresh, from, at := s.pendingLocked()
	s.mu.Unlock()
	if fresh || from != "aaaaaaaa-0000-4000-8000-000000000000" || at != "u9" {
		t.Fatalf("pending: %v %q %q", fresh, from, at)
	}
	p := filepath.Join(projectDirFor(a.projectsDir(), cwd), id+".jsonl")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	fresh, from, _ = s.pendingLocked()
	s.mu.Unlock()
	if fresh || from != "" {
		t.Fatalf("记录有了还当成待分叉：%v %q", fresh, from)
	}
}

// initialize 的命令列表不分 skill：不是 builtin 的算 skill，builtin 里只有 init 的 skills 报了的才算（/code-review 这种）
func TestACPCommandsSkill(t *testing.T) {
	var list []cliCommand
	_ = json.Unmarshal([]byte(`[{"name":"lark-doc","description":"x (user)"},{"name":"code-review","builtin":true},{"name":"compact","argumentHint":"<x>","builtin":true}]`), &list)
	a := &Agent{}
	skill := func(cmds []acp.AvailableCommand) (out []string) {
		for _, c := range cmds {
			if c.Meta["skill"] == true {
				out = append(out, c.Name)
			}
		}
		return
	}
	if got := skill(a.acpCommands(list)); !slices.Equal(got, []string{"lark-doc"}) {
		t.Fatalf("没有 init 时：%v", got)
	}
	a.skills = map[string]bool{"lark-doc": true, "code-review": true}
	cmds := a.acpCommands(list)
	if got := skill(cmds); !slices.Equal(got, []string{"lark-doc", "code-review"}) {
		t.Fatalf("有 init 之后：%v", got)
	}
	if cmds[2].Input == nil || cmds[2].Input.Unstructured.Hint != "<x>" {
		t.Fatalf("参数提示丢了：%+v", cmds[2])
	}
}

// --resume 一个有没跑完的后台任务的会话：CLI 一起来就自己跑起来（running 到时这一轮还没挂上），我们这句在同一段 running 里
// 跑完才 idle。只认 running 的话这一轮永远等不到结束（实测卡死）；认 command_lifecycle started 才对。
func TestTurnEndsWhenQueuedIntoAutoRun(t *testing.T) {
	a := &Agent{opts: Options{Env: []string{"CLAUDE_CONFIG_DIR=" + t.TempDir()}}, sessions: map[string]*session{}, log: slog.New(slog.DiscardHandler)}
	s := a.newSession("11111111-2222-4333-8444-555555555555", "/p", nil)
	s.emitHook = func(acp.SessionUpdate) {}
	line := func(js string) {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(js), &m); err != nil {
			t.Fatal(err)
		}
		s.onLine(nil, m, []byte(js))
	}
	line(`{"type":"system","subtype":"task_notification","task_id":"b1","status":"stopped","summary":"didn't finish"}`)
	line(`{"type":"system","subtype":"session_state_changed","state":"running"}`) // 这时 turn 还没挂上
	const msg = "aaaaaaaa-1111-4222-8333-444444444444"
	turn := &turnState{done: make(chan turnResult, 1), id: msg}
	s.mu.Lock()
	s.turn = turn
	s.mu.Unlock()
	line(`{"type":"command_lifecycle","command_uuid":"` + msg + `","state":"queued"}`)
	line(`{"type":"result","subtype":"success","is_error":false,"stop_reason":null}`) // 它自己那一轮的空 result
	line(`{"type":"command_lifecycle","command_uuid":"` + msg + `","state":"started"}`)
	line(`{"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","result":"OK"}`)
	line(`{"type":"command_lifecycle","command_uuid":"` + msg + `","state":"completed"}`)
	line(`{"type":"system","subtype":"session_state_changed","state":"idle"}`)
	select {
	case r := <-turn.done:
		if r.err != nil || r.stop != acp.StopReasonEndTurn {
			t.Fatalf("%+v", r)
		}
	default:
		t.Fatal("idle 之后这一轮没结束")
	}
}

// A failed API call: the CLI sends a synthetic assistant message carrying the error, then an is_error result with the same
// text. Only the turn's error should carry it; pushing the message too printed the error once per retry attempt.
func TestAPIErrorMessageNotShownTwice(t *testing.T) {
	a := &Agent{opts: Options{Env: []string{"CLAUDE_CONFIG_DIR=" + t.TempDir()}}, sessions: map[string]*session{}, log: slog.New(slog.DiscardHandler)}
	s := a.newSession("11111111-2222-4333-8444-555555555555", "/p", nil)
	var got []acp.SessionUpdate
	s.emitHook = func(u acp.SessionUpdate) { got = append(got, u) }
	line := func(js string) {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(js), &m); err != nil {
			t.Fatal(err)
		}
		s.onLine(nil, m, []byte(js))
	}
	turn := &turnState{done: make(chan turnResult, 1)}
	s.mu.Lock()
	s.turn = turn
	s.mu.Unlock()
	const text = "Failed to authenticate. API Error: 403 Request not allowed"
	line(`{"type":"assistant","error":"authentication_failed","parent_tool_use_id":null,"message":{"id":"m1","content":[{"type":"text","text":"` + text + `"}]}}`)
	line(`{"type":"result","subtype":"success","is_error":true,"result":"` + text + `"}`)
	for _, u := range got {
		if u.AgentMessageChunk != nil {
			t.Fatalf("error pushed as a reply: %+v", u.AgentMessageChunk)
		}
	}
	s.mu.Lock()
	errText := turn.errText
	s.mu.Unlock()
	if !strings.Contains(errText, "Request not allowed") {
		t.Fatalf("turn error lost: %q", errText)
	}
}
