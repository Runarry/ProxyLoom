package apicontract

import (
	"time"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

type ExportResourceRef struct {
	ResourceID ir.ID           `json:"resource_id"`
	Kind       ir.ResourceKind `json:"kind"`
	Revision   Revision        `json:"revision"`
}

type ExportRequest struct {
	Type           string              `json:"type"`
	Resources      []ExportResourceRef `json:"resources,omitempty"`
	Format         string              `json:"format"`
	IncludeSecrets bool                `json:"include_secrets"`
	PublicationID  ir.ID               `json:"publication_id,omitempty"`
	TargetKeys     []string            `json:"target_keys,omitempty"`
}

type ExportedArtifact struct {
	Filename        string `json:"filename"`
	MediaType       string `json:"media_type"`
	Content         string `json:"content"`
	ContainsSecrets bool   `json:"contains_secrets"`
}

func (ExportedArtifact) String() string { return "[REDACTED]" }

type ExportData struct {
	Artifacts []ExportedArtifact `json:"artifacts"`
	CreatedAt time.Time          `json:"created_at"`
}

type ExportResponse struct {
	RequestID string     `json:"request_id"`
	Data      ExportData `json:"data"`
}

func (ExportResponse) String() string { return "[REDACTED]" }
