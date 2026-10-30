package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"
)

// claude 把会话记录写在 ~/.claude/projects/<cwd 变形>/<sessionId>.jsonl，每行一条，
// 靠 uuid / parentUuid 串成一条链。

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

// 目录名变形规则是 CLI 的内部细节（目前是「非字母数字一律换成 -」），只拿来当快路径；
// 对不上就按 session id 全局找 —— id 是 UUID，全机唯一。
// root 是 projects 目录（Agent.projectsDir，每个 claude 实例各有各的）。
func projectDirFor(root, cwd string) string {
	return filepath.Join(root, nonAlnum.ReplaceAllString(cwd, "-"))
}

var idRe = regexp.MustCompile(`^[0-9a-fA-F-]{8,64}$`)

func findTranscript(root, id, cwd string) (string, error) {
	if !idRe.MatchString(id) {
		return "", errors.New("invalid session id")
	}
	if cwd != "" {
		p := filepath.Join(projectDirFor(root, cwd), id+".jsonl")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	matches, _ := filepath.Glob(filepath.Join(root, "*", id+".jsonl"))
	if len(matches) > 0 {
		return matches[0], nil
	}
	return "", acp.NewInvalidParams(map[string]any{"sessionId": id, "error": "transcript not found"})
}

type entry struct {
	Type        string          `json:"type"`
	UUID        string          `json:"uuid"`
	ParentUUID  *string         `json:"parentUuid"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	Cwd         string          `json:"cwd"`
	Timestamp   string          `json:"timestamp"`
	Summary     string          `json:"summary"`
	CustomTitle string          `json:"customTitle"` // type=custom-title：/rename 改的名字
	AITitle     string          `json:"aiTitle"`     // type=ai-title：claude 自己起的标题（会随对话更新，后写的算）
	Message     json.RawMessage `json:"message"`
	ToolResult  json.RawMessage `json:"toolUseResult"`
	// type=attachment: a steer that landed mid-turn is stored as a queued_command attachment, not a
	// user message; SourceUUID is the uuid we sent it with (gerdr's messageId).
	Attachment *struct {
		Type       string `json:"type"`
		SourceUUID string `json:"source_uuid"`
	} `json:"attachment"`
	// IsCompactSummary: the summary Claude Code writes as a user message right after a compaction
	// ("This session is being continued from a previous conversation…"). Not something the person said.
	IsCompactSummary bool   `json:"isCompactSummary"`
	Subtype          string `json:"subtype"` // type=system: compact_boundary marks where a compaction happened
}

func scan(path string, fn func(e *entry, raw []byte) bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 256<<20)
	for sc.Scan() {
		var e entry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if !fn(&e, sc.Bytes()) {
			break
		}
	}
	return sc.Err()
}

// parentOf finds the parentUuid of the user message we sent with uuid id. found=false: not in the transcript.
// A steer that landed mid-turn is recorded as a queued_command attachment carrying our uuid as source_uuid;
// forking at its parent is what Claude Code's own /rewind does for it ("No code restore").
func parentOf(path, id string) (parent string, found bool, err error) {
	err = scan(path, func(e *entry, _ []byte) bool {
		if e.UUID == id || (e.Type == "attachment" && e.Attachment != nil && e.Attachment.Type == "queued_command" && e.Attachment.SourceUUID == id) {
			found = true
			if e.ParentUUID != nil {
				parent = *e.ParentUUID
			}
			return false
		}
		return true
	})
	return
}

type userMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// userText 取一条用户消息里人说的话；工具结果、命令包装、系统注入的返回 ""。
func userText(e *entry) string {
	if e.Type != "user" || e.IsSidechain || e.IsMeta || e.IsCompactSummary {
		return ""
	}
	var m userMsg
	if json.Unmarshal(e.Message, &m) != nil {
		return ""
	}
	var text string
	var str string
	if json.Unmarshal(m.Content, &str) == nil {
		text = str
	} else {
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(m.Content, &parts) != nil {
			return ""
		}
		for _, p := range parts {
			if p.Type == "tool_result" {
				return ""
			}
			if p.Type == "text" {
				text += p.Text
			}
		}
	}
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "<local-command") || strings.HasPrefix(t, "<command-") || strings.HasPrefix(t, "<system-reminder>") || strings.HasPrefix(t, "Caveat:") {
		return ""
	}
	return text
}

func listTranscripts(root, cwd string, limit int) ([]acp.SessionInfo, error) {
	var files []string
	if cwd != "" {
		files, _ = filepath.Glob(filepath.Join(projectDirFor(root, cwd), "*.jsonl"))
	} else {
		files, _ = filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	}
	type fi struct {
		path string
		mod  time.Time
	}
	var all []fi
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && st.Size() > 0 {
			all = append(all, fi{f, st.ModTime()})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mod.After(all[j].mod) })
	var out []acp.SessionInfo
	for _, f := range all {
		if len(out) >= limit {
			break
		}
		id := strings.TrimSuffix(filepath.Base(f.path), ".jsonl")
		var title, scwd string
		n := 0
		_ = scan(f.path, func(e *entry, _ []byte) bool {
			n++
			if e.Cwd != "" && scwd == "" {
				scwd = e.Cwd
			}
			if e.Type == "summary" && e.Summary != "" {
				title = e.Summary
			}
			if title == "" {
				if t := userText(e); t != "" {
					title = t
				}
			}
			return n < 400 && (title == "" || scwd == "")
		})
		if title == "" {
			continue // 只有系统消息的空会话
		}
		// 标题和 Claude Code 自己的 /resume 列表一致：/rename 过的名字 > 它自动起的标题 > 第一句话。
		if c, ai := titles(f.path); c != "" {
			title = c
		} else if ai != "" {
			title = ai
		}
		// 目录名变形是有损的（长路径会被截断），同一个目录下可能混着别的 cwd 的会话：以记录里的 cwd 为准。
		if cwd != "" && scwd != "" && filepath.Clean(scwd) != filepath.Clean(cwd) {
			continue
		}
		if scwd == "" {
			scwd = cwd
		}
		up := f.mod.UTC().Format(time.RFC3339)
		t := strings.Join(strings.Fields(title), " ")
		if r := []rune(t); len(r) > 120 {
			t = string(r[:120]) + "…"
		}
		out = append(out, acp.SessionInfo{SessionId: acp.SessionId(id), Cwd: scwd, Title: &t, UpdatedAt: &up})
	}
	return out, nil
}

// replayTranscript 把一份会话记录重放成 session/update。只走主链（不含子 agent 的旁支）。
// userImages 取一条用户消息里贴的图（base64 那种），和 userText 认的是同一类消息（人说的、不是工具结果）。
// 重放时和文字一起发：不发的话，会话在别处接着聊过、整份重放一次之后，带截图的消息就只剩文字（踩过：截图没了）。
func userImages(e *entry) []acp.ContentBlock {
	if e.Type != "user" || e.IsSidechain || e.IsMeta || e.IsCompactSummary {
		return nil
	}
	var m userMsg
	if json.Unmarshal(e.Message, &m) != nil {
		return nil
	}
	var parts []struct {
		Type   string `json:"type"`
		Source struct {
			Type      string `json:"type"`
			MediaType string `json:"media_type"`
			Data      string `json:"data"`
		} `json:"source"`
	}
	if json.Unmarshal(m.Content, &parts) != nil {
		return nil
	}
	var out []acp.ContentBlock
	for _, p := range parts {
		if p.Type == "tool_result" {
			return nil
		}
		if p.Type == "image" && p.Source.Type == "base64" && p.Source.Data != "" {
			out = append(out, acp.ImageBlock(p.Source.Data, p.Source.MediaType))
		}
	}
	return out
}

func replayTranscript(path string, emit func(acp.SessionUpdate)) error {
	return replayTranscriptUntil(path, "", emit)
}

// replayTranscriptUntil：重放到 uuid 为 last 的那一条（含）为止；last 为空就是整份。
// 回退之后还没说过话的会话用它：新会话的记录要等下一句话才写，这时它的内容 = 原会话到分叉点为止。
func replayTranscriptUntil(path, last string, emit func(acp.SessionUpdate)) error {
	tmp := &session{a: &Agent{}, tools: map[string]*toolState{}}
	tmp.a.conn = nil
	// 借用 stream.go 里那套「assistant 块 / 工具结果 → update」的翻译，输出改成 emit。
	rec := &replayer{s: tmp, emit: emit}
	return scan(path, func(e *entry, raw []byte) bool {
		if e.IsSidechain {
			return true
		}
		switch e.Type {
		case "user":
			// 文字和图是同一条消息的几块，用同一个 MessageId 发，界面按它拼回一条
			if t, imgs := userText(e), userImages(e); t != "" || len(imgs) > 0 {
				id := e.UUID
				if t != "" {
					emit(acp.SessionUpdate{UserMessageChunk: &acp.SessionUpdateUserMessageChunk{Content: acp.TextBlock(t), MessageId: &id}})
				}
				for _, b := range imgs {
					emit(acp.SessionUpdate{UserMessageChunk: &acp.SessionUpdateUserMessageChunk{Content: b, MessageId: &id}})
				}
				return last == "" || e.UUID != last
			}
			var m userMsg
			if json.Unmarshal(e.Message, &m) == nil {
				var blocks []json.RawMessage
				if json.Unmarshal(m.Content, &blocks) == nil {
					for _, b := range blocks {
						rec.toolResult(b, e.ToolResult)
					}
				}
			}
		case "system":
			// Same marker the live stream shows for it (stream.go); the summary message itself is skipped by userText.
			if e.Subtype == "compact_boundary" {
				emit(compactedNote)
			}
		case "assistant":
			var m struct {
				ID      string            `json:"id"`
				Content []json.RawMessage `json:"content"`
			}
			if json.Unmarshal(e.Message, &m) == nil {
				for _, b := range m.Content {
					rec.assistant(m.ID, b)
				}
			}
		}
		return last == "" || e.UUID != last
	})
}

type replayer struct {
	s    *session
	emit func(acp.SessionUpdate)
}

func (r *replayer) assistant(apiID string, raw json.RawMessage) {
	r.s.withEmit(r.emit, func() { r.s.onAssistantBlock(context.Background(), apiID, "", raw) })
}

func (r *replayer) toolResult(raw, structured json.RawMessage) {
	r.s.withEmit(r.emit, func() { r.s.onToolResult(context.Background(), raw, structured) })
}

// titles：会话记录里最后一次 /rename 的名字（{"type":"custom-title","customTitle":…}）和 claude 自己起的标题
// （{"type":"ai-title","aiTitle":…}，对话往下走它会改，后写的算）。
// 改名是追加在文件末尾的，只读最后 512KB，不为它把几十 MB 的记录整个扫一遍。
func titles(path string) (custom, ai string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", ""
	}
	off := st.Size() - 512<<10
	if off < 0 {
		off = 0
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return "", ""
	}
	for _, line := range bytes.Split(buf, []byte("\n")) {
		if !bytes.Contains(line, []byte(`"custom-title"`)) && !bytes.Contains(line, []byte(`"ai-title"`)) {
			continue
		}
		var e entry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		if e.Type == "custom-title" && e.CustomTitle != "" {
			custom = e.CustomTitle
		}
		if e.Type == "ai-title" && e.AITitle != "" {
			ai = e.AITitle
		}
	}
	return custom, ai
}

// userTextsFrom：记录文件 offset 之后人说的话（跳过斜杠命令的包装、系统提醒、工具结果）。
// offset 比文件还大（文件被换掉 / 截断过）就从头读。
func userTextsFrom(path string, offset int64) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || offset > st.Size() {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil
	}
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 256<<20)
	for sc.Scan() {
		if !bytes.Contains(sc.Bytes(), []byte(`"type":"user"`)) {
			continue
		}
		var e entry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		// 这两种是 claude 自己写进来的、不是人说的话，算进来追上就误报「在别处接着聊过」。重放时不滤，界面各有画法。
		//   - 「[Request interrupted by user…]」：被打断时补写的（gerdr 重启、Esc 都会；踩过：只是重启了一下 App）
		//   - 「<task-notification>…」：后台任务跑完 / 被停时 CLI 自己开一轮汇报（踩过：后台 find 超时被停，提示「1 条消息」）
		t := userText(&e)
		if tt := strings.TrimSpace(t); tt != "" && !strings.HasPrefix(tt, "[Request interrupted by user") && !strings.HasPrefix(tt, "<task-notification>") {
			out = append(out, t)
		}
	}
	return out
}
