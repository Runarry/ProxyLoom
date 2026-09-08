//go:build unix && !linux

package exec

func prepareReaper() error { return nil }
