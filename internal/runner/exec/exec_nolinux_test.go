//go:build !linux

package exec

func runPlatformHelper([]string) int { return 2 }
