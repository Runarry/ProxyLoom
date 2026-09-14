//go:build !linux

package exec

func pinParentThread() func() { return func() {} }
