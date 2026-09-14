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
// may create threads, but cannot fork other processes. Processes bounds Linux's
// per-UID thread count across the supervisor and all active cores. Offline-only
// work uses 32; concurrent online work uses the container's 128-thread ceiling.
type SandboxPolicy struct {
	MemoryBytes uint64         `json:"memory_bytes"`
	Processes   uint64         `json:"processes"`
	CPUSeconds  uint64         `json:"cpu_seconds"`
	Network     *NetworkPolicy `json:"network,omitempty"`
}

type NetworkPolicy struct {
	ListenerPort uint16   `json:"listener_port"`
	RemotePorts  []uint16 `json:"remote_ports"`
}

func (p SandboxPolicy) valid() bool {
	if p.Network != nil {
		if p.Network.ListenerPort < 20000 || p.Network.ListenerPort > 20127 || len(p.Network.RemotePorts) < 1 || len(p.Network.RemotePorts) > 2 {
			return false
		}
		for _, port := range p.Network.RemotePorts {
			if port == 0 {
				return false
			}
		}
	}
	return p.MemoryBytes >= 1<<20 && p.MemoryBytes <= 2147483647 && p.Processes >= 1 && p.Processes <= 128 && p.CPUSeconds >= 1 && p.CPUSeconds <= 300
}

// ValidateIsolated is the only execution entry point used by the runner's
// remote job consumer. Raw Run/Start remain available to controlled fixtures.
func ValidateIsolated(ctx context.Context, registry Registry, buildID ir.ID, config []byte, timeout time.Duration, policy SandboxPolicy) (Result, error) {
	if !policy.valid() || policy.Network != nil || timeout <= 0 || timeout > 300*time.Second {
		return Result{}, ErrSandboxUnavailable
	}
	return executeConfigured(ctx, registry, buildID, config, false, Options{Timeout: timeout, Sandbox: &policy}, Workspace.Close)
}

// StartIsolated retains checker file/process limits and restricts TCP ports.
// IP pinning and deployment egress rules enforce the address boundary.
func StartIsolated(ctx context.Context, registry Registry, buildID ir.ID, config []byte, policy SandboxPolicy, started func(int)) (Result, error) {
	if !policy.valid() || policy.Network == nil {
		return Result{}, ErrSandboxUnavailable
	}
	return executeConfigured(ctx, registry, buildID, config, true, Options{Timeout: 60 * time.Second, Sandbox: &policy, Started: started}, Workspace.Close)
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
