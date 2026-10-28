package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zbysir/gerdr/internal/hub"
)

// 安卓 App 的后台通知（android/ 的 NoticeService）：App 关着 / 锁屏时，一个前台服务挂在这两个口上长轮询。
//
//	GET /api/app/notices?since=&wait=50  「等你」「跑完了」「出错了」—— 一条一条的提示，弹系统通知
//	GET /api/app/agents?ver=&wait=50     所有会话的状态一览 —— 常驻通知和桌面小部件照它画
//
// 为什么长轮询不用 /ws：/ws 推的是界面要的全部东西（会话摘要里带着命令列表、配置项，一帧几 KB），后台走中继一直收
// 这些，流量和电都白花；而且 /ws 算「远程界面开着」，会让 Mac 不休眠（internal/awake）—— 后台通知不该有这个副作用。
// 长轮询一分钟一个请求，状态一变当场回。上限 50 秒：赶在隧道 / 反代常见的 60 秒空闲掐断之前回。
//
// 提示的口径和界面上的系统通知（ui/src/App.tsx 的 useAttention）一样：变成「等你」、从「在跑 / 等你」变成「空闲 / 出错」。
// 界面那边只在页面开着时有效；这边是服务端算的，App 不在前台也收得到。App 在前台时它自己不弹（网页有提示）。

const (
	noticeKeep = 64               // 环里留几条：App 断开一会儿回来，补得上中间那几条
	waitMax    = 50 * time.Second // 长轮询最多挂多久
)

type appNotice struct {
	Seq     int64  `json:"seq"`
	Session string `json:"session"`
	Status  string `json:"status"` // waiting | done | error
	Agent   string `json:"agent"`
	Title   string `json:"title"`
	Text    string `json:"text"`
	At      int64  `json:"at"` // unix 毫秒
}

type appAgent struct {
	Session   string `json:"session"`
	Agent     string `json:"agent"`
	Status    string `json:"status"` // hub 的会话状态：waiting / running / idle / error / starting
	Workspace string `json:"workspace"`
	Title     string `json:"title"`
	Changed   int64  `json:"changed"` // 状态上次变的时刻，unix 毫秒
}

type appWatch struct {
	once sync.Once

	mu      sync.Mutex
	seq     int64 // 最新一条提示的序号
	notices []appNotice
	ver     int64 // 一览的版本：任何会话的状态 / 标题变了、新建、删了都加一
	last    map[string]watchState
	bell    chan struct{} // 有变化就 close 换新的：挂着的请求都醒
}

type watchState struct {
	status, title string
	changed       int64
}

func (s *Server) watch() *appWatch {
	w := &s.appWatch
	w.once.Do(func() {
		w.last = map[string]watchState{}
		w.bell = make(chan struct{})
		for _, sum := range s.Hub.List() {
			w.last[sum.ID] = watchState{sum.Status, sum.Title, time.Now().UnixMilli()}
		}
		go s.watchLoop()
	})
	return w
}

// watchLoop 订阅 hub 的广播。订阅在慢消费时会被 hub 掐掉（Sub.send），那就重订一次 —— 中间漏的状态变化
// 拿一份全量 List 对一下就补上了（只补版本号，不补提示：补出来的「跑完了」时间也是错的）。
func (s *Server) watchLoop() {
	w := &s.appWatch
	for {
		sub := s.Hub.Subscribe()
		w.resync(s.Hub.List())
		for m := range sub.C() {
			switch m.T {
			case "session":
				if m.Session != nil {
					s.onSession(*m.Session)
				}
			case "session_removed":
				w.mu.Lock()
				delete(w.last, m.ID)
				w.ver++
				w.ringLocked()
				w.mu.Unlock()
			}
		}
		s.Hub.Unsubscribe(sub)
		time.Sleep(time.Second)
	}
}

func (w *appWatch) resync(list []hub.Summary) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now().UnixMilli()
	seen := map[string]bool{}
	changed := false
	for _, sum := range list {
		seen[sum.ID] = true
		if old, ok := w.last[sum.ID]; !ok || old.status != sum.Status || old.title != sum.Title {
			w.last[sum.ID] = watchState{sum.Status, sum.Title, now}
			changed = true
		}
	}
	for id := range w.last {
		if !seen[id] {
			delete(w.last, id)
			changed = true
		}
	}
	if changed {
		w.ver++
		w.ringLocked()
	}
}

func (s *Server) onSession(sum hub.Summary) {
	w := &s.appWatch
	w.mu.Lock()
	old, had := w.last[sum.ID]
	if had && old.status == sum.Status && old.title == sum.Title {
		w.mu.Unlock()
		return // 会话摘要推得很密（命令列表、上下文用量都会推一帧），状态和标题没变就不算
	}
	now := time.Now().UnixMilli()
	st := watchState{sum.Status, sum.Title, old.changed}
	if !had || old.status != sum.Status {
		st.changed = now
	}
	w.last[sum.ID] = st
	w.ver++
	var n *appNotice
	if had {
		switch {
		case old.status != hub.StWaiting && sum.Status == hub.StWaiting:
			text := sum.Waiting
			if text == "" {
				text = "需要你批准或回答问题"
			}
			n = &appNotice{Status: "waiting", Text: text}
		case (old.status == hub.StRunning || old.status == hub.StWaiting) && (sum.Status == hub.StIdle || sum.Status == hub.StError):
			if sum.Status == hub.StError {
				n = &appNotice{Status: "error", Text: sum.Error}
			} else {
				n = &appNotice{Status: "done"}
			}
		}
	}
	if n != nil {
		w.seq++
		n.Seq, n.Session, n.Agent, n.Title, n.At = w.seq, sum.ID, sum.Agent, sessionName(sum), now
		w.notices = append(w.notices, *n)
		if len(w.notices) > noticeKeep {
			w.notices = w.notices[len(w.notices)-noticeKeep:]
		}
	}
	w.ringLocked()
	w.mu.Unlock()
	// 「跑完了」的正文是 agent 最后那段话：要读日志，放到锁外面算，算完补进那一条（还没被取走的话就带上了）
	if n != nil && n.Status == "done" && s.Hub != nil {
		if sess, err := s.Hub.Session(sum.ID); err == nil {
			if t := lastAgentText(sess.Events()); t != "" {
				w.mu.Lock()
				for i := range w.notices {
					if w.notices[i].Seq == n.Seq {
						w.notices[i].Text = t
					}
				}
				w.mu.Unlock()
			}
		}
	}
}

func (w *appWatch) ringLocked() {
	close(w.bell)
	w.bell = make(chan struct{})
}

// wait 挂到 ready() 为真、或者超时 / 请求断了。ready 在锁里调。
func (w *appWatch) wait(r *http.Request, d time.Duration, ready func() bool) {
	t := time.NewTimer(d)
	defer t.Stop()
	for {
		w.mu.Lock()
		if ready() {
			w.mu.Unlock()
			return
		}
		bell := w.bell
		w.mu.Unlock()
		select {
		case <-bell:
		case <-t.C:
			return
		case <-r.Context().Done():
			return
		}
	}
}

func waitParam(r *http.Request) time.Duration {
	n, _ := strconv.Atoi(r.URL.Query().Get("wait"))
	d := time.Duration(n) * time.Second
	if d > waitMax {
		d = waitMax
	}
	return d
}

// appNotices：since 之后的提示。不带 since（App 的第一拍）只回当前序号、不回提示 —— 不然服务启动、手机重启、
// App 被杀后拉起，都会把环里的旧提示全弹一遍。seq 比 since 小 = gerdr 重启过（序号从头数），也当场回，让 App 对上新的底。
func (s *Server) appNotices(rw http.ResponseWriter, r *http.Request) {
	w := s.watch()
	since, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	if err != nil || since < 0 {
		w.mu.Lock()
		seq := w.seq
		w.mu.Unlock()
		writeJSON(rw, map[string]any{"seq": seq, "notices": []appNotice{}})
		return
	}
	w.wait(r, waitParam(r), func() bool { return w.seq != since })
	w.mu.Lock()
	out := []appNotice{}
	for _, n := range w.notices {
		if n.Seq > since {
			out = append(out, n)
		}
	}
	seq := w.seq
	w.mu.Unlock()
	writeJSON(rw, map[string]any{"seq": seq, "notices": out})
}

// appAgents：一览。ver 和手上的一样就挂着等，变了当场回。
func (s *Server) appAgents(rw http.ResponseWriter, r *http.Request) {
	w := s.watch()
	ver, err := strconv.ParseInt(r.URL.Query().Get("ver"), 10, 64)
	if err == nil {
		w.wait(r, waitParam(r), func() bool { return w.ver != ver })
	}
	list := s.Hub.List()
	w.mu.Lock()
	out := make([]appAgent, 0, len(list))
	for _, sum := range list {
		out = append(out, appAgent{
			Session: sum.ID, Agent: sum.Agent, Status: sum.Status, Title: sessionName(sum),
			Workspace: filepath.Base(sum.Cwd), Changed: w.last[sum.ID].changed,
		})
	}
	v := w.ver
	w.mu.Unlock()
	writeJSON(rw, map[string]any{"ver": v, "agents": out})
}

// sessionName：和界面上一样的叫法（标题，没有就是目录名）。
func sessionName(sum hub.Summary) string {
	if sum.Title != "" {
		return sum.Title
	}
	if b := filepath.Base(sum.Cwd); b != "." && b != "/" {
		return b
	}
	return sum.Agent
}

// lastAgentText：最后一轮里 agent 说的最后那段话（连续的 agent_message_chunk 拼起来），截到通知放得下的长度。
func lastAgentText(evs []*hub.Event) string {
	var parts []string
	for i := len(evs) - 1; i >= 0; i-- {
		e := evs[i]
		if e.Kind == hub.EvTurnStart || e.Kind == hub.EvUser {
			break
		}
		if e.Kind != hub.EvUpdate {
			continue
		}
		var u struct {
			SessionUpdate string `json:"sessionUpdate"`
			Content       struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(e.Update, &u) != nil {
			continue
		}
		if u.SessionUpdate == "agent_message_chunk" && u.Content.Type == "text" {
			parts = append(parts, u.Content.Text)
		} else if len(parts) > 0 && u.SessionUpdate != "agent_thought_chunk" && u.SessionUpdate != "usage_update" {
			break // 往前碰到工具调用之类：最后那段话到头了
		}
	}
	var b strings.Builder
	for i := len(parts) - 1; i >= 0; i-- {
		b.WriteString(parts[i])
	}
	t := strings.TrimSpace(b.String())
	if r := []rune(t); len(r) > 300 {
		t = string(r[:300]) + "…"
	}
	return t
}
