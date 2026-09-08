//go:build unix

package exec

import (
	"fmt"
	"syscall"

	ose "os/exec"
)

func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

func terminate(cmd *ose.Cmd) error {
	return signalGroup(cmd, syscall.SIGTERM)
}

func killProcess(cmd *ose.Cmd) error {
	return signalGroup(cmd, syscall.SIGKILL)
}

func signalGroup(cmd *ose.Cmd, signal syscall.Signal) error {
	if err := syscall.Kill(-cmd.Process.Pid, signal); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("signal runner process group with %s: %w", signal, err)
	}
	return nil
}

// processGroupDone must only be called after cmd.Wait has reaped the root.
// A negative pgid restricts wait to this run's adopted descendants; it must
// never consume another Run's root or an unrelated child owned by the caller.
func processGroupDone(cmd *ose.Cmd) (bool, error) {
	pgid := cmd.Process.Pid
	// Bound each batch so reaping cannot starve the TERM/KILL deadlines.
	for i := 0; i < 64; i++ {
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(-pgid, &status, syscall.WNOHANG, nil)
		if err == syscall.EINTR {
			continue
		}
		if err != nil && err != syscall.ECHILD {
			return false, err
		}
		if pid <= 0 {
			break
		}
	}
	// ECHILD alone is insufficient: a live descendant may still be owned by
	// its own parent and only become available to the subreaper later.
	err := syscall.Kill(-pgid, 0)
	if err == syscall.ESRCH {
		return true, nil
	}
	return false, err
}
