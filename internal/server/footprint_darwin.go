package server

import (
	"syscall"
	"unsafe"
)

// footprintKB is a process's physical footprint, the "Memory" column of Activity Monitor. ps's RSS overstates a Go
// process badly on macOS: pages the runtime gave back (MADV_FREE) stay counted until the system is short of memory,
// so after the hub unloaded idle sessions the panel still showed ~180 MB for a 90 MB footprint.
//
// It's proc_pid_rusage(pid, RUSAGE_INFO_V2) without cgo: libproc implements that as __proc_info(PROC_INFO_CALL_PIDRUSAGE,
// pid, flavor, 0, buf, 0). ri_phys_footprint sits at byte 72 of every rusage_info version (after the 16-byte uuid and
// seven uint64s). Fails (other users' processes, a pid that's gone) → false, and the caller keeps RSS.
func footprintKB(pid int) (int64, bool) {
	const sysProcInfo, callPidRusage, rusageInfoV2 = 336, 9, 2
	var buf [512]byte // rusage_info_v2 is 232 bytes; room for the newer ones
	if _, _, e := syscall.Syscall6(sysProcInfo, callPidRusage, uintptr(pid), rusageInfoV2, 0, uintptr(unsafe.Pointer(&buf[0])), 0); e != 0 {
		return 0, false
	}
	fp := *(*uint64)(unsafe.Pointer(&buf[72]))
	return int64(fp >> 10), fp > 0
}
