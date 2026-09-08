//go:build !linux

package exec

func runPlatformHelper([]string) int { return 2 }

func runSandboxProbe(string) (int, bool) { return 0, false }
