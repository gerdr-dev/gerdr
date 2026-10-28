//go:build !darwin && !windows

package term

import (
	"os"
	"strconv"
)

// LiveCwd：shell 现在在哪个目录。拿不到返回空。
func (t *Term) LiveCwd() string {
	d, _ := os.Readlink("/proc/" + strconv.Itoa(t.pid) + "/cwd")
	return d
}
