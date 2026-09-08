package exec

import (
	"context"
	"errors"
	"time"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

const sandboxFlag = "__proxyloom_config_checker"
const sandboxFailureExit = 125

var ErrSandboxUnavailable = errors.New("runner_sandbox_unavailable")
var ErrResourceLimit = errors.New("runner_resource_limit")

// SandboxPolicy is an upper bound enforced by the operating system. A checker
// may create threads, but cannot fork other processes. The supported service
// uses one validation slot per runner and also sets GOMAXPROCS=2.
type SandboxPolicy struct {
	MemoryBytes uint64 `json:"memory_bytes"`
	Processes   uint64 `json:"processes"`
	CPUSeconds  uint64 `json:"cpu_seconds"`
}

func (p SandboxPolicy) valid() bool {
	return p.MemoryBytes >= 1<<20 && p.MemoryBytes <= 2147483647 && p.Processes >= 1 && p.Processes <= 32 && p.CPUSeconds >= 1 && p.CPUSeconds <= 300
}

// ValidateIsolated is the only execution entry point used by the runner's
// remote job consumer. Raw Run/Start remain available to controlled fixtures.
func ValidateIsolated(ctx context.Context, registry Registry, buildID ir.ID, config []byte, timeout time.Duration, policy SandboxPolicy) (Result, error) {
	if !policy.valid() || timeout <= 0 || timeout > 300*time.Second {
		return Result{}, ErrSandboxUnavailable
	}
	return executeConfigured(ctx, registry, buildID, config, false, Options{Timeout: timeout, Sandbox: &policy}, Workspace.Close)
}

// SandboxEntrypoint must be called before interpreting process configuration.
// The private re-exec receives only a descriptor from its supervisor; it never
// reads the runner's TLS or environment settings. A successful call execs the
// fixed checker and does not return.
func SandboxEntrypoint(args []string) (handled bool, exitCode int) {
	if len(args) == 0 || args[0] != sandboxFlag {
		return false, 0
	}
	if len(args) != 1 || sandboxChild() != nil {
		return true, sandboxFailureExit
	}
	return true, sandboxFailureExit
}
