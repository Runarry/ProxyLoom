package exec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

var (
	ErrUnknownExecutable = errors.New("unknown_executable")
	ErrForbiddenPath     = errors.New("forbidden_executable_path")
	ErrDigestMismatch    = errors.New("executable_digest_mismatch")
	ErrMissingBinary     = errors.New("executable_missing")
	ErrForbiddenArgs     = errors.New("forbidden_command_arguments")
)

type Registry interface {
	Executable(id ir.ID) (path string, build capability.Build, err error)
}

type MapRegistry struct {
	Files  map[ir.ID]string
	Builds map[ir.ID]capability.Build
}

func (m MapRegistry) Executable(id ir.ID) (string, capability.Build, error) {
	path, ok := m.Files[id]
	if !ok || path == "" {
		return "", capability.Build{}, ErrUnknownExecutable
	}
	if !filepath.IsAbs(path) {
		return "", capability.Build{}, ErrForbiddenPath
	}
	build := m.Builds[id]
	build.ID = id
	return filepath.Clean(path), build, nil
}

type DiskRegistry struct {
	Root    string
	Catalog *capability.Catalog
}

func (d DiskRegistry) Executable(id ir.ID) (string, capability.Build, error) {
	if d.Catalog == nil {
		return "", capability.Build{}, ErrUnknownExecutable
	}
	build, err := d.Catalog.Build(id)
	if err != nil {
		return "", capability.Build{}, ErrUnknownExecutable
	}
	if !filepath.IsAbs(d.Root) {
		return "", capability.Build{}, ErrForbiddenPath
	}
	path := filepath.Join(d.Root, string(build.Family), build.GitTag, build.Arch, build.BinaryName)
	sum, err := fileSHA256(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", capability.Build{}, ErrMissingBinary
		}
		return "", capability.Build{}, err
	}
	if err := d.Catalog.Authenticate(id, sum, "", ""); err != nil {
		return "", capability.Build{}, ErrDigestMismatch
	}
	return path, build, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
