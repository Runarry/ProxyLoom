package exec

import "golang.org/x/sys/unix"

func extraBlockedSyscalls() []uint32 {
	return []uint32{unix.SYS_FORK, unix.SYS_VFORK, unix.SYS_CHMOD, unix.SYS_CHOWN, unix.SYS_LCHOWN, unix.SYS_UTIME, unix.SYS_UTIMES}
}
