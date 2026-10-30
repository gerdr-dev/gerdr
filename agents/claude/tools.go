package claude

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/coder/acp-go-sdk"
)

type toolInfo struct {
	title     string
	kind      acp.ToolKind
	locations []acp.ToolCallLocation
	content   []acp.ToolCallContent
}

// hiddenTool：不当成工具调用画出来的（它们另有去处）。
func hiddenTool(name string) bool {
	switch name {
	// SubagentHandback：后台子 agent 把汇报交回主 agent 用的内部工具（汇报转给子 agent 面板，stream.go）。
	case "AskUserQuestion", "ExitPlanMode", "EnterPlanMode", "SubagentHandback":
		return true
	}
	return false
}

func isEditTool(name string) bool {
	switch name {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return true
	}
	return false
}

func s(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

func rel(p, cwd string) string {
	if cwd != "" {
		if r, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
	}
	return p
}

// describeTool 给 Claude Code 内置工具起标题、归类、配 diff。input 为 nil 时只有名字（参数还在流式生成）。
func describeTool(name string, in map[string]any, cwd string) toolInfo {
	ti := toolInfo{title: name, kind: acp.ToolKindOther}
	path := s(in, "file_path")
	if path == "" {
		path = s(in, "notebook_path")
	}
	loc := func() {
		if path != "" {
			ti.locations = []acp.ToolCallLocation{{Path: path}}
		}
	}
	switch name {
	case "Bash", "BashOutput", "PowerShell":
		ti.kind = acp.ToolKindExecute
		if c := s(in, "command"); c != "" {
			ti.title = c
		} else if in == nil {
			ti.title = "Run command"
		}
	case "KillShell", "KillBash":
		ti.kind = acp.ToolKindExecute
		ti.title = "Stop background command"
	case "Read":
		ti.kind = acp.ToolKindRead
		ti.title = "Read " + rel(path, cwd)
		loc()
	case "Write":
		ti.kind = acp.ToolKindEdit
		ti.title = "Write " + rel(path, cwd)
		loc()
		if in != nil && path != "" {
			ti.content = []acp.ToolCallContent{acp.ToolDiffContent(path, s(in, "content"))}
		}
	case "Edit":
		ti.kind = acp.ToolKindEdit
		ti.title = "Edit " + rel(path, cwd)
		loc()
		if in != nil && path != "" {
			ti.content = []acp.ToolCallContent{acp.ToolDiffContent(path, s(in, "new_string"), s(in, "old_string"))}
		}
	case "MultiEdit":
		ti.kind = acp.ToolKindEdit
		ti.title = "Edit " + rel(path, cwd)
		loc()
		if edits, ok := in["edits"].([]any); ok {
			for _, e := range edits {
				em, _ := e.(map[string]any)
				ti.content = append(ti.content, acp.ToolDiffContent(path, s(em, "new_string"), s(em, "old_string")))
			}
		}
	case "NotebookEdit":
		ti.kind = acp.ToolKindEdit
		ti.title = "Edit notebook " + rel(path, cwd)
		loc()
	case "Glob":
		ti.kind = acp.ToolKindSearch
		ti.title = "Find " + s(in, "pattern")
		if p := s(in, "path"); p != "" {
			ti.title += " in " + rel(p, cwd)
		}
	case "Grep":
		ti.kind = acp.ToolKindSearch
		ti.title = "Search " + fmt.Sprintf("%q", s(in, "pattern"))
		if p := s(in, "path"); p != "" {
			ti.title += " in " + rel(p, cwd)
		}
	case "LS":
		ti.kind = acp.ToolKindSearch
		ti.title = "List " + rel(s(in, "path"), cwd)
	case "WebFetch":
		ti.kind = acp.ToolKindFetch
		ti.title = "Fetch " + s(in, "url")
	case "WebSearch":
		ti.kind = acp.ToolKindFetch
		ti.title = "Search the web: " + s(in, "query")
	case "Task", "Agent":
		ti.kind = acp.ToolKindThink
		if d := s(in, "description"); d != "" {
			ti.title = d
		} else {
			ti.title = "Sub-agent"
		}
	case "Skill":
		ti.title = "Skill " + s(in, "skill")
	default:
		if strings.HasPrefix(name, "mcp__") {
			parts := strings.SplitN(strings.TrimPrefix(name, "mcp__"), "__", 2)
			if len(parts) == 2 {
				ti.title = parts[0] + " · " + parts[1]
			}
		}
	}
	if ti.title == "" || strings.HasSuffix(ti.title, " ") {
		ti.title = strings.TrimSpace(name + " " + strings.TrimSpace(ti.title))
	}
	return ti
}

// todoPlan 把 TodoWrite 翻成 ACP 的 plan（界面上是一块会更新的待办清单，而不是一串工具调用）。
func todoPlan(in map[string]any) []acp.PlanEntry {
	todos, ok := in["todos"].([]any)
	if !ok {
		return nil
	}
	out := []acp.PlanEntry{}
	for _, t := range todos {
		m, _ := t.(map[string]any)
		st := acp.PlanEntryStatusPending
		switch s(m, "status") {
		case "in_progress":
			st = acp.PlanEntryStatusInProgress
		case "completed":
			st = acp.PlanEntryStatusCompleted
		}
		text := s(m, "content")
		if st == acp.PlanEntryStatusInProgress && s(m, "activeForm") != "" {
			text = s(m, "activeForm")
		}
		out = append(out, acp.PlanEntry{Content: text, Status: st, Priority: acp.PlanEntryPriorityMedium})
	}
	return out
}
