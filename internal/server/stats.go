package server

import (
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// 资源占用（设置 →「资源」）：后端自己、它起的每个 agent 进程（连带 agent 自己起的 MCP 之类的子进程）、
// 以及界面所在的 WebView 进程（mac App 经 webpid 告诉我们；浏览器里没有）。都用 ps 量 RSS，和活动监视器对得上。

type procInfo struct {
	PID     int    `json:"pid"`
	Label   string `json:"label"`
	Session string `json:"session,omitempty"` // 对应的 gerdr 会话
	RSS     int64  `json:"rss"`               // 这个进程自己，KB
	Total   int64  `json:"total"`             // 连带它所有子进程，KB
	Procs   int    `json:"procs"`             // 子树里几个进程
}

type psRow struct {
	pid, ppid int
	rss       int64
	cmd       string
}

func psAll() map[int]*psRow {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,rss=,command=").Output()
	if err != nil {
		return nil
	}
	rows := map[int]*psRow{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		rss, _ := strconv.ParseInt(f[2], 10, 64)
		rows[pid] = &psRow{pid: pid, ppid: ppid, rss: rss, cmd: strings.Join(f[3:], " ")}
	}
	return rows
}

func argAfter(cmd, flag string) string {
	f := strings.Fields(cmd)
	for i := 0; i+1 < len(f); i++ {
		if f[i] == flag {
			return f[i+1]
		}
	}
	return ""
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	rows := psAll()
	kids := map[int][]int{}
	for _, p := range rows {
		kids[p.ppid] = append(kids[p.ppid], p.pid)
	}
	var subtree func(pid int) (int64, int)
	subtree = func(pid int) (int64, int) {
		p := rows[pid]
		if p == nil {
			return 0, 0
		}
		total, n := p.rss, 1
		for _, c := range kids[pid] {
			t, m := subtree(c)
			total, n = total+t, n+m
		}
		return total, n
	}
	self := os.Getpid()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	out := map[string]any{"goHeap": ms.HeapAlloc / 1024, "goroutines": runtime.NumGoroutine()}
	if p := rows[self]; p != nil {
		out["server"] = procInfo{PID: self, Label: "gerdr 后端", RSS: p.rss, Total: p.rss, Procs: 1}
	}
	var agents []procInfo
	for _, c := range kids[self] {
		p := rows[c]
		if strings.HasPrefix(p.cmd, "ps ") || strings.HasPrefix(p.cmd, "git ") {
			continue // 量的这一下自己起的 ps、顺手跑的 git，不是 agent
		}
		total, n := subtree(c)
		info := procInfo{PID: c, RSS: p.rss, Total: total, Procs: n, Label: p.cmd}
		switch {
		case strings.Contains(p.cmd, "app-server"):
			info.Label = "Codex（所有 Codex 会话共用）"
		case strings.Contains(p.cmd, "claude") || strings.Contains(p.cmd, "stream-json"):
			info.Label = "Claude Code"
			id := argAfter(p.cmd, "--session-id")
			if id == "" {
				id = argAfter(p.cmd, "--resume")
			}
			if sess := s.Hub.FindByAgent("claude", id); sess != nil {
				sum := sess.Summary()
				info.Session, info.Label = sum.ID, "Claude Code · "+firstNonEmpty(sum.Title, sum.Cwd)
			}
		default:
			if len(info.Label) > 80 {
				info.Label = info.Label[:80] + "…"
			}
		}
		agents = append(agents, info)
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Total > agents[j].Total })
	out["agents"] = agents
	if pid, err := strconv.Atoi(r.URL.Query().Get("webpid")); err == nil && pid > 0 {
		if p := rows[pid]; p != nil {
			out["web"] = procInfo{PID: pid, Label: "界面（WebView）", RSS: p.rss, Total: p.rss, Procs: 1}
		}
	}
	writeJSON(w, out)
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}
