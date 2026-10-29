package native

import (
	"context"
	"fmt"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/internal/acpx"
	"github.com/gerdr-dev/gerdr/internal/agents/native/llm"
)

// 后台任务：bash 的 run_in_background、task（子 agent）的 run_in_background，以及界面上「转到后台」（= claude 的 ctrl+b，
// _gerdr/task_background）把正在前台跑的命令 / 子 agent 挪到后台。这一轮接着往下走，任务跑完时：
//   - 会话正在跑：通知排进插话队列，下一次调模型时带上；
//   - 会话闲着：自己开一轮（session_info_update 的 autoTurn，hub 当成「agent 自己跑起来了」），把通知交给模型处理。
//     照 Claude Code：后台任务跑完它也是自己开一轮。
//
// 任务 id 就是起它的那个工具调用的 id（界面上任务栏那一行和对话里的卡片对得上）。只在内存里：gerdr 重启后台任务就没了。

// track 登记一个后台任务（任务栏里一行），stop 是停它的办法。
func (s *session) track(id, typ, desc string, stop func()) {
	s.mu.Lock()
	if s.tasks == nil {
		s.tasks = map[string]*acpx.Task{}
	}
	if s.stops == nil {
		s.stops = map[string]func(){}
	}
	t := s.tasks[id]
	if t == nil {
		t = &acpx.Task{ID: id, Type: typ, Description: oneLine(desc, 160), ToolCallID: id, StartedAt: time.Now().UnixMilli()}
		s.tasks[id] = t
	}
	t.Status, t.Background = "running", true
	s.stops[id] = stop
	s.mu.Unlock()
	s.pushTasks()
}

// finishTask：任务结束，更新任务栏。
func (s *session) finishTask(id, status, summary string) {
	s.mu.Lock()
	delete(s.stops, id)
	if t := s.tasks[id]; t != nil {
		t.Status, t.EndedAt, t.Summary = status, time.Now().UnixMilli(), oneLine(summary, 200)
	}
	s.mu.Unlock()
	s.pushTasks()
}

func (s *session) stopTask(id string) bool {
	s.mu.Lock()
	stop := s.stops[id]
	s.mu.Unlock()
	if stop == nil {
		return false
	}
	stop()
	return true
}

func (s *session) stopAll() {
	s.mu.Lock()
	var stops []func()
	for _, f := range s.stops {
		stops = append(stops, f)
	}
	s.mu.Unlock()
	for _, f := range stops {
		f()
	}
}

// detachable：前台跑着的命令 / 子 agent 登记一个「转到后台」的口子；关掉返回的 chan 就是让它转后台。
func (s *session) detachable(id string) (<-chan struct{}, func()) {
	ch := make(chan struct{})
	s.mu.Lock()
	if s.fg == nil {
		s.fg = map[string]chan struct{}{}
	}
	s.fg[id] = ch
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		if s.fg[id] == ch {
			delete(s.fg, id)
		}
		s.mu.Unlock()
	}
}

func (s *session) detach(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.fg[id]
	if ch == nil {
		return false
	}
	delete(s.fg, id)
	close(ch)
	return true
}

// notifyDone：后台任务跑完了，告诉模型。
func (s *session) notifyDone(text string) {
	note := "<background-task-notification>\n" + text + "\n</background-task-notification>"
	s.mu.Lock()
	if s.busy {
		s.queue = append(s.queue, queued{note: note})
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	go s.autoTurn(note)
}

// autoTurn：会话闲着时自己开一轮处理通知。
func (s *session) autoTurn(note string) {
	ctx, end, err := s.begin()
	if err != nil {
		// 刚好有人发了消息、开了一轮：排进它的队里
		s.mu.Lock()
		s.queue = append(s.queue, queued{note: note})
		s.mu.Unlock()
		return
	}
	s.send(acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{acpx.AutoTurnKey: true})}})
	defer func() {
		end()
		s.send(acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{acpx.AutoTurnKey: false})}})
	}()
	s.addMsg(llm.Message{Role: "user", Content: []llm.Part{llm.TextPart(note)}}, "", nil)
	r := s.newRun(ctx)
	for {
		err = r.loop(ctx)
		if err != nil || s.endIfIdle(end) {
			break
		}
	}
	if err != nil && err != errStopped && ctx.Err() == nil {
		r.notice("处理后台任务的结果时出错：" + oneLine(err.Error(), 200))
	}
}

// ---- bash 的后台 ----

// bgBash 把一个已经起来的命令交给后台：等它结束（或被停），然后通知。
func (c *call) bgBash(p *proc, desc string) {
	s := c.r.s
	ctx, cancel := context.WithCancel(context.Background())
	s.track(c.id, "local_bash", desc, cancel)
	go func() {
		var werr error
		stopped := false
		select {
		case werr = <-p.done:
		case <-ctx.Done():
			werr, stopped = p.kill(), true
		}
		cancel()
		out, _ := p.buf.snapshot()
		code := exitCode(werr)
		status, line := "completed", "completed"
		switch {
		case stopped:
			status, line = "killed", "was stopped"
		case code != 0:
			status, line = "failed", fmt.Sprintf("failed with exit code %d", code)
		}
		s.finishTask(c.id, status, lastLine(out))
		s.notifyDone(fmt.Sprintf("Background command %q (task id %s) %s after %s.\nOutput (tail):\n%s",
			oneLine(desc, 200), c.id, line, time.Since(p.start).Round(time.Second), tailStr(out, 8000)))
	}()
}

// ---- 看 / 停后台任务的工具 ----

var bashOutputTool = &tool{
	name:     "task_output",
	desc:     "Get the current output and status of a background task (a bash command started with run_in_background, or a background sub-agent). You are notified automatically when a background task finishes, so only use this to check progress.",
	params:   obj(map[string]any{"id": prop("string", "The task id")}, "id"),
	readOnly: true,
	show: func(args map[string]any, cwd string) (string, acp.ToolKind, []acp.ToolCallLocation) {
		return "Task output " + str(args, "id"), acp.ToolKindRead, nil
	},
	run: func(c *call, args map[string]any) result {
		s := c.r.s
		id := str(args, "id")
		s.mu.Lock()
		t := s.tasks[id]
		var status string
		if t != nil {
			status = t.Status
		}
		out := s.bgOut[id]
		s.mu.Unlock()
		if t == nil {
			return errResult("no task %s", id)
		}
		text := "Status: " + status
		if out != nil {
			o, _ := out.snapshot()
			text += "\nOutput (tail):\n" + tailStr(o, outMax)
		} else if t.Progress != "" {
			text += "\nLatest: " + t.Progress
		}
		return result{text: text}
	},
}

var stopTaskTool = &tool{
	name:     "task_stop",
	desc:     "Stop a running background task by id.",
	params:   obj(map[string]any{"id": prop("string", "The task id")}, "id"),
	readOnly: true, // 只能停自己起的
	show: func(args map[string]any, cwd string) (string, acp.ToolKind, []acp.ToolCallLocation) {
		return "Stop task " + str(args, "id"), acp.ToolKindOther, nil
	},
	run: func(c *call, args map[string]any) result {
		if !c.r.s.stopTask(str(args, "id")) {
			return errResult("no running task %s", str(args, "id"))
		}
		return result{text: "Stopped."}
	},
}
