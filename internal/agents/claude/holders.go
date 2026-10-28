package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/zbysir/gerdr/internal/acpx"
	"github.com/zbysir/gerdr/internal/sysenv"
)

// 同一个 claude 会话被两处同时开着（gerdr 里接着聊、herdr / 终端里那个交互式 claude 也还开着）：
// 两个进程各自往同一个记录文件里追加，各接在自己看到的最后一条后面 —— 记录分了叉，两边都只看得见自己那一半
// （用户报的：herdr 里导入的会话，两边各说各的）。CLI 自己不拦这件事。
//
// 判据：交互式 claude 起来时会写 <配置目录>/sessions/<pid>.json（pid、sessionId、cwd、状态……），
// 退出时删掉（被强杀的会留下，所以还要核 pid 活着）。**gerdr 自己起的 stream-json 进程也写**
// （entrypoint 是 sdk-cli）—— 第一版以为不写，结果把自己当成了「别处开着」：人选「关掉那边」关的是自己，
// 下一轮进程重起、又登记，又问一遍（用户报的）。所以按 pid 排除自己起的（ownPids），不按 sdk-cli 排除：
// 别的用 SDK 的程序开着同一个会话，一样会分叉，该拦。

type sessionFile struct {
	Pid       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Name      string `json:"name"`
	Status    string `json:"status"`
}

func (a *Agent) holders(id string) []acpx.Holder {
	if id == "" {
		return nil
	}
	dir := filepath.Join(filepath.Dir(a.projectsDir()), "sessions")
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	own := a.ownPids()
	var out []acpx.Holder
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil || !strings.Contains(string(b), id) { // 先粗筛，几十个文件只解析命中的
			continue
		}
		var sf sessionFile
		if json.Unmarshal(b, &sf) != nil || sf.SessionID != id || sf.Pid <= 0 || sf.Pid == os.Getpid() || own[sf.Pid] {
			continue
		}
		if !sysenv.PidAlive(sf.Pid) {
			continue
		}
		out = append(out, acpx.Holder{Pid: sf.Pid, Name: sf.Name, Status: sf.Status, Cwd: sf.Cwd})
	}
	return out
}

// takeover：人挑了怎么处理「别处开着」。
func (s *session) takeover(action string) (acpx.TakeoverResponse, error) {
	s.mu.Lock()
	id := s.id
	s.mu.Unlock()
	switch action {
	case "kill":
		hs := s.a.holders(id)
		for _, h := range hs {
			sysenv.TerminatePid(h.Pid)
		}
		// 等**进程**真退了再放行，不是等登记文件没了：交互式 claude 收到 SIGTERM 先删 sessions/<pid>.json、
		// 再把记录写完才退（实测差一两秒），只看文件的话这段时间里两边还在同时写
		deadline := time.Now().Add(5 * time.Second)
		for _, h := range hs {
			for sysenv.PidAlive(h.Pid) && time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
			}
			if sysenv.PidAlive(h.Pid) {
				return acpx.TakeoverResponse{}, fmt.Errorf("pid %d 没有退出", h.Pid)
			}
		}
		return acpx.TakeoverResponse{SessionId: acp.SessionId(id)}, nil
	case "fork":
		// 带着全部历史复制出一个新会话（--resume 旧 --fork-session --session-id 新）：从此写自己的文件，别处那个照旧
		s.kill()
		newID := uuid.NewString()
		s.mu.Lock()
		s.fresh, s.forkFrom, s.forkAt = false, id, ""
		s.id = newID
		s.mu.Unlock()
		s.a.mu.Lock()
		s.a.sessions[newID] = s
		s.a.mu.Unlock()
		return acpx.TakeoverResponse{SessionId: acp.SessionId(newID)}, nil
	}
	return acpx.TakeoverResponse{}, acp.NewInvalidParams(map[string]any{"action": action})
}

// ownPids：这个适配器自己起着的 claude 进程。
func (a *Agent) ownPids() map[int]bool {
	a.mu.Lock()
	ss := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		ss = append(ss, s)
	}
	a.mu.Unlock()
	out := map[int]bool{}
	for _, s := range ss {
		s.mu.Lock()
		if s.p != nil && s.p.cmd != nil && s.p.cmd.Process != nil {
			out[s.p.cmd.Process.Pid] = true
		}
		s.mu.Unlock()
	}
	return out
}
