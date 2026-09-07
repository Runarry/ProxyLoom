//go:build linux

package exec

import "syscall"

// Subreaper keeps killed helpers' grandchildren from becoming PID 1 zombies.
const prSetChildSubreaper = 36

func enableChildSubreaper() {
	_, _, _ = syscall.Syscall6(syscall.SYS_PRCTL, uintptr(prSetChildSubreaper), 1, 0, 0, 0, 0)
}
