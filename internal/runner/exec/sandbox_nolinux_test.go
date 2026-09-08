//go:build !linux

package exec

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestIsolatedCheckerRejectsNonLinux(t *testing.T) {
	id, registry, workspace := helperSetup(t)
	defer workspace.Close()
	registry.Builds = map[ir.ID]capability.Build{id: {ID: id, Family: ir.Xray}}
	_, err := ValidateIsolated(context.Background(), registry, id, []byte(`{}`), time.Second, SandboxPolicy{MemoryBytes: 1 << 30, Processes: 32, CPUSeconds: 1})
	if !errors.Is(err, ErrSandboxUnavailable) {
		t.Fatalf("non-Linux checker did not fail closed: %v", err)
	}
}
