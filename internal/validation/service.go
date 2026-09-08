// Package validation submits immutable compiler outputs to the durable queue.
// It is an API-side service; no HTTP upload route or core executor is exposed.
package validation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
)

var ErrInput = errors.New("validation_input_invalid")

type Queue interface {
	Enqueue(context.Context, jobs.EnqueueInput) (jobs.Job, error)
}

type Service struct {
	queue Queue
	cores *capability.Catalog
}

func New(queue Queue, cores *capability.Catalog) (*Service, error) {
	if queue == nil || cores == nil {
		return nil, ErrInput
	}
	return &Service{queue: queue, cores: cores}, nil
}

// Submit accepts an internal compiler artifact. Callers retain the compile
// batch's target-key -> job-ID mapping; the encrypted payload binds exact bytes,
// artifact snapshot and authenticated build. It never upgrades compatibility.
func (s *Service) Submit(ctx context.Context, scope, batch ir.ID, target ir.Target, artifact adapter.Artifact) (jobs.Job, error) {
	if s == nil || scope.Validate() != nil || (batch != "" && batch.Validate() != nil) || target.Validate() != nil || artifact.SnapshotID.Validate() != nil || artifact.TargetKey != target.Key {
		return jobs.Job{}, ErrInput
	}
	build, err := s.cores.Build(target.CoreBuildID)
	if err != nil || s.cores.Authenticate(target.CoreBuildID, target.CoreBuildSHA256, "linux", "") != nil || build.Family != target.CoreFamily || target.AdapterVersion != capability.AdapterVersion {
		return jobs.Job{}, ErrInput
	}
	wantType := "application/json"
	if target.Format == ir.MihomoYAML {
		wantType = "application/yaml"
	}
	if artifact.ContentType != wantType || len(artifact.Bytes) == 0 || len(artifact.Bytes) > 10<<20 {
		return jobs.Job{}, ErrInput
	}
	frozen := runnerprotocol.FrozenPayload{
		SchemaVersion: 1, Type: "config_validate",
		Core: runnerprotocol.CoreIdentity{CoreBuildID: build.ID, CoreFamily: build.Family, Version: build.Version,
			BuildSHA256: build.BinarySHA256, Platform: build.OS, Architecture: build.Arch, AdapterVersion: target.AdapterVersion},
		Artifact: runnerprotocol.Artifact{ArtifactID: artifact.SnapshotID, Format: target.Format,
			SHA256: runnerprotocol.Digest(artifact.Bytes), ByteLength: int64(len(artifact.Bytes)), ContentBase64: base64.StdEncoding.EncodeToString(artifact.Bytes)},
		Limits:          runnerprotocol.Limits{DurationMS: 15000, MaxBytes: 0},
		ExecutionPolicy: runnerprotocol.ExecutionPolicy{Network: "none", TerminationGraceMS: 2000, MemoryLimitBytes: 1 << 30, ProcessLimit: 32},
	}
	if runnerprotocol.ValidateConfigPayload(frozen) != nil {
		return jobs.Job{}, ErrInput
	}
	encoded, err := json.Marshal(frozen)
	if err != nil {
		return jobs.Job{}, ErrInput
	}
	defer clear(encoded)
	return s.queue.Enqueue(ctx, jobs.EnqueueInput{ScopeID: scope, BatchID: batch, Executor: jobs.Runner,
		Type: jobs.ConfigValidate, CoreBuildID: build.ID, Payload: encoded})
}
