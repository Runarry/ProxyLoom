package capability

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Runarry/ProxyLoom/compat"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestCatalogReturnValuesAreIsolated(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	// Parse an independent baseline so a corrupted Load cache cannot hide a failure.
	baseline, err := parse(compat.CoresLockYAML, compat.CombinationsYAML)
	if err != nil {
		t.Fatal(err)
	}
	build := baseline.builds[0]
	var key string
	for _, record := range build.Capabilities {
		if len(record.FixtureIDs) > 0 && len(record.Evidence.FixtureIDs) > 0 {
			key = record.Key
			break
		}
	}
	if key == "" {
		t.Fatal("lock has no capability with nested fixture IDs")
	}
	accessors := map[string]func() []Record{
		"Builds": func() []Record { return catalog.Builds()[0].Capabilities },
		"Build": func() []Record {
			got, err := catalog.Build(build.ID)
			if err != nil {
				t.Fatal(err)
			}
			return got.Capabilities
		},
		"Lookup": func() []Record {
			got, err := catalog.Lookup(build.Family, build.Version, build.OS, build.Arch)
			if err != nil {
				t.Fatal(err)
			}
			return got.Capabilities
		},
		"Capability": func() []Record {
			got, err := catalog.Capability(build.ID, key)
			if err != nil {
				t.Fatal(err)
			}
			return []Record{got}
		},
	}
	for name, accessor := range accessors {
		t.Run(name, func(t *testing.T) {
			otherValues := map[string][]Record{}
			for otherName, otherAccessor := range accessors {
				otherValues[otherName] = otherAccessor()
			}
			mutateRecords(accessor())
			if !reflect.DeepEqual(catalog, baseline) {
				t.Fatal("accessor mutation changed source catalog")
			}
			for otherName, otherAccessor := range accessors {
				if !reflect.DeepEqual(otherValues[otherName], otherAccessor()) {
					t.Fatalf("mutation changed earlier %s result", otherName)
				}
			}
			fresh, err := Load()
			if err != nil || !reflect.DeepEqual(fresh, baseline) {
				t.Fatalf("accessor mutation changed Load cache: %v", err)
			}
		})
	}
	// Direct package-level mutation also exercises Load's own clone boundary.
	mutateRecords(catalog.builds[0].Capabilities)
	catalog.Combinations[0].State = Verified
	delete(catalog.byID, build.ID)
	fresh, err := Load()
	if err != nil || !reflect.DeepEqual(fresh, baseline) {
		t.Fatalf("Load instances share mutable catalog data: %v", err)
	}
}

func mutateRecords(records []Record) {
	for i := range records {
		records[i].State = Verified
		records[i].Evidence.State = Verified
		for j := range records[i].FixtureIDs {
			records[i].FixtureIDs[j] = "mutated"
		}
		for j := range records[i].Evidence.FixtureIDs {
			records[i].Evidence.FixtureIDs[j] = "mutated-evidence"
		}
	}
}

func TestCatalogConcurrentReturnValueMutation(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	baseline := catalog.Builds()
	build := baseline[0]
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 20 {
				mutateRecords(catalog.Builds()[0].Capabilities)
				got, err := catalog.Build(build.ID)
				if err != nil {
					t.Error(err)
					return
				}
				mutateRecords(got.Capabilities)
				got, err = catalog.Lookup(build.Family, build.Version, build.OS, build.Arch)
				if err != nil {
					t.Error(err)
					return
				}
				mutateRecords(got.Capabilities)
				for _, record := range build.Capabilities {
					got, err := catalog.Capability(build.ID, record.Key)
					if err != nil {
						t.Error(err)
						return
					}
					mutateRecords([]Record{got})
				}
				fresh, err := Load()
				if err != nil {
					t.Error(err)
					return
				}
				mutateRecords(fresh.builds[0].Capabilities)
			}
		})
	}
	workers.Wait()
	if !reflect.DeepEqual(catalog.Builds(), baseline) {
		t.Fatal("concurrent return value mutations changed source catalog")
	}
}

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
		capabilityKeys := map[string]bool{}
		for _, record := range build.Capabilities {
			if capabilityKeys[record.Key] {
				t.Fatalf("duplicate capability %s for %s/%s", record.Key, build.Family, build.Arch)
			}
			capabilityKeys[record.Key] = true
		}
		if build.Family == ir.SingBox {
			if err := catalog.RequireVerified(build.ID, "policy.round_robin"); err != ErrUnsupported {
				t.Fatalf("family rejection masked for %s: %v", build.Arch, err)
			}
		}
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
