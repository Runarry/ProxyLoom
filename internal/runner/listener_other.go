//go:build !linux

package runner

func ownsListener(pid, port int) bool { return false }
