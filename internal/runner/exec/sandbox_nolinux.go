//go:build !linux

package exec

import ose "os/exec"

func sandboxCommand(string, []string, string, []string, SandboxPolicy, string) (*ose.Cmd, func(), error) {
	return nil, nil, ErrSandboxUnavailable
}

func sandboxChild() error { return ErrSandboxUnavailable }
