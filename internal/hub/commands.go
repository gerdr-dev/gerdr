package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/coder/acp-go-sdk"
)

// 斜杠命令按 agent 记一份（最近一次报上来的），存在 <dir>/commands.json。
// claude 的命令列表是进程起来时才报的：导入的会话、gerdr 重启后还没说过话的会话，进程还没起，列表是空的 ——
// 输入框里打 / 什么都没有（用户报的）。命令是按 agent（和用户的配置）来的，不是按会话，所以拿别的会话报的顶上。
type cmdCache struct {
	mu   sync.Mutex
	path string
	m    map[string][]acp.AvailableCommand
}

func newCmdCache(dir string) *cmdCache {
	c := &cmdCache{path: filepath.Join(dir, "commands.json"), m: map[string][]acp.AvailableCommand{}}
	if b, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(b, &c.m)
	}
	return c
}

func (c *cmdCache) get(agent string) []acp.AvailableCommand {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[agent]
}

func (c *cmdCache) set(agent string, cmds []acp.AvailableCommand) {
	if len(cmds) == 0 {
		return
	}
	c.mu.Lock()
	c.m[agent] = cmds
	b, _ := json.Marshal(c.m)
	c.mu.Unlock()
	_ = os.WriteFile(c.path, b, 0o600)
}

// 模式 / 模型 / effort 那几个下拉框同理（<dir>/options.json）：它们是接上 agent 之后才报的，gerdr 重启后
// 还没说过话的会话一律没接，输入框下面那一栏是空的、聊一句才冒出来（用户报的）。选项本身按 agent 来，
// 拿上次报的那份画出来；**当前值**用这个会话自己记下的（Record.Mode / Config），不借别的会话的。
type agentOpts struct {
	Modes  *acp.SessionModeState     `json:"modes,omitempty"`
	Config []acp.SessionConfigOption `json:"config,omitempty"`
}

type optCache struct {
	mu   sync.Mutex
	path string
	m    map[string]json.RawMessage
}

func newOptCache(dir string) *optCache {
	c := &optCache{path: filepath.Join(dir, "options.json"), m: map[string]json.RawMessage{}}
	if b, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(b, &c.m)
	}
	return c
}

func (c *optCache) set(agent string, modes *acp.SessionModeState, config []acp.SessionConfigOption) {
	if modes == nil && len(config) == 0 {
		return
	}
	// 合并着写：claude 的会话刚接上、进程还没起时只报得出模式、报不出模型列表（config 是空的）——
	// 整份覆盖的话，存好的模型 / effort 被这一下冲掉，下拉框又只剩模式（用户报的）。
	c.mu.Lock()
	var old agentOpts
	_ = json.Unmarshal(c.m[agent], &old)
	if modes == nil {
		modes = old.Modes
	}
	if len(config) == 0 {
		config = old.Config
	}
	b, _ := json.Marshal(agentOpts{Modes: modes, Config: config})
	if string(c.m[agent]) == string(b) {
		c.mu.Unlock()
		return
	}
	c.m[agent] = b
	all, _ := json.Marshal(c.m)
	c.mu.Unlock()
	_ = os.WriteFile(c.path, all, 0o600)
}

// get 返回一份**拷贝**（调用方会改 CurrentValue）。
func (c *optCache) get(agent string) (o agentOpts, ok bool) {
	c.mu.Lock()
	b := c.m[agent]
	c.mu.Unlock()
	if b == nil {
		return o, false
	}
	return o, json.Unmarshal(b, &o) == nil
}

// cmdSets hands command lists out by content instead of inside every Summary. A list runs to ~60 KB (claude with
// many skills), the same few lists are shared by every session of an agent, and a Summary goes out with the session
// list on every page load and with every streamed chunk of a running turn: 181 sessions made /api/hello 8.7 MB
// (3.3 MB gzipped: the copies sit further apart than gzip's 32 KB window), which took 5-10 s to open over the relay.
// Now a Summary carries the list's key and the page fetches /api/commands/<key> once, only for the sessions it shows.
type cmdSets struct {
	mu    sync.Mutex
	byKey map[string][]acp.AvailableCommand
	// last remembers the key of a slice already hashed, by its first element: Summary runs on every chunk, and the
	// lists are replaced, never edited in place. Holding the slice in byKey keeps its memory from being reused for
	// another list while the pointer is remembered.
	last map[*acp.AvailableCommand]string
}

// cmdSetsMax: distinct lists kept; past it everything is dropped and rebuilt from what Summary sees next. A page that
// asks for a dropped key gets a 404 and asks again once the next Summary brings the key back.
const cmdSetsMax = 64

func (c *cmdSets) key(cmds []acp.AvailableCommand) string {
	if len(cmds) == 0 {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byKey == nil || len(c.byKey) >= cmdSetsMax {
		c.byKey, c.last = map[string][]acp.AvailableCommand{}, map[*acp.AvailableCommand]string{}
	}
	p := &cmds[0]
	if k, ok := c.last[p]; ok && len(c.byKey[k]) == len(cmds) {
		return k
	}
	b, _ := json.Marshal(cmds)
	sum := sha256.Sum256(b)
	k := hex.EncodeToString(sum[:8])
	if _, ok := c.byKey[k]; !ok {
		c.byKey[k] = cmds
	}
	c.last[p] = k
	return k
}

// CommandSet: the command list a Summary's CommandsKey stands for (nil if unknown).
func (h *Hub) CommandSet(key string) []acp.AvailableCommand {
	h.cmdSets.mu.Lock()
	defer h.cmdSets.mu.Unlock()
	return h.cmdSets.byKey[key]
}
