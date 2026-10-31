package claude

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/acpx"
)

// 后台任务（Bash run_in_background、后台 / 前台子 agent、Monitor……）和「agent 自己开的一轮」。
//
// CLI 在 stream-json 里报这几条（实测，2.1.x）：
//   - system/task_started：{task_id, tool_use_id, task_type: local_bash|local_agent|…, description,
//     subagent_type, is_backgrounded}
//   - system/task_progress：子 agent 的进度（description 是它此刻在干什么、usage 里有 tool_uses）
//   - system/task_updated：{task_id, patch:{status, end_time}}
//   - system/task_notification：{task_id, status, summary, output_file}
//   - system/background_tasks_changed：{tasks:[…]} 此刻还在跑的全部
//
// 后台任务跑完时 CLI 会**自己**开一轮（session_state running → idle，前面没有 user 消息），
// 把结果告诉模型、模型接着说话。这一轮没有 session/prompt 对应，所以用 session_info_update
// 的 _meta 告诉 client「开始了 / 结束了」（acpx.AutoTurn）。

const maxDoneTasks = 20

func (s *session) taskEvent(ctx context.Context, sub string, raw []byte) {
	var ev struct {
		TaskID       string `json:"task_id"`
		ToolUseID    string `json:"tool_use_id"`
		TaskType     string `json:"task_type"`
		Description  string `json:"description"`
		SubagentType string `json:"subagent_type"`
		Background   bool   `json:"is_backgrounded"`
		Status       string `json:"status"`
		Summary      string `json:"summary"`
		Handback     string `json:"handback"`
		Patch        struct {
			Status string `json:"status"`
		} `json:"patch"`
		Usage struct {
			ToolUses int `json:"tool_uses"`
		} `json:"usage"`
		LastTool string `json:"last_tool_name"`
		Tasks    []struct {
			TaskID       string `json:"task_id"`
			TaskType     string `json:"task_type"`
			Description  string `json:"description"`
			SubagentType string `json:"subagent_type"`
		} `json:"tasks"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return
	}
	var subFinal *acp.SessionUpdate // 后台子 agent 跑完了：它最后的话给子 agent 面板
	s.mu.Lock()
	if s.tasks == nil {
		s.tasks = map[string]*acpx.Task{}
	}
	get := func(id string) *acpx.Task {
		t := s.tasks[id]
		if t == nil {
			t = &acpx.Task{ID: id, Status: "running", StartedAt: time.Now().UnixMilli()}
			s.tasks[id] = t
		}
		return t
	}
	now := time.Now().UnixMilli()
	switch sub {
	case "task_started":
		t := get(ev.TaskID)
		t.Type, t.Description, t.Subagent, t.ToolCallID, t.Background = ev.TaskType, ev.Description, ev.SubagentType, ev.ToolUseID, ev.Background
		if st := s.tools[ev.ToolUseID]; st != nil {
			t.Parent = st.parent
		}
	case "task_progress":
		t := get(ev.TaskID)
		t.Progress, t.ToolUses = ev.Description, ev.Usage.ToolUses
		if t.ToolCallID == "" {
			t.ToolCallID = ev.ToolUseID
		}
	case "task_updated":
		if ev.Patch.Status != "" {
			t := get(ev.TaskID)
			t.Status = ev.Patch.Status
			if t.Status != "running" && t.EndedAt == 0 {
				t.EndedAt = now
			}
		}
	case "task_notification":
		t := get(ev.TaskID)
		if ev.Status != "" {
			t.Status = ev.Status
		}
		t.Summary = ev.Summary
		// 经 SubagentHandback 交回的子 agent：summary 是写给模型看的内部说明（「report was delivered…」），不是汇报。
		if ev.Handback != "" {
			t.Summary = ""
		}
		if t.Background && ev.Handback == "" && (t.Type == "local_agent" || t.Subagent != "") && t.ToolCallID != "" && ev.Summary != "" {
			subFinal = &acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock(ev.Summary), Meta: acpx.Meta(map[string]any{"subagent": t.ToolCallID})}}
		}
		if t.EndedAt == 0 {
			t.EndedAt = now
		}
	case "background_tasks_changed":
		live := map[string]bool{}
		for _, x := range ev.Tasks {
			live[x.TaskID] = true
			t := get(x.TaskID)
			if t.Type == "" {
				t.Type, t.Description, t.Subagent = x.TaskType, x.Description, x.SubagentType
			}
			t.Background = true
		}
		// 从列表里消失了却没收到结束事件的，当它结束了。
		for id, t := range s.tasks {
			if !live[id] && t.Background && t.Status == "running" {
				t.Status, t.EndedAt = "completed", now
			}
		}
	}
	list := s.taskListLocked()
	s.mu.Unlock()
	if subFinal != nil {
		s.update(ctx, *subFinal)
	}
	s.update(ctx, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{"tasks": list})}})
}

// backgroundTask：这个工具调用开的是不是后台任务（后台子 agent 的工具结果只是「已启动」）。
func (s *session) backgroundTask(toolCallID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tasks {
		if t.ToolCallID == toolCallID {
			return t.Background
		}
	}
	return false
}

// taskListLocked: every running task plus the latest finished ones (maxDoneTasks subagents and as many others), by start time.
func (s *session) taskListLocked() []acpx.Task {
	var running, done []acpx.Task
	for _, t := range s.tasks {
		if t.Status == "running" {
			running = append(running, *t)
		} else {
			done = append(done, *t)
		}
	}
	sort.Slice(done, func(i, j int) bool { return done[i].EndedAt > done[j].EndedAt })
	// Subagents and commands are capped apart: a session that runs many slow commands (each is a task) pushed its
	// subagents out within minutes, and a subagent's row needs its task (background tag, real duration, its report).
	var kept []acpx.Task
	agents, others := 0, 0
	for _, t := range done {
		n := &others
		if t.Type == "local_agent" || t.Subagent != "" {
			n = &agents
		}
		if *n >= maxDoneTasks {
			delete(s.tasks, t.ID)
			continue
		}
		*n++
		kept = append(kept, t)
	}
	out := append(running, kept...)
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt < out[j].StartedAt })
	if out == nil {
		out = []acpx.Task{}
	}
	return out
}

// autoTurn：CLI 在没有 session/prompt 的时候自己跑起来 / 停下来了。
func (s *session) autoTurn(ctx context.Context, running bool) {
	s.update(ctx, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{acpx.AutoTurnKey: running})}})
}

// stopTask：停掉一个后台任务（和 claude 界面里任务列表的「停止」一样）。
func (s *session) stopTask(ctx context.Context, id string) error {
	s.mu.Lock()
	pr := s.p
	s.mu.Unlock()
	if pr == nil {
		return acp.NewInvalidRequest("claude 进程没在跑")
	}
	_, err := pr.control(ctx, map[string]any{"subtype": "stop_task", "task_id": id})
	return err
}

// backgroundTool：把一个正在前台跑的工具调用（长命令、子 agent）转到后台 —— 等于 claude 里按 ctrl+b。
// 这一轮会继续往下走，那个任务在后台跑完再通知。
func (s *session) backgroundTool(ctx context.Context, toolCallID string) (bool, error) {
	s.mu.Lock()
	pr := s.p
	s.mu.Unlock()
	if pr == nil {
		return false, acp.NewInvalidRequest("claude 进程没在跑")
	}
	res, err := pr.control(ctx, map[string]any{"subtype": "background_tasks", "tool_use_id": toolCallID})
	if err != nil {
		return false, err
	}
	var r struct {
		Backgrounded *bool `json:"backgrounded"`
	}
	_ = json.Unmarshal(res, &r)
	return r.Backgrounded == nil || *r.Backgrounded, nil
}

// withdraw：插话写进 stdin 之后在 CLI 的命令队列里排着，直到下一个工具结果的间隙才被拿进这一轮。
// 还没被拿进去的，cancel_async_message 能按 uuid 收回（{cancelled: true}）。要在打断之前收：
// 打断会把排着的插话留到下一轮执行（interrupt 的 still_queued / put_back）。
func (s *session) withdraw(ctx context.Context, ids []string) acpx.WithdrawResponse {
	out := acpx.WithdrawResponse{Withdrawn: []string{}}
	s.mu.Lock()
	pr := s.p
	s.mu.Unlock()
	if pr == nil {
		return out
	}
	for _, id := range ids {
		res, err := pr.control(ctx, map[string]any{"subtype": "cancel_async_message", "message_uuid": id})
		if err != nil {
			continue
		}
		var r struct {
			Cancelled bool `json:"cancelled"`
		}
		if json.Unmarshal(res, &r) == nil && r.Cancelled {
			out.Withdrawn = append(out.Withdrawn, id)
		}
	}
	return out
}
