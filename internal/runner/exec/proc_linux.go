//go:build linux

package exec

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
)

func bindParentLifetime(attr *syscall.SysProcAttr) { attr.Pdeathsig = syscall.SIGKILL }

// Linux ties Pdeathsig to the creating OS thread. Keep that thread alive until
// the entire supervised process group is reaped, including normal cancellation.
func pinParentThread() func() {
	runtime.LockOSThread()
	return runtime.UnlockOSThread
}

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
