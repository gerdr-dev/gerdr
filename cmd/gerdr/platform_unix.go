//go:build !windows

package main

import (
	"os"
	"syscall"
	"time"
)

// lockFile：一个数据目录只许一个 gerdr 在用（两个进程同时写会话日志会互相覆盖）。
func lockFile(p string) (*os.File, error) {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// waitParent：父进程（App 外壳）没了就返回。Unix 上父进程死了，孤儿进程会被过继给 init / launchd，ppid 当场变掉。
func waitParent(pid int) {
	for {
		time.Sleep(time.Second)
		if os.Getppid() != pid {
			return
		}
	}
}
