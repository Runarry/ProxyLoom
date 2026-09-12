// Package subscriptions defines publication metadata and frozen selection.
package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"log/slog"
	"time"
)

type Revision = apicontract.Revision
type Counter = apicontract.Counter

var (
	ErrObsolete     = errors.New("COMPILE_OBSOLETE")
	ErrBlocked      = errors.New("PUBLICATION_BLOCKED")
	ErrConfirmation = errors.New("PUBLICATION_CONFIRMATION_REQUIRED")
	ErrToken        = errors.New("SUBSCRIPTION_NOT_FOUND")
	ErrNotReady     = errors.New("SUBSCRIPTION_NOT_READY")
)

type Actor struct {
	ScopeID, ID ir.ID
	Key         string
}
type CompileRequest struct {
	TargetKeys []string `json:"target_keys"`
}
type PublishRequest struct {
	BatchID              ir.ID   `json:"batch_id"`
	ExpectedGeneration   Counter `json:"expected_generation"`
	EffectivePreviewHash string  `json:"effective_preview_hash"`
	Confirmation         struct {
		Acknowledged bool `json:"acknowledged"`
	} `json:"confirmation"`
}
type RollbackRequest struct {
	PublicationID      ir.ID   `json:"publication_id"`
	ExpectedGeneration Counter `json:"expected_generation"`
}
type TokenRequest struct {
	Name           string     `json:"name"`
	AllowedTargets []string   `json:"allowed_targets"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}
type Dependency struct {
	ResourceID           ir.ID           `json:"resource_id"`
	Kind                 ir.ResourceKind `json:"kind"`
	Revision             Revision        `json:"revision"`
	SecurityEpoch        Revision        `json:"security_epoch"`
	Inclusion            string          `json:"inclusion"`
	RequiredBy           []ir.ID         `json:"required_by"`
	CredentialCategories []string        `json:"credential_categories"`
}
type Output struct {
	TargetKey                string          `json:"target_key"`
	CoreBuildID              ir.ID           `json:"core_build_id"`
	CoreBuildSHA256          string          `json:"core_build_sha256"`
	AdapterVersion           string          `json:"adapter_version"`
	ClientPresetID           ir.ID           `json:"client_preset_id"`
	ClientPresetRevision     Revision        `json:"client_preset_revision"`
	Format                   ir.OutputFormat `json:"format"`
	State                    string          `json:"state"`
	ArtifactID               ir.ID           `json:"artifact_id,omitempty"`
	ValidationJobID          ir.ID           `json:"validation_job_id,omitempty"`
	ValidationVerdict        string          `json:"validation_verdict,omitempty"`
	Diagnostics              []ir.Diagnostic `json:"diagnostics"`
	PreviewTruncated         bool            `json:"preview_truncated,omitempty"`
	PreviousPreviewTruncated bool            `json:"previous_preview_truncated,omitempty"`
	Preview                  string          `json:"preview,omitempty"`
	PreviousPreview          string          `json:"previous_preview,omitempty"`
	Changed                  bool            `json:"changed"`
}
type Batch struct {
	BatchID              ir.ID           `json:"batch_id"`
	Revision             Revision        `json:"revision"`
	SubscriptionID       ir.ID           `json:"subscription_id"`
	SubscriptionRevision Revision        `json:"subscription_revision"`
	CatalogRevision      Revision        `json:"catalog_revision"`
	State                string          `json:"state"`
	JobIDs               []ir.ID         `json:"job_ids"`
	EffectivePreviewHash string          `json:"effective_preview_hash,omitempty"`
	Dependencies         []Dependency    `json:"dependencies"`
	Outputs              []Output        `json:"outputs"`
	Diagnostics          []ir.Diagnostic `json:"diagnostics"`
	BlockingReasons      []ir.Diagnostic `json:"blocking_reasons"`
	CreatedAt            time.Time       `json:"created_at"`
}
type Head struct {
	Generation      Counter         `json:"generation"`
	PublicationID   ir.ID           `json:"publication_id,omitempty"`
	State           string          `json:"state"`
	BlockingReasons []ir.Diagnostic `json:"blocking_reasons"`
}
type PublishedTarget struct {
	TargetKey            string          `json:"target_key"`
	ArtifactID           ir.ID           `json:"artifact_id"`
	CoreBuildID          ir.ID           `json:"core_build_id"`
	CoreBuildSHA256      string          `json:"core_build_sha256"`
	ClientPresetID       ir.ID           `json:"client_preset_id"`
	ClientPresetRevision Revision        `json:"client_preset_revision"`
	Format               ir.OutputFormat `json:"format"`
	ValidationJobID      ir.ID           `json:"validation_job_id"`
}
type Publication struct {
	PublicationID       ir.ID             `json:"publication_id"`
	SubscriptionID      ir.ID             `json:"subscription_id"`
	Generation          Revision          `json:"generation"`
	BatchID             ir.ID             `json:"batch_id"`
	SourcePublicationID ir.ID             `json:"source_publication_id,omitempty"`
	CreatedAt           time.Time         `json:"created_at"`
	CreatedBy           ir.ID             `json:"created_by"`
	State               string            `json:"state"`
	Targets             []PublishedTarget `json:"targets"`
	Dependencies        []Dependency      `json:"dependencies"`
	BlockingReasons     []ir.Diagnostic   `json:"blocking_reasons"`
}
type TokenMetadata struct {
	TokenID        ir.ID      `json:"token_id"`
	SubscriptionID ir.ID      `json:"subscription_id"`
	Revision       Revision   `json:"revision"`
	Name           string     `json:"name"`
	AllowedTargets []string   `json:"allowed_targets"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
	State          string     `json:"state"`
}
type TokenIssue struct {
	Metadata TokenMetadata `json:"metadata"`
	Replayed bool          `json:"replayed"`
	Token    string        `json:"token,omitempty"`
}

func (TokenIssue) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "TokenIssue{[REDACTED]}") }
func (TokenIssue) LogValue() slog.Value       { return slog.StringValue("TokenIssue{[REDACTED]}") }
func (TokenIssue) String() string             { return "TokenIssue{[REDACTED]}" }

type Core struct {
	runnerprotocol.CoreIdentity
	Revision         Revision   `json:"revision"`
	Enabled          bool       `json:"enabled"`
	RegisteredAt     time.Time  `json:"registered_at"`
	DisabledAt       *time.Time `json:"disabled_at,omitempty"`
	CapabilityStatus string     `json:"capability_status"`
}
type Download struct {
	Bytes       []byte
	ContentType string
}

func (Download) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "Download{[REDACTED]}") }
func (Download) LogValue() slog.Value       { return slog.StringValue("Download{[REDACTED]}") }
func (Download) String() string             { return "Download{[REDACTED]}" }

type Repository interface {
	Compile(context.Context, Actor, ir.ID, int64, CompileRequest) (Batch, error)
	GetBatch(context.Context, Actor, ir.ID) (Batch, error)
	Publish(context.Context, Actor, ir.ID, int64, PublishRequest) (Publication, error)
	Rollback(context.Context, Actor, ir.ID, int64, RollbackRequest) (Publication, error)
	Head(context.Context, ir.ID, ir.ID) (Head, error)
	Publications(context.Context, ir.ID, ir.ID, ir.ID, int) ([]Publication, error)
	IssueToken(context.Context, Actor, ir.ID, TokenRequest) (TokenIssue, error)
	Tokens(context.Context, ir.ID, ir.ID, ir.ID, int) ([]TokenMetadata, error)
	RevokeToken(context.Context, Actor, ir.ID, int64) (TokenMetadata, error)
	Download(context.Context, string, string) (Download, error)
	Cores(context.Context) ([]Core, error)
	DisableCore(context.Context, Actor, ir.ID, int64) (Core, error)
	Export(context.Context, Actor, ir.ID, []string, ir.OutputFormat, bool) ([]apicontract.ExportedArtifact, error)
}
