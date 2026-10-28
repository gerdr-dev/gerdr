package term

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// LiveCwd：shell 现在在哪个目录（mac 上没有 /proc，问 lsof）。拿不到返回空。
func (t *Term) LiveCwd() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsof", "-a", "-p", strconv.Itoa(t.pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "n/") {
			return l[1:]
		}
	}
	return ""
}
