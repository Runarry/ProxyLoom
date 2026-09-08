//go:build linux && !amd64 && !arm64

package exec

func extraBlockedSyscalls() []uint32 { return nil }
