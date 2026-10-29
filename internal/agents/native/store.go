package native

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/zbysir/gerdr/internal/agents/native/llm"
)

// 会话记录：<dir>/sessions/<id>.jsonl，一行一条 entry，只追加；回退时整份重写（先写临时文件再改名）。
//
// 记录里是**完整的历史**（压缩过的也留着），发给模型的上下文从最后一条 compact 往后算（context()）：
// 回退到压缩之前的某句话时，截掉那条 compact，上下文自然回到压缩前的样子。
//
// hub 有自己的对话日志（界面看的是那份），这里的是给模型的上下文 + LoadSession 重放用的。

type entry struct {
	T  string `json:"t"` // meta | msg | compact
	At int64  `json:"at"`
	// ID：user 消息 = gerdr 的 messageId（回退按它找）；assistant = 推给界面时用的 messageId。
	ID   string       `json:"id,omitempty"`
	Meta *sessionMeta `json:"meta,omitempty"`
	Msg  *llm.Message `json:"msg,omitempty"`
	// UI：tool 结果在界面上长什么样（重放用）。
	UI *toolUI `json:"ui,omitempty"`
	// Summary：compact 的摘要，之后的上下文从它开始。
	Summary string `json:"summary,omitempty"`
}

type sessionMeta struct {
	Cwd     string `json:"cwd"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Created int64  `json:"created,omitempty"`
	// Allow：「以后不再问」放行过的规则（permission.go），跟着会话走、重启后还在。
	Allow []string `json:"allow,omitempty"`
}

type toolUI struct {
	Title   string                 `json:"title"`
	Kind    acp.ToolKind           `json:"kind"`
	Input   any                    `json:"input,omitempty"`
	Status  acp.ToolCallStatus     `json:"status"`
	Content []acp.ToolCallContent  `json:"content,omitempty"`
	Locs    []acp.ToolCallLocation `json:"locs,omitempty"`
}

type store struct{ dir string }

func (st store) path(id string) string { return filepath.Join(st.dir, "sessions", id+".jsonl") }

func (st store) exists(id string) bool {
	_, err := os.Stat(st.path(id))
	return err == nil
}

func (st store) load(id string) ([]entry, error) {
	f, err := os.Open(st.path(id))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 256<<20)
	for sc.Scan() {
		var e entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

func (st store) append(id string, es ...entry) error {
	if err := os.MkdirAll(filepath.Join(st.dir, "sessions"), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(st.path(id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, e := range es {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		w.Write(b)
		w.WriteByte('\n')
	}
	return w.Flush()
}

func (st store) rewrite(id string, es []entry) error {
	if err := os.MkdirAll(filepath.Join(st.dir, "sessions"), 0o700); err != nil {
		return err
	}
	tmp := st.path(id) + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, e := range es {
		b, _ := json.Marshal(e)
		w.Write(b)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, st.path(id))
}

type listed struct {
	ID      string
	Cwd     string
	Title   string
	Updated time.Time
}

// list 列出所有会话（新的在前）。标题是第一句人话。
func (st store) list() []listed {
	ents, _ := os.ReadDir(filepath.Join(st.dir, "sessions"))
	var out []listed
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(name, ".jsonl")
		info, err := e.Info()
		if err != nil {
			continue
		}
		es, _ := st.load(id)
		l := listed{ID: id, Updated: info.ModTime()}
		for _, x := range es {
			if x.Meta != nil && l.Cwd == "" {
				l.Cwd = x.Meta.Cwd
			}
			if x.T == "msg" && x.Msg != nil && x.Msg.Role == "user" && l.Title == "" && x.ID != "" {
				l.Title = oneLine(userText(*x.Msg), 120)
			}
		}
		if l.Title == "" {
			continue // 没说过话的不列
		}
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// lastMeta：记录里最后一条 meta（设置会变，只追加新的一条）。
func lastMeta(es []entry) sessionMeta {
	var m sessionMeta
	for _, e := range es {
		if e.Meta != nil {
			m = *e.Meta
		}
	}
	return m
}

// buildContext：发给模型的消息 —— 从最后一条 compact 开始（摘要当成一条 user 消息），之后的 msg 照搬。
func buildContext(es []entry) []llm.Message {
	start, summary := 0, ""
	for i, e := range es {
		if e.T == "compact" {
			start, summary = i+1, e.Summary
		}
	}
	var out []llm.Message
	if summary != "" {
		out = append(out, llm.Message{Role: "user", Content: []llm.Part{llm.TextPart(summaryPrefix + summary)}})
	}
	for _, e := range es[start:] {
		if e.T == "msg" && e.Msg != nil {
			out = append(out, *e.Msg)
		}
	}
	return out
}

const summaryPrefix = "This session is being continued from an earlier conversation that ran out of context. " +
	"The summary below covers the earlier part of the conversation.\n\n"

func userText(m llm.Message) string {
	var b strings.Builder
	for _, p := range m.Content {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}
