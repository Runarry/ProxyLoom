package config

import (
	"strings"
	"testing"
)

func TestNetworkGuardRejectsPreviousHostContainerAndRunner(t *testing.T) {
	runner := "11111111-1111-4111-8111-111111111111"
	boot := "22222222-2222-4222-8222-222222222222"
	nonce := strings.Repeat("ab", 32)
	g := NetworkGuard{RunnerID: runner, BootID: boot, Namespace: "net:[1234]", Nonce: nonce}
	if !g.Matches(runner, boot, "net:[1234]", nonce) {
		t.Fatal("current receipt rejected")
	}
	for _, changed := range []NetworkGuard{{RunnerID: boot, BootID: boot, Namespace: g.Namespace, Nonce: nonce}, {RunnerID: runner, BootID: runner, Namespace: g.Namespace, Nonce: nonce}, {RunnerID: runner, BootID: boot, Namespace: "net:[1235]", Nonce: nonce}, {RunnerID: runner, BootID: boot, Namespace: g.Namespace, Nonce: strings.Repeat("cd", 32)}, {}} {
		if changed.Matches(runner, boot, g.Namespace, nonce) {
			t.Fatal("stale receipt accepted")
		}
	}
	if g.Matches(runner, boot, g.Namespace, "") {
		t.Fatal("missing startup nonce accepted")
	}
}
