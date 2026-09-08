//go:build windows

package exec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	ose "os/exec"
)

func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func prepareReaper() error { return nil }

// Windows retains taskkill's process-tree cleanup. The root is waited by
// cmd.Wait; unlike Linux, Windows has no adopted children to reap here.
func processGroupDone(cmd *ose.Cmd) (bool, error) { return true, nil }

func terminate(cmd *ose.Cmd) error {
	return killTree(cmd)
}

func killProcess(cmd *ose.Cmd) error {
	return killTree(cmd)
}

func killTree(cmd *ose.Cmd) error {
	root := os.Getenv("SYSTEMROOT")
	if root == "" {
		root = `C:\Windows`
	}
	ctx, cancel := context.WithTimeout(context.Background(), killGrace)
	defer cancel()
	killer := ose.CommandContext(ctx, filepath.Join(root, "System32", "taskkill.exe"), "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	killer.Env = []string{"SYSTEMROOT=" + root}
	treeErr := killer.Run()
	if treeErr == nil {
		return nil
	}
	rootErr := cmd.Process.Kill()
	if ctx.Err() != nil {
		return fmt.Errorf("terminate runner process tree: %w", errors.Join(ctx.Err(), treeErr))
	}
	// The root may exit while taskkill is starting. On Windows cmd.Wait also
	// releases the process handle, after which Process.Kill returns EINVAL.
	if errors.Is(rootErr, os.ErrProcessDone) || errors.Is(rootErr, syscall.EINVAL) {
		return nil
	}
	if treeErr != nil || rootErr != nil {
		return fmt.Errorf("terminate runner process tree: %w", errors.Join(treeErr, rootErr))
	}
	return nil
}
