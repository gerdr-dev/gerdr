package server

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zbysir/gerdr/internal/auth"
)

// 正在连着的远程界面（设置 →「远程访问」里列出来，tab 上挂个数字红点）。
// 按远程的 /ws 算：远程界面开着就一直有这一条，关掉 / 锁屏被浏览器掐掉就没了 —— 和不休眠（internal/awake）同一个口径。
// 一台设备开两个标签页就是两条，照实列。

type RemoteConn struct {
	ID     int64  `json:"id"`
	Device string `json:"device"` // 设备 id（对得上「配过对的设备」那张表）
	Label  string `json:"label"`  // 配对时起的名字
	IP     string `json:"ip"`
	Client string `json:"client"` // 「iPhone · Safari」这种，从 User-Agent 粗看
	Since  int64  `json:"since"`  // 连上的时刻，unix 毫秒
}

type remoteConns struct {
	mu   sync.Mutex
	next int64
	m    map[int64]RemoteConn
}

func (rc *remoteConns) list() []RemoteConn {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	out := make([]RemoteConn, 0, len(rc.m))
	for _, c := range rc.m {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Since < out[j].Since })
	return out
}

// trackRemote 登记一条远程 /ws，返回断开时调的函数。连上、断开都广播一次（界面上的红点和列表跟着变）。
func (s *Server) trackRemote(r *http.Request) func() {
	c := RemoteConn{IP: auth.ClientIP(r), Client: uaBrief(r.UserAgent()), Since: time.Now().UnixMilli()}
	if s.Remote != nil {
		if id := s.Remote.Auth.Authenticate(r); id != nil {
			c.Device, c.Label = id.Device.ID, id.Label
		}
	}
	rc := &s.remoteConns
	rc.mu.Lock()
	if rc.m == nil {
		rc.m = map[int64]RemoteConn{}
	}
	rc.next++
	c.ID = rc.next
	rc.m[c.ID] = c
	rc.mu.Unlock()
	s.broadcastRemoteConns()
	return func() {
		rc.mu.Lock()
		delete(rc.m, c.ID)
		rc.mu.Unlock()
		s.broadcastRemoteConns()
	}
}

func (s *Server) broadcastRemoteConns() {
	if s.Hub != nil {
		s.Hub.Broadcast("remote_conns", s.remoteConns.list())
	}
}

// uaBrief：User-Agent 粗看成「设备 · 浏览器」。只给人认一眼，认不出就空着。
func uaBrief(ua string) string {
	has := func(k string) bool { return strings.Contains(ua, k) }
	dev := ""
	switch {
	case has("iPhone"):
		dev = "iPhone"
	case has("iPad"):
		dev = "iPad"
	case has("Android"):
		dev = "Android"
	case has("Macintosh"):
		dev = "Mac"
	case has("Windows"):
		dev = "Windows"
	case has("Linux"):
		dev = "Linux"
	}
	br := ""
	switch { // 顺序要紧：Edge / Chrome 的 UA 里都带 Safari，Edge 里还带 Chrome
	case has("EdgA/") || has("EdgiOS/") || has("Edg/"):
		br = "Edge"
	case has("CriOS/") || has("Chrome/"):
		br = "Chrome"
	case has("FxiOS/") || has("Firefox/"):
		br = "Firefox"
	case has("Safari/"):
		br = "Safari"
	}
	switch {
	case dev != "" && br != "":
		return dev + " · " + br
	default:
		return dev + br
	}
}
