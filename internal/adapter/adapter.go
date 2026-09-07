// Package adapter defines the boundary for future deterministic compilers and
// runner-owned command builders. It performs no compilation or execution.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

type CoreFamily = ir.CoreFamily
type Diagnostic = ir.Diagnostic
type FrozenInput = ir.FrozenInput
type Target = ir.Target

// Compile implementations must validate input and require target to equal the
// descriptor returned by input.Target(target.Key). All compilation decisions
// must depend only on frozen data and immutable compiler/build code. No clock,
// database or network lookup is permitted. Unknown or unsupported combinations
// return errors. The M0 skeleton may emit an artifact that records locked-but-
// unverified capabilities; publication must still refuse unverified output.
type Compiler interface {
	Compile(ctx context.Context, input FrozenInput, target Target) (Artifact, []Diagnostic, error)
}

// Artifact bytes contain credentials. The persistence layer encrypts them before
// storage. ContentHMAC is a keyed content digest, never an unkeyed secret hash.
type Artifact struct {
	SnapshotID  ir.ID
	TargetKey   string
	ContentType string
	Bytes       []byte
	ContentHMAC []byte
}

func (v Artifact) Clone() Artifact {
	v.Bytes = slices.Clone(v.Bytes)
	v.ContentHMAC = slices.Clone(v.ContentHMAC)
	return v
}
func (Artifact) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, "Artifact{[REDACTED]}") }
func (Artifact) LogValue() slog.Value           { return slog.StringValue("Artifact{[REDACTED]}") }

// JobWorkspace is allocated and permission-checked by the Runner. No HTTP DTO
// may populate it. The adapter generates flags only for its pinned core build.
type JobWorkspace struct {
	Directory      string
	ConfigFilename string
}

func (v JobWorkspace) Validate() error {
	if v.Directory == "" || !filepath.IsAbs(v.Directory) || filepath.Clean(v.Directory) != v.Directory {
		return errors.New("job directory must be an absolute cleaned path")
	}
	if v.ConfigFilename == "" || filepath.Base(v.ConfigFilename) != v.ConfigFilename || strings.Contains(v.ConfigFilename, "..") {
		return errors.New("config filename must be a basename inside the job directory")
	}
	if strings.ContainsRune(v.ConfigFilename, 0) {
		return errors.New("config filename contains a forbidden NUL")
	}
	return nil
}

// CommandSpec selects a registry executable, never an executable path or shell
// command. Args go directly to exec.Command; no interpolation or shell is used.
// Environment allowlists, argument allowlists, budgets and process cleanup are
// obligations of the future Runner framework, not an adapter escape hatch.
type CommandSpec struct {
	ExecutableID ir.ID
	Args         []string
	WorkingDir   string
}

// Validate checks a generated spec against the Runner's own trusted allocation.
// It does not prove filesystem containment in the presence of symlinks or grant
// execution authorization; those checks belong to the runner allocation layer.
func (v CommandSpec) Validate(expectedExecutable ir.ID, jobDirectory string) error {
	if err := expectedExecutable.Validate(); err != nil {
		return errors.New("invalid registry executable identity")
	}
	if v.ExecutableID != expectedExecutable {
		return errors.New("command executable does not match the pinned build")
	}
	if !filepath.IsAbs(jobDirectory) || !filepath.IsAbs(v.WorkingDir) || filepath.Clean(jobDirectory) != filepath.Clean(v.WorkingDir) {
		return errors.New("command directory does not match the runner allocation")
	}
	for _, arg := range v.Args {
		if strings.ContainsRune(arg, 0) {
			return errors.New("command argument contains a forbidden NUL")
		}
	}
	return nil
}

func (v CommandSpec) Clone() CommandSpec { v.Args = slices.Clone(v.Args); return v }

type RunnerAdapter interface {
	Family() CoreFamily
	ValidateSpec(workspace JobWorkspace) (CommandSpec, error)
	RunSpec(workspace JobWorkspace) (CommandSpec, error)
	// RedactLog must return a fresh, sanitized buffer without credentials.
	RedactLog(line []byte) []byte
}

// CoreAdapter preserves the design document's name for the runner-side boundary.
type CoreAdapter = RunnerAdapter
