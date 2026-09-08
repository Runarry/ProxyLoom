package apicontract

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

type PageInfo struct {
	Limit      int    `json:"limit"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type NodeListResponse struct {
	RequestID string         `json:"request_id"`
	Data      []NodeResource `json:"data"`
	Page      PageInfo       `json:"page"`
}

func NewNodeListResponse(requestID string, items []ir.Resource, page PageInfo) (NodeListResponse, error) {
	response := NodeListResponse{RequestID: safeRequestID(requestID), Data: make([]NodeResource, 0, len(items)), Page: page}
	for _, resource := range items {
		read, err := NewNodeReadResponse(requestID, resource)
		if err != nil {
			return NodeListResponse{}, err
		}
		response.Data = append(response.Data, read.Data)
	}
	return response, nil
}

type NodeCloneRequest struct {
	Name string `json:"name"`
}

type NodePrecondition struct {
	NodeID   ir.ID    `json:"node_id"`
	Revision Revision `json:"revision"`
}

type NodeBatchRequest struct {
	Operation     string             `json:"operation"`
	NodeIDs       []ir.ID            `json:"node_ids"`
	Preconditions []NodePrecondition `json:"preconditions"`
	Tags          []string           `json:"tags,omitempty"`
	Enabled       *bool              `json:"enabled,omitempty"`
}

type NodeBatchItem struct {
	NodeID     ir.ID      `json:"node_id"`
	HTTPStatus int        `json:"http_status"`
	Revision   *Revision  `json:"revision,omitempty"`
	Error      *ErrorBody `json:"error,omitempty"`
}

type NodeBatchResponse struct {
	RequestID string          `json:"request_id"`
	Data      []NodeBatchItem `json:"data"`
}

type MutationReceipt struct {
	ResourceID    ir.ID                 `json:"resource_id"`
	Revision      Revision              `json:"revision"`
	SecurityEpoch Revision              `json:"security_epoch"`
	Status        catalog.ReceiptStatus `json:"status"`
}

type MutationResponse struct {
	RequestID string          `json:"request_id"`
	Data      MutationReceipt `json:"data"`
}

type ResourceReference struct {
	SourceResourceID ir.ID           `json:"source_resource_id"`
	SourceRevision   Revision        `json:"source_revision"`
	SourceKind       ir.ResourceKind `json:"source_kind"`
	TargetResourceID ir.ID           `json:"target_resource_id"`
	TargetRevision   *Revision       `json:"target_revision,omitempty"`
	State            string          `json:"state"`
	FieldPath        string          `json:"field_path"`
}

type ReferenceListResponse struct {
	RequestID string              `json:"request_id"`
	Data      []ResourceReference `json:"data"`
	Page      PageInfo            `json:"page"`
}

type NodeReveal struct {
	ResourceID ir.ID         `json:"resource_id"`
	Revision   Revision      `json:"revision"`
	Auth       AuthInput     `json:"auth"`
	Security   SecurityInput `json:"security"`
}

type NodeRevealResponse struct {
	RequestID string     `json:"request_id"`
	Data      NodeReveal `json:"data"`
}

func (NodeReveal) Format(s fmt.State, _ rune)         { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (NodeReveal) LogValue() slog.Value               { return slog.StringValue("[REDACTED]") }
func (NodeRevealResponse) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (NodeRevealResponse) LogValue() slog.Value       { return slog.StringValue("[REDACTED]") }

// NewNodeRevealResponse is used only after the recent-authentication audit has
// committed. It is deliberately not a complete Resource or idempotency receipt.
func NewNodeRevealResponse(requestID string, resource ir.Resource) (NodeRevealResponse, error) {
	if resource.Validate() != nil {
		return NodeRevealResponse{}, NewError(InternalError)
	}
	node, ok := resource.Payload.(*ir.Node)
	if !ok {
		return NodeRevealResponse{}, NewError(InternalError)
	}
	response := NodeRevealResponse{RequestID: safeRequestID(requestID), Data: NodeReveal{ResourceID: resource.Metadata.ResourceID, Revision: Revision(resource.Metadata.Revision)}}
	for _, pair := range []struct{ source, target any }{{node.Auth, &response.Data.Auth}, {node.Security, &response.Data.Security}} {
		encoded, err := json.Marshal(pair.source)
		if err != nil {
			return NodeRevealResponse{}, NewError(InternalError)
		}
		err = json.Unmarshal(encoded, pair.target)
		clear(encoded)
		if err != nil {
			return NodeRevealResponse{}, NewError(InternalError)
		}
	}
	return response, nil
}
