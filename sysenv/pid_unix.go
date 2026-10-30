//go:build !windows

package sysenv

import "syscall"

// PidAlive：这个进程还在不在（EPERM 也算在：进程在，只是不归我们管）。
func PidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// TerminatePid：请它退出（SIGTERM，让它有机会收尾）。
func TerminatePid(pid int) { _ = syscall.Kill(pid, syscall.SIGTERM) }
