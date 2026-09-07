//go:build windows

package exec

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	ose "os/exec"
)

func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func terminate(cmd *ose.Cmd) {
	killTree(cmd)
}

func killProcess(cmd *ose.Cmd) {
	killTree(cmd)
}

func killTree(cmd *ose.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	root := os.Getenv("SYSTEMROOT")
	if root == "" {
		root = `C:\Windows`
	}
	killer := ose.Command(filepath.Join(root, "System32", "taskkill.exe"), "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	killer.Env = []string{"SYSTEMROOT=" + root}
	_ = killer.Run()
	_ = cmd.Process.Kill()
}
