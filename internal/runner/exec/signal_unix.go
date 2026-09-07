//go:build unix

package exec

import "syscall"

func signalZero(pid int) error {
	return syscall.Kill(pid, 0)
}
