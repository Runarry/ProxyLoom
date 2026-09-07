//go:build unix

package main

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestInitCertsRestrictiveUmask(t *testing.T) {
	previous := syscall.Umask(0077)
	defer syscall.Umask(previous)
	parent := filepath.Join(t.TempDir(), "isolation")
	out := filepath.Join(parent, "certs")
	if err := initCerts(out); err != nil {
		t.Fatal(err)
	}
	assertMode(t, parent, 0755)
	assertMode(t, out, 0755)
	for _, name := range []string{"ca.pem", "a.pem", "a-key.pem", "b.pem", "b-key.pem"} {
		assertMode(t, filepath.Join(out, name), 0644)
	}
}
