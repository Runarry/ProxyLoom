package config

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// The host writes this receipt only after installing OUTPUT rules in this
// container's network namespace. The startup nonce prevents accepting a stale
// receipt even when Linux reuses a namespace inode in the same host boot.
type NetworkGuard struct {
	RunnerID  string `json:"runner_id"`
	BootID    string `json:"boot_id"`
	Namespace string `json:"network_namespace"`
	Nonce     string `json:"startup_nonce"`
}

func (g NetworkGuard) Matches(runnerID, bootID, namespace, nonce string) bool {
	_, nonceErr := hex.DecodeString(nonce)
	return len(nonce) == 64 && nonceErr == nil && runnerUUID(runnerID) && runnerUUID(bootID) && strings.HasPrefix(namespace, "net:[") && strings.HasSuffix(namespace, "]") && g.RunnerID == runnerID && g.BootID == bootID && g.Namespace == namespace && g.Nonce == nonce
}

func (c RunnerTransport) WaitNetworkGuard(ctx context.Context) error {
	if !c.NetworkEnabled {
		return nil
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return configError("PROXYLOOM_RUNNER_NETWORK_GUARD_FILE", "linux_network_guard_required")
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return configError("PROXYLOOM_RUNNER_NETWORK_GUARD_FILE", "startup_nonce_failed")
	}
	nonce := hex.EncodeToString(random)
	request, err := os.CreateTemp("/tmp", "proxyloom-network-guard-*")
	if err != nil {
		return configError("PROXYLOOM_RUNNER_NETWORK_GUARD_FILE", "startup_nonce_failed")
	}
	_, writeErr := request.WriteString(nonce + "\n")
	closeErr := request.Close()
	if writeErr != nil || closeErr != nil || os.Rename(request.Name(), "/tmp/proxyloom-network-guard-nonce") != nil {
		_ = os.Remove(request.Name())
		return configError("PROXYLOOM_RUNNER_NETWORK_GUARD_FILE", "startup_nonce_failed")
	}
	namespace, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return configError("PROXYLOOM_RUNNER_NETWORK_GUARD_FILE", "linux_network_guard_required")
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		data, err := readFile(c.NetworkGuardFile, "PROXYLOOM_RUNNER_NETWORK_GUARD_FILE", 1024)
		var guard NetworkGuard
		if err == nil && json.Unmarshal(data, &guard) == nil && guard.Matches(c.RunnerID, strings.TrimSpace(string(boot)), namespace, nonce) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
