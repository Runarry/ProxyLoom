package capability

import (
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/compat"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestEmbeddedLockPinsSixUnverifiedBuilds(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	builds := catalog.Builds()
	if len(builds) != 6 {
		t.Fatalf("got %d builds", len(builds))
	}
	if catalog.AdapterVersion != AdapterVersion || catalog.FixtureSet != FixtureSet {
		t.Fatal("adapter or fixture set drifted")
	}
	if catalog.ClientValidation == Verified {
		t.Fatal("client validation marked verified")
	}
	if len(catalog.Combinations) < 10 {
		t.Fatal("P0 combinations missing")
	}
	ids := map[string]struct{}{}
	for _, combo := range catalog.Combinations {
		if combo.State != Unverified {
			t.Fatalf("combination %s is %s", combo.ID, combo.State)
		}
		ids[combo.ID] = struct{}{}
	}
	for _, required := range []string{"P0-SS-TCP", "P0-TROJAN-TCP", "P0-VLESS-REALITY", "P0-HTTP-TCP"} {
		if _, ok := ids[required]; !ok {
			t.Fatalf("missing combination %s", required)
		}
	}
	seen := map[string]bool{}
	for _, build := range builds {
		if err := build.ID.Validate(); err != nil {
			t.Fatal(err)
		}
		seen[string(build.Family)+"/"+build.Arch] = true
		if build.OS != "linux" || strings.Contains(build.AssetURL, "latest") {
			t.Fatal("invalid build identity")
		}
		if err := catalog.Authenticate(build.ID, build.BinarySHA256, build.OS, build.Arch); err != nil {
			t.Fatal(err)
		}
		if catalog.Authenticate(build.ID, strings.Repeat("0", 64), build.OS, build.Arch) == nil {
			t.Fatal("digest mismatch accepted")
		}
		if catalog.Authenticate(build.ID, build.BinarySHA256, build.OS, "other") == nil {
			t.Fatal("arch mismatch accepted")
		}
		if err := catalog.RequireVerified(build.ID, "protocol.trojan.native_tcp.tls"); err != ErrUnverified {
			t.Fatalf("unverified capability: %v", err)
		}
		expected := NameUUID(IDNamespace, BuildName(build.Family, build.GitTag, build.OS, build.Arch, build.BinarySHA256))
		if build.ID != expected {
			t.Fatalf("id %s != %s", build.ID, expected)
		}
	}
	for _, family := range []ir.CoreFamily{ir.Xray, ir.SingBox, ir.Mihomo} {
		if !seen[string(family)+"/amd64"] || !seen[string(family)+"/arm64"] {
			t.Fatalf("missing architecture for %s", family)
		}
	}
	singbox, err := catalog.Lookup(ir.SingBox, "1.14.0", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.RequireVerified(singbox.ID, "policy.round_robin"); err != ErrUnsupported {
		t.Fatalf("round_robin: %v", err)
	}
	if _, err := catalog.Build("00000000-0000-4000-8000-000000000000"); err != ErrUnknownBuild {
		t.Fatalf("unknown build: %v", err)
	}
}

func TestParseRejectsVerifiedLatestAndMismatchedIDs(t *testing.T) {
	combos := compat.CombinationsYAML
	lock := string(compat.CoresLockYAML)
	tests := []struct {
		name string
		lock string
	}{
		{"latest url", strings.Replace(lock, "releases/download/v26.3.27/", "releases/download/latest/", 1)},
		{"verified client", strings.Replace(lock, "state: unverified", "state: verified", 1)},
		{"placeholder", strings.Replace(lock, "26.3.27", "REQUIRED_AT_M0", 1)},
		{"wrong id", strings.Replace(lock, "42f06ab1-ac85-524b-87e5-6bc1e9a82409", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", 1)},
		{"wrong digest charset", strings.Replace(lock, "8255dd939c34cf966cc91517b6324dd3c8d0bcf49ffac8beca049a38c46845ed", "8255dd939c34cf966cc91517b6324dd3c8d0bcf49ffac8beca049a38c46845eX", 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parse([]byte(test.lock), combos); err == nil {
				t.Fatal("invalid lock accepted")
			}
		})
	}
	verifiedTopLevel := strings.Replace(string(combos), "\nstate: unverified", "\nstate: verified", 1)
	if verifiedTopLevel == string(combos) {
		t.Fatal("top-level state mutation did not match")
	}
	if _, err := parse([]byte(lock), []byte(verifiedTopLevel)); err != ErrInvalidLock {
		t.Fatalf("verified combinations state: got %v, want ErrInvalidLock", err)
	}
}
