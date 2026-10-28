package hub

import (
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
