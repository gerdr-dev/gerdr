//go:build !windows

package work

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// setProcGroup puts the command and what it starts in a process group, and stopping kills the whole tree: the group,
// plus descendants that made groups of their own (a script that starts codex, which runs commands in its own group).
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pid := cmd.Process.Pid
		for _, p := range descendants(pid) {
			_ = syscall.Kill(p, syscall.SIGKILL)
		}
		return syscall.Kill(-pid, syscall.SIGKILL)
	}
}

// descendants is every process below pid (by ps's parent links).
func descendants(pid int) []int {
	out, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=").Output()
	if err != nil {
		return nil
	}
	kids := map[int][]int{}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) != 2 {
			continue
		}
		c, _ := strconv.Atoi(f[0])
		p, _ := strconv.Atoi(f[1])
		kids[p] = append(kids[p], c)
	}
	var all []int
	queue := []int{pid}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, c := range kids[p] {
			all = append(all, c)
			queue = append(queue, c)
		}
	}
	return all
}
