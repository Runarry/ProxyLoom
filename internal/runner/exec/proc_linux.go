//go:build linux

package exec

import (
	"fmt"
	"sync"
	"syscall"
)

// Subreaper keeps killed helpers' grandchildren from becoming PID 1 zombies.
const prSetChildSubreaper = 36

var (
	subreaperOnce sync.Once
	subreaperErr  error
)

func prepareReaper() error {
	subreaperOnce.Do(func() {
		_, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, uintptr(prSetChildSubreaper), 1, 0, 0, 0, 0)
		if errno != 0 {
			subreaperErr = fmt.Errorf("enable runner child subreaper: %w", errno)
		}
	})
	return subreaperErr
}
