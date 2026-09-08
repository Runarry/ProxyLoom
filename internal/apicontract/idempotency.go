package apicontract

import (
	"fmt"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var routeKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,95}$`)

// IdempotencyMetadata carries authenticated context and a fixed operation ID.
// ScopeID/PrincipalID come from authentication, never from user JSON. RouteKey is
// a handler constant, not a URL containing identifiers or a subscription token.
type IdempotencyMetadata struct {
	ScopeID     ir.ID
	PrincipalID ir.ID
	RouteKey    string
	Key         string
}

func (IdempotencyMetadata) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (IdempotencyMetadata) LogValue() slog.Value       { return slog.StringValue("[REDACTED]") }

func ReadIdempotency(header http.Header, scopeID, principalID ir.ID, routeKey string) (IdempotencyMetadata, error) {
	values := header.Values("Idempotency-Key")
	if len(values) != 1 || !idempotencyKeyPattern.MatchString(values[0]) {
		return IdempotencyMetadata{}, NewError(MalformedRequest)
	}
	if scopeID.Validate() != nil || principalID.Validate() != nil || !routeKeyPattern.MatchString(routeKey) {
		return IdempotencyMetadata{}, NewError(InternalError)
	}
	return IdempotencyMetadata{ScopeID: scopeID, PrincipalID: principalID, RouteKey: routeKey, Key: values[0]}, nil
}

func (metadata IdempotencyMetadata) Request(data []byte, schemaName string) (catalog.IdempotencyRequest, error) {
	if metadata.ScopeID.Validate() != nil || metadata.PrincipalID.Validate() != nil || !routeKeyPattern.MatchString(metadata.RouteKey) {
		return catalog.IdempotencyRequest{}, NewError(InternalError)
	}
	if !idempotencyKeyPattern.MatchString(metadata.Key) {
		return catalog.IdempotencyRequest{}, NewError(MalformedRequest)
	}
	canonical, err := CanonicalRequest(data, schemaName)
	if err != nil {
		return catalog.IdempotencyRequest{}, err
	}
	return catalog.IdempotencyRequest{ScopeID: metadata.ScopeID, PrincipalID: metadata.PrincipalID, RouteKey: metadata.RouteKey, Key: metadata.Key, CanonicalRequest: canonical}, nil
}

// IdempotencyReceipt maps storage's allowlisted metadata to lossless API wire
// counters. It cannot contain payload bytes or one-time-issued token secrets.
type IdempotencyReceipt struct {
	Replayed    bool                  `json:"replayed"`
	HTTPStatus  int                   `json:"http_status"`
	ResourceID  ir.ID                 `json:"resource_id,omitempty"`
	Revision    Revision              `json:"revision,omitempty"`
	OperationID ir.ID                 `json:"operation_id,omitempty"`
	Status      catalog.ReceiptStatus `json:"status"`
}
type IdempotencyReceiptResponse struct {
	RequestID string             `json:"request_id"`
	Data      IdempotencyReceipt `json:"data"`
}

func NewIdempotencyReceipt(requestID string, result catalog.IdempotencyResult) (IdempotencyReceiptResponse, error) {
	if result.Receipt.Validate() != nil {
		return IdempotencyReceiptResponse{}, NewError(InternalError)
	}
	r := result.Receipt
	return IdempotencyReceiptResponse{RequestID: safeRequestID(requestID), Data: IdempotencyReceipt{Replayed: result.Replayed, HTTPStatus: r.HTTPStatus, ResourceID: r.ResourceID, Revision: Revision(r.Revision), OperationID: r.OperationID, Status: r.Status}}, nil
}
