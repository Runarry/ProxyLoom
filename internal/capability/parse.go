package capability

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/Runarry/ProxyLoom/compat"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"gopkg.in/yaml.v3"
)

type lockFile struct {
	SchemaVersion    int               `yaml:"schema_version"`
	AdapterVersion   string            `yaml:"adapter_version"`
	FixtureSet       string            `yaml:"fixture_set"`
	IDNamespace      ir.ID             `yaml:"id_namespace"`
	PinnedAt         string            `yaml:"pinned_at"`
	ClientValidation clientValidation  `yaml:"client_validation"`
	Builds           []lockBuild       `yaml:"builds"`
	Unsupported      []lockUnsupported `yaml:"unsupported"`
}

type clientValidation struct {
	State State `yaml:"state"`
}

type lockBuild struct {
	ID           ir.ID         `yaml:"id"`
	Family       ir.CoreFamily `yaml:"family"`
	Version      string        `yaml:"version"`
	GitTag       string        `yaml:"git_tag"`
	GitCommit    string        `yaml:"git_commit"`
	OS           string        `yaml:"os"`
	Arch         string        `yaml:"arch"`
	AssetName    string        `yaml:"asset_name"`
	AssetURL     string        `yaml:"asset_url"`
	AssetSHA256  string        `yaml:"asset_sha256"`
	BinaryName   string        `yaml:"binary_name"`
	BinarySHA256 string        `yaml:"binary_sha256"`
	CGO          bool          `yaml:"cgo"`
	FeatureNotes string        `yaml:"feature_notes"`
	License      string        `yaml:"license"`
	Source       string        `yaml:"source"`
}

type lockUnsupported struct {
	Family ir.CoreFamily `yaml:"family"`
	Key    string        `yaml:"key"`
	State  State         `yaml:"state"`
	Reason string        `yaml:"reason"`
}

type combinationsFile struct {
	SchemaVersion      int               `yaml:"schema_version"`
	State              State             `yaml:"state"`
	Combinations       []lockCombination `yaml:"combinations"`
	SharedCapabilities []lockRecord      `yaml:"shared_capabilities"`
}

type lockCombination struct {
	ID            string `yaml:"id"`
	CapabilityKey string `yaml:"capability_key"`
	Protocol      string `yaml:"protocol"`
	Transport     string `yaml:"transport"`
	Security      string `yaml:"security"`
	State         State  `yaml:"state"`
}

type lockRecord struct {
	Key        string   `yaml:"key"`
	State      State    `yaml:"state"`
	Reason     string   `yaml:"reason"`
	FixtureIDs []string `yaml:"fixture_ids"`
}

var loaded = sync.OnceValues(func() (*Catalog, error) {
	return parse(compat.CoresLockYAML, compat.CombinationsYAML)
})

func Load() (*Catalog, error) {
	catalog, err := loaded()
	if err != nil {
		return nil, err
	}
	return catalog.clone(), nil
}

func parse(lockYAML, combinationsYAML []byte) (*Catalog, error) {
	if bytes.Contains(lockYAML, []byte("latest")) || bytes.Contains(lockYAML, []byte("REQUIRED_AT_M0")) {
		return nil, ErrInvalidLock
	}
	var lock lockFile
	if err := decodeYAML(lockYAML, &lock); err != nil {
		return nil, ErrInvalidLock
	}
	var combos combinationsFile
	if err := decodeYAML(combinationsYAML, &combos); err != nil {
		return nil, ErrInvalidLock
	}
	if lock.SchemaVersion != 1 || combos.SchemaVersion != 1 {
		return nil, ErrInvalidLock
	}
	if lock.AdapterVersion != AdapterVersion || lock.FixtureSet != FixtureSet || lock.IDNamespace != IDNamespace {
		return nil, ErrInvalidLock
	}
	if lock.ClientValidation.State == Verified || combos.State == Verified {
		return nil, ErrInvalidLock
	}
	if len(lock.Builds) != 6 {
		return nil, ErrInvalidLock
	}
	catalog := &Catalog{
		SchemaVersion:    lock.SchemaVersion,
		AdapterVersion:   lock.AdapterVersion,
		FixtureSet:       lock.FixtureSet,
		IDNamespace:      lock.IDNamespace,
		PinnedAt:         lock.PinnedAt,
		ClientValidation: lock.ClientValidation.State,
		byID:             map[ir.ID]int{},
	}
	seenPair := map[string]struct{}{}
	for _, combo := range combos.Combinations {
		if combo.ID == "" || combo.CapabilityKey == "" || combo.State == Verified {
			return nil, ErrInvalidLock
		}
		if combo.State == "" {
			combo.State = Unverified
		}
		catalog.Combinations = append(catalog.Combinations, Combination{
			ID: combo.ID, CapabilityKey: combo.CapabilityKey, Protocol: combo.Protocol,
			Transport: combo.Transport, Security: combo.Security, State: combo.State,
		})
	}
	if len(catalog.Combinations) < 10 {
		return nil, ErrInvalidLock
	}
	for i, raw := range lock.Builds {
		if err := validateBuild(lock.IDNamespace, raw); err != nil {
			return nil, err
		}
		pair := string(raw.Family) + "/" + raw.Arch
		if _, exists := seenPair[pair]; exists {
			return nil, ErrInvalidLock
		}
		seenPair[pair] = struct{}{}
		if _, exists := catalog.byID[raw.ID]; exists {
			return nil, ErrInvalidLock
		}
		build := Build{
			ID: raw.ID, Family: raw.Family, Version: raw.Version, GitTag: raw.GitTag, GitCommit: raw.GitCommit,
			OS: raw.OS, Arch: raw.Arch, AssetName: raw.AssetName, AssetURL: raw.AssetURL, AssetSHA256: raw.AssetSHA256,
			BinaryName: raw.BinaryName, BinarySHA256: raw.BinarySHA256, CGO: raw.CGO, FeatureNotes: raw.FeatureNotes,
			License: raw.License, Source: raw.Source, AdapterVersion: lock.AdapterVersion, FixtureSet: lock.FixtureSet,
		}
		for _, combo := range catalog.Combinations {
			build.Capabilities = append(build.Capabilities, Record{
				Key: combo.CapabilityKey, State: Unverified,
				Evidence: EvidenceRef{BuildID: raw.ID, State: Unverified},
			})
		}
		for _, shared := range combos.SharedCapabilities {
			if shared.State == Verified {
				return nil, ErrInvalidLock
			}
			build.Capabilities = append(build.Capabilities, Record{
				Key: shared.Key, State: shared.State, Reason: shared.Reason, FixtureIDs: append([]string(nil), shared.FixtureIDs...),
				Evidence: EvidenceRef{BuildID: raw.ID, State: shared.State, FixtureIDs: append([]string(nil), shared.FixtureIDs...)},
			})
		}
		for _, extra := range lock.Unsupported {
			if extra.Family != raw.Family {
				continue
			}
			if extra.State != Unsupported {
				return nil, ErrInvalidLock
			}
			build.Capabilities = append(build.Capabilities, Record{
				Key: extra.Key, State: Unsupported, Reason: extra.Reason,
				Evidence: EvidenceRef{BuildID: raw.ID, State: Unsupported},
			})
		}
		catalog.byID[raw.ID] = i
		catalog.builds = append(catalog.builds, build)
	}
	for _, family := range []ir.CoreFamily{ir.Xray, ir.SingBox, ir.Mihomo} {
		if _, ok := seenPair[string(family)+"/amd64"]; !ok {
			return nil, ErrInvalidLock
		}
		if _, ok := seenPair[string(family)+"/arm64"]; !ok {
			return nil, ErrInvalidLock
		}
	}
	return catalog, nil
}

func validateBuild(namespace ir.ID, build lockBuild) error {
	if build.OS != "linux" || (build.Arch != "amd64" && build.Arch != "arm64") {
		return ErrInvalidLock
	}
	if build.Family != ir.Xray && build.Family != ir.SingBox && build.Family != ir.Mihomo {
		return ErrInvalidLock
	}
	if !sha256Digest(build.AssetSHA256) || !sha256Digest(build.BinarySHA256) {
		return ErrInvalidLock
	}
	if build.AssetURL == "" || strings.Contains(build.AssetURL, "latest") || !strings.HasPrefix(build.AssetURL, "https://github.com/") {
		return ErrInvalidLock
	}
	if !strings.HasSuffix(build.AssetURL, "/"+build.AssetName) {
		return ErrInvalidLock
	}
	if build.GitTag == "" || build.GitCommit == "" || build.BinaryName == "" || build.License == "" || build.Source == "" {
		return ErrInvalidLock
	}
	expected := NameUUID(namespace, BuildName(build.Family, build.GitTag, build.OS, build.Arch, build.BinarySHA256))
	if build.ID != expected {
		return ErrInvalidLock
	}
	if err := build.ID.Validate(); err != nil {
		return ErrInvalidLock
	}
	return nil
}

func decodeYAML(data []byte, dest any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(dest); err != nil {
		return err
	}
	return nil
}

func sha256Digest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func (c *Catalog) clone() *Catalog {
	next := *c
	next.builds = c.Builds()
	next.Combinations = append([]Combination(nil), c.Combinations...)
	next.byID = make(map[ir.ID]int, len(c.byID))
	for id, index := range c.byID {
		next.byID[id] = index
	}
	return &next
}

func parseNamespace(id ir.ID) ([16]byte, error) {
	var out [16]byte
	raw, err := hex.DecodeString(strings.ReplaceAll(string(id), "-", ""))
	if err != nil || len(raw) != 16 {
		return out, fmt.Errorf("namespace")
	}
	copy(out[:], raw)
	return out, nil
}
