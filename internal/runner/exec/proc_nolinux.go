//go:build unix && !linux

package exec

func enableChildSubreaper() {}
