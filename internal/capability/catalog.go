// Package capability loads the frozen core lock and capability evidence model.
// It performs no compilation, process execution, or network access.
package capability

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"strings"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

type State string

const (
	Unverified  State = "unverified"
	Unsupported State = "unsupported"
	Verified    State = "verified"
)

const AdapterVersion = "0.1.0-m0-skeleton"
const FixtureSet = "isolation-v1"
const IDNamespace ir.ID = "8c3f0e2a-4b91-41d6-a2c1-9f0e6b7d4a10"

var (
	ErrUnknownBuild      = errors.New("unknown_core_build")
	ErrDigestMismatch    = errors.New("core_build_digest_mismatch")
	ErrArchMismatch      = errors.New("core_build_arch_mismatch")
	ErrOSMismatch        = errors.New("core_build_os_mismatch")
	ErrUnverified        = errors.New("capability_unverified")
	ErrUnsupported       = errors.New("capability_unsupported")
	ErrInvalidLock       = errors.New("invalid_core_lock")
	ErrUnknownCapability = errors.New("unknown_capability")
)

type Record struct {
	Key        string
	State      State
	Reason     string
	FixtureIDs []string
	Evidence   EvidenceRef
}

type EvidenceRef struct {
	Suite      string
	BuildID    ir.ID
	State      State
	Report     string
	FixtureIDs []string
}

type Combination struct {
	ID            string
	CapabilityKey string
	Protocol      string
	Transport     string
	Security      string
	State         State
}

type Build struct {
	ID             ir.ID
	Family         ir.CoreFamily
	Version        string
	GitTag         string
	GitCommit      string
	OS             string
	Arch           string
	AssetName      string
	AssetURL       string
	AssetSHA256    string
	BinaryName     string
	BinarySHA256   string
	CGO            bool
	FeatureNotes   string
	License        string
	Source         string
	AdapterVersion string
	FixtureSet     string
	Capabilities   []Record
}

type Catalog struct {
	SchemaVersion    int
	AdapterVersion   string
	FixtureSet       string
	IDNamespace      ir.ID
	PinnedAt         string
	ClientValidation State
	Combinations     []Combination
	builds           []Build
	byID             map[ir.ID]int
}

func (c *Catalog) Builds() []Build {
	out := make([]Build, len(c.builds))
	copy(out, c.builds)
	return out
}

func (c *Catalog) Build(id ir.ID) (Build, error) {
	index, ok := c.byID[id]
	if !ok {
		return Build{}, ErrUnknownBuild
	}
	return c.builds[index], nil
}

func (c *Catalog) Lookup(family ir.CoreFamily, version, os, arch string) (Build, error) {
	for _, build := range c.builds {
		if build.Family == family && build.Version == version && build.OS == os && build.Arch == arch {
			return build, nil
		}
	}
	return Build{}, ErrUnknownBuild
}

func (c *Catalog) Authenticate(id ir.ID, actualSHA256, os, arch string) error {
	build, err := c.Build(id)
	if err != nil {
		return err
	}
	if os != "" && build.OS != os {
		return ErrOSMismatch
	}
	if arch != "" && build.Arch != arch {
		return ErrArchMismatch
	}
	if actualSHA256 == "" || !equalDigest(build.BinarySHA256, actualSHA256) {
		return ErrDigestMismatch
	}
	return nil
}

func (c *Catalog) Capability(id ir.ID, key string) (Record, error) {
	build, err := c.Build(id)
	if err != nil {
		return Record{}, err
	}
	for _, record := range build.Capabilities {
		if record.Key == key {
			return record, nil
		}
	}
	return Record{}, ErrUnknownCapability
}

func (c *Catalog) RequireVerified(id ir.ID, key string) error {
	record, err := c.Capability(id, key)
	if err != nil {
		return err
	}
	switch record.State {
	case Verified:
		return nil
	case Unsupported:
		return ErrUnsupported
	default:
		return ErrUnverified
	}
}

func NameUUID(namespace ir.ID, name string) ir.ID {
	raw, err := parseNamespace(namespace)
	if err != nil {
		return ""
	}
	sum := sha1.New()
	sum.Write(raw[:])
	sum.Write([]byte(name))
	hashed := sum.Sum(nil)
	hashed[6] = hashed[6]&0x0f | 0x50
	hashed[8] = hashed[8]&0x3f | 0x80
	return ir.ID(fmt.Sprintf("%x-%x-%x-%x-%x", hashed[0:4], hashed[4:6], hashed[6:8], hashed[8:10], hashed[10:16]))
}

func BuildName(family ir.CoreFamily, tag, os, arch, binarySHA256 string) string {
	return string(family) + "|" + tag + "|" + os + "|" + arch + "|" + binarySHA256
}

func equalDigest(want, got string) bool {
	return strings.ToLower(strings.TrimSpace(want)) == strings.ToLower(strings.TrimSpace(got))
}
