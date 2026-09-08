package validation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
)

type captureQueue struct {
	input jobs.EnqueueInput
	calls int
}

func (q *captureQueue) Enqueue(_ context.Context, input jobs.EnqueueInput) (jobs.Job, error) {
	q.calls++
	q.input = input
	q.input.Payload = append([]byte(nil), input.Payload...)
	return jobs.Job{ID: jobs.NewID(), CoreBuildID: input.CoreBuildID, BatchID: input.BatchID}, nil
}

func TestSubmissionBindsLockedBuildAndExactArtifact(t *testing.T) {
	cores, err := capability.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, build := range cores.Builds() {
		t.Run(string(build.Family)+"/"+build.Arch, func(t *testing.T) {
			queue := &captureQueue{}
			service, err := New(queue, cores)
			if err != nil {
				t.Fatal(err)
			}
			format := ir.XrayJSON
			contentType := adapter.JSONContentType
			if build.Family == ir.SingBox {
				format = ir.SingBoxJSON
			}
			if build.Family == ir.Mihomo {
				format = ir.MihomoYAML
				contentType = adapter.YAMLContentType
			}
			target := ir.Target{Key: string(build.Family) + "-default", CoreFamily: build.Family, CoreBuildID: build.ID, CoreBuildSHA256: build.BinarySHA256, AdapterVersion: capability.AdapterVersion, ClientPresetID: jobs.NewID(), ClientPresetRevision: 1, Format: format}
			artifact := adapter.Artifact{SnapshotID: jobs.NewID(), TargetKey: target.Key, ContentType: contentType, Bytes: []byte("{}")}
			scope, batch := jobs.NewID(), jobs.NewID()
			job, err := service.Submit(context.Background(), scope, batch, target, artifact)
			if err != nil {
				t.Fatal(err)
			}
			var payload runnerprotocol.FrozenPayload
			if json.Unmarshal(queue.input.Payload, &payload) != nil || runnerprotocol.ValidateConfigPayload(payload) != nil {
				t.Fatal("invalid durable validation payload")
			}
			if job.BatchID != batch || queue.input.ScopeID != scope || payload.Core.CoreBuildID != build.ID || payload.Artifact.SHA256 != runnerprotocol.Digest(artifact.Bytes) || payload.Artifact.ArtifactID != artifact.SnapshotID || payload.QuotaReservationID != "" || payload.Limits.MaxBytes != 0 {
				t.Fatal("artifact identity or offline budget was lost")
			}
			artifact.Bytes[0] = '['
			if runnerprotocol.ValidateConfigPayload(payload) != nil {
				t.Fatal("caller mutated durable bytes")
			}
			target.CoreBuildSHA256 = strings.Repeat("0", 64)
			if _, err = service.Submit(context.Background(), scope, batch, target, artifact); err != ErrInput || queue.calls != 1 {
				t.Fatal("untrusted build reached queue")
			}
		})
	}
}
