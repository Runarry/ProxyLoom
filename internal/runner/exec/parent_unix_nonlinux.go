//go:build unix && !linux

package exec

import "syscall"

func bindParentLifetime(_ *syscall.SysProcAttr) {}
