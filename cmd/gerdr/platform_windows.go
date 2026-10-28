//go:build windows

package main

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// lockFile：一个数据目录只许一个 gerdr 在用。Windows 没有 flock，用 LockFileEx（进程退出时系统自动解锁）。
func lockFile(p string) (*os.File, error) {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// lockFileWait：同一把锁，拿不到就一直等（不带 FAIL_IMMEDIATELY 的 LockFileEx 就是阻塞的）。
func lockFileWait(p string) (*os.File, error) {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, ol); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// waitParent：父进程（App 外壳）没了就返回。Windows 上父进程死了 ppid **不会变**（不像 Unix 会过继给 init），
// 照 Unix 那样比 ppid 的话后端永远退不掉 —— 所以拿父进程的句柄等它结束。
func waitParent(pid int) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return // 打不开 = 已经没了
	}
	defer windows.CloseHandle(h)
	for {
		if ev, _ := windows.WaitForSingleObject(h, 1000); ev == windows.WAIT_OBJECT_0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
