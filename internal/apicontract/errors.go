// Package apicontract provides HTTP boundary components and explicit API DTOs.
// It does not mount business routes, run cores or persist idempotency records.
package apicontract

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/Runarry/ProxyLoom/api"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

type Code string

const (
	CompileObsolete             Code = "COMPILE_OBSOLETE"
	PublicationBlocked          Code = "PUBLICATION_BLOCKED"
	PreviewConfirmationRequired Code = "PUBLICATION_CONFIRMATION_REQUIRED"
	MalformedRequest            Code = "MALFORMED_REQUEST"
	UnknownField                Code = "UNKNOWN_FIELD"
	DuplicateField              Code = "DUPLICATE_FIELD"
	AuthRequired                Code = "AUTH_REQUIRED"
	PermissionDenied            Code = "PERMISSION_DENIED"
	ReauthRequired              Code = "REAUTH_REQUIRED"
	ResourceNotFound            Code = "RESOURCE_NOT_FOUND"
	StateConflict               Code = "STATE_CONFLICT"
	IdempotencyConflict         Code = "IDEMPOTENCY_CONFLICT"
	LeaseLost                   Code = "LEASE_LOST"
	RevisionMismatch            Code = "REVISION_MISMATCH"
	InputLimitExceeded          Code = "INPUT_LIMIT_EXCEEDED"
	ValidationFailed            Code = "VALIDATION_FAILED"
	PreconditionRequired        Code = "PRECONDITION_REQUIRED"
	RateLimited                 Code = "RATE_LIMITED"
	ServiceUnavailable          Code = "SERVICE_UNAVAILABLE"
	InternalError               Code = "INTERNAL_ERROR"
)

var errorDefinitions = map[Code]struct {
	status  int
	message string
}{
	CompileObsolete:             {409, "The compiled input is obsolete."},
	PublicationBlocked:          {422, "The publication is blocked by its current safety state."},
	PreviewConfirmationRequired: {409, "View and confirm this exact publication preview."},
	MalformedRequest:            {400, "The request does not match the API contract."},
	UnknownField:                {400, "A field is not part of the API contract."},
	DuplicateField:              {400, "Duplicate object fields are forbidden."},
	AuthRequired:                {401, "Authentication is required."},
	PermissionDenied:            {403, "The operation is not permitted."},
	ReauthRequired:              {403, "Recent authentication is required."},
	ResourceNotFound:            {404, "The resource was not found."},
	StateConflict:               {409, "The operation conflicts with the current state."},
	IdempotencyConflict:         {409, "The idempotency key was used for a different request."},
	LeaseLost:                   {409, "The job lease is no longer valid."},
	RevisionMismatch:            {412, "The resource revision has changed."},
	InputLimitExceeded:          {413, "The request exceeds the input limit."},
	ValidationFailed:            {422, "The merged resource is not valid."},
	PreconditionRequired:        {428, "A resource revision precondition is required."},
	RateLimited:                 {429, "The request exceeds the permitted budget."},
	ServiceUnavailable:          {503, "The operation is temporarily unavailable."},
	InternalError:               {500, "The operation could not be completed."},
}

type Detail struct {
	FieldPath  string `json:"field_path"`
	ResourceID ir.ID  `json:"resource_id,omitempty"`
}

type ErrorBody struct {
	Code    Code     `json:"code"`
	Message string   `json:"message"`
	Details []Detail `json:"details"`
}

type ErrorResponse struct {
	Error     ErrorBody `json:"error"`
	RequestID string    `json:"request_id"`
}

// Error contains only an allowlisted code and sanitized details. Neither an
// underlying error nor a caller-provided message can reach its JSON response.
type Error struct {
	code    Code
	details []Detail
}

func NewError(code Code, details ...Detail) *Error {
	if _, ok := errorDefinitions[code]; !ok {
		code = InternalError
	}
	out := &Error{code: code, details: make([]Detail, 0, len(details))}
	for _, detail := range details {
		if len(out.details) == 16 {
			break
		}
		detail.FieldPath = safePointer(detail.FieldPath)
		if detail.ResourceID.Validate() != nil {
			detail.ResourceID = ""
		}
		out.details = append(out.details, detail)
	}
	return out
}

func (e *Error) Error() string { return string(e.Code()) }
func (e *Error) Code() Code {
	if e == nil {
		return InternalError
	}
	if _, ok := errorDefinitions[e.code]; !ok {
		return InternalError
	}
	return e.code
}
func (e *Error) HTTPStatus() int { return errorDefinitions[e.Code()].status }
func (e *Error) Response(requestID string) ErrorResponse {
	clean := NewError(e.Code(), e.details...)
	return ErrorResponse{Error: ErrorBody{Code: clean.code, Message: errorDefinitions[clean.code].message, Details: clean.details}, RequestID: safeRequestID(requestID)}
}

func AsError(err error) *Error {
	var boundary *Error
	if errors.As(err, &boundary) {
		return NewError(boundary.Code(), boundary.details...)
	}
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		return NewError(ResourceNotFound)
	case errors.Is(err, catalog.ErrRevisionConflict):
		return NewError(RevisionMismatch)
	case errors.Is(err, catalog.ErrIdempotencyConflict):
		return NewError(IdempotencyConflict)
	case errors.Is(err, catalog.ErrInvalidReference), errors.Is(err, catalog.ErrInvalidInput):
		return NewError(ValidationFailed)
	case errors.Is(err, catalog.ErrUnavailable), errors.Is(err, catalog.ErrCrypto):
		return NewError(ServiceUnavailable)
	case errors.Is(err, catalog.ErrWrapConflict):
		return NewError(StateConflict)
	}
	var diagnostics ir.Diagnostics
	if errors.As(err, &diagnostics) {
		details := make([]Detail, 0, len(diagnostics))
		for _, diagnostic := range diagnostics {
			details = append(details, Detail{FieldPath: diagnostic.FieldPath, ResourceID: diagnostic.ResourceID})
		}
		return NewError(ValidationFailed, details...)
	}
	return NewError(InternalError)
}

func WriteError(w http.ResponseWriter, requestID string, err error) {
	failure := AsError(err)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Request-ID", safeRequestID(requestID))
	w.WriteHeader(failure.HTTPStatus())
	_ = json.NewEncoder(w).Encode(failure.Response(requestID))
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func safeRequestID(value string) string {
	if !requestIDPattern.MatchString(value) {
		return "unavailable"
	}
	return value
}

func safePointer(value string) string {
	if value == "" || !strings.HasPrefix(value, "/") || len(value) > 1024 {
		return ""
	}
	out := ""
	for _, part := range strings.Split(value[1:], "/") {
		if !api.KnownField(part) {
			index, err := strconv.ParseUint(part, 10, 32)
			if err != nil || strconv.FormatUint(index, 10) != part {
				break
			}
		}
		out += "/" + part
	}
	return out
}
