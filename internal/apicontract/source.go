package apicontract

import (
	"time"

	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/source"
)

type SourceAuthWrite struct {
	Kind     source.AuthKind `json:"kind"`
	Token    ir.Secret       `json:"token,omitempty"`
	Username ir.Secret       `json:"username,omitempty"`
	Password ir.Secret       `json:"password,omitempty"`
}

type SourceWrite struct {
	SchemaVersion int                  `json:"schema_version"`
	URL           ir.Secret            `json:"url"`
	Format        source.Format        `json:"format"`
	Auth          SourceAuthWrite      `json:"auth"`
	RefreshPolicy source.RefreshPolicy `json:"refresh_policy"`
	FetchLimits   source.FetchLimits   `json:"fetch_limits"`
}

type SourceCreateRequest struct {
	Name    string      `json:"name"`
	Tags    []string    `json:"tags,omitempty"`
	Enabled *bool       `json:"enabled,omitempty"`
	Source  SourceWrite `json:"source"`
}

type SourceAuthPatch struct {
	Kind     source.AuthKind `json:"kind"`
	Token    SecretPatch     `json:"token,omitzero"`
	Username SecretPatch     `json:"username,omitzero"`
	Password SecretPatch     `json:"password,omitzero"`
}

type SourcePatch struct {
	URL           *ir.Secret            `json:"url,omitempty"`
	Format        *source.Format        `json:"format,omitempty"`
	Auth          *SourceAuthPatch      `json:"auth,omitempty"`
	RefreshPolicy *source.RefreshPolicy `json:"refresh_policy,omitempty"`
	FetchLimits   *source.FetchLimits   `json:"fetch_limits,omitempty"`
}

type SourcePatchRequest struct {
	Name    *string      `json:"name,omitempty"`
	Tags    *[]string    `json:"tags,omitempty"`
	Enabled *bool        `json:"enabled,omitempty"`
	Source  *SourcePatch `json:"source,omitempty"`
}

type SourceAuthRedacted struct {
	Kind        source.AuthKind `json:"kind"`
	HasToken    *bool           `json:"has_token,omitempty"`
	HasUsername *bool           `json:"has_username,omitempty"`
	HasPassword *bool           `json:"has_password,omitempty"`
}

type SourceRedacted struct {
	SchemaVersion        int                  `json:"schema_version"`
	URLDisplay           string               `json:"url_display"`
	HasURL               bool                 `json:"has_url"`
	Format               source.Format        `json:"format"`
	Auth                 SourceAuthRedacted   `json:"auth"`
	RefreshPolicy        source.RefreshPolicy `json:"refresh_policy"`
	FetchLimits          source.FetchLimits   `json:"fetch_limits"`
	BindingRevision      Revision             `json:"binding_revision"`
	LastSuccessAt        *time.Time           `json:"last_success_at,omitempty"`
	LastJobID            ir.ID                `json:"last_job_id,omitempty"`
	LatestPreviewBatchID ir.ID                `json:"latest_preview_batch_id,omitempty"`
	LastError            *ErrorBody           `json:"last_error,omitempty"`
}

type SourceItem struct {
	ID              ir.ID  `json:"id"`
	State           string `json:"state"`
	Name            string `json:"name"`
	ExternalKey     string `json:"external_key,omitempty"`
	NodeID          ir.ID  `json:"node_id,omitempty"`
	SuggestedNodeID ir.ID  `json:"suggested_node_id,omitempty"`
}

type SourceResource struct {
	Metadata ResourceMetadata `json:"metadata"`
	Source   SourceRedacted   `json:"source"`
	Items    []SourceItem     `json:"items,omitempty"`
}

type SourceResponse struct {
	RequestID string         `json:"request_id"`
	Data      SourceResource `json:"data"`
}

type SourceListResponse struct {
	RequestID string           `json:"request_id"`
	Data      []SourceResource `json:"data"`
	Page      PageInfo         `json:"page"`
}

func (request SourceCreateRequest) Config() (source.Config, error) {
	if err := ValidateDTO("SourceCreateRequest", request); err != nil {
		return source.Config{}, err
	}
	cfg := source.Config{SchemaVersion: request.Source.SchemaVersion, URL: request.Source.URL, Format: request.Source.Format,
		Auth:          source.Auth{Kind: request.Source.Auth.Kind, Token: request.Source.Auth.Token, Username: request.Source.Auth.Username, Password: request.Source.Auth.Password},
		RefreshPolicy: request.Source.RefreshPolicy, FetchLimits: request.Source.FetchLimits, BindingRevision: 1}
	if err := cfg.Validate(); err != nil {
		return source.Config{}, NewError(ValidationFailed)
	}
	return cfg, nil
}

func (request SourcePatchRequest) Merge(old source.Document) (source.Config, string, []string, bool, error) {
	if err := ValidateDTO("SourcePatchRequest", request); err != nil {
		return source.Config{}, "", nil, false, err
	}
	cfg := old.Source
	name, tags, enabled := old.Metadata.Name, append([]string{}, old.Metadata.Tags...), old.Metadata.Enabled
	if request.Name != nil {
		name = *request.Name
	}
	if request.Tags != nil {
		tags = append([]string{}, *request.Tags...)
	}
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	if request.Source != nil {
		if request.Source.URL != nil {
			if *request.Source.URL == "" {
				return source.Config{}, "", nil, false, NewError(ValidationFailed)
			}
			cfg.URL = *request.Source.URL
		}
		if request.Source.Format != nil {
			cfg.Format = *request.Source.Format
		}
		if request.Source.RefreshPolicy != nil {
			cfg.RefreshPolicy = *request.Source.RefreshPolicy
		}
		if request.Source.FetchLimits != nil {
			cfg.FetchLimits = *request.Source.FetchLimits
		}
		if request.Source.Auth != nil {
			cfg.Auth.Kind = request.Source.Auth.Kind
			switch cfg.Auth.Kind {
			case source.AuthNone:
				cfg.Auth.Token, cfg.Auth.Username, cfg.Auth.Password = "", "", ""
			case source.AuthBearer:
				cfg.Auth.Token = request.Source.Auth.Token.apply(old.Source.Auth.Token)
				cfg.Auth.Username, cfg.Auth.Password = "", ""
			case source.AuthBasic:
				cfg.Auth.Username = request.Source.Auth.Username.apply(old.Source.Auth.Username)
				cfg.Auth.Password = request.Source.Auth.Password.apply(old.Source.Auth.Password)
				cfg.Auth.Token = ""
			}
		}
	}
	if err := cfg.Validate(); err != nil {
		return source.Config{}, "", nil, false, NewError(ValidationFailed)
	}
	return cfg, name, tags, enabled, nil
}

func NewSourceResponse(requestID string, document source.Document) (SourceResponse, error) {
	display, err := source.DisplayURL(string(document.Source.URL))
	if err != nil {
		return SourceResponse{}, NewError(InternalError)
	}
	if _, err := source.Canonical(document); err != nil {
		return SourceResponse{}, NewError(InternalError)
	}
	read := SourceRedacted{SchemaVersion: document.Source.SchemaVersion, URLDisplay: display, HasURL: document.Source.URL != "",
		Format: document.Source.Format, RefreshPolicy: document.Source.RefreshPolicy, FetchLimits: document.Source.FetchLimits,
		BindingRevision: Revision(document.Source.BindingRevision), LastSuccessAt: document.Source.LastSuccessAt, LastJobID: document.Source.LastJobID,
		LatestPreviewBatchID: document.Source.LatestPreviewBatchID}
	switch document.Source.Auth.Kind {
	case source.AuthNone:
		read.Auth = SourceAuthRedacted{Kind: source.AuthNone}
	case source.AuthBearer:
		has := document.Source.Auth.Token != ""
		read.Auth = SourceAuthRedacted{Kind: source.AuthBearer, HasToken: &has}
	case source.AuthBasic:
		user, pass := document.Source.Auth.Username != "", document.Source.Auth.Password != ""
		read.Auth = SourceAuthRedacted{Kind: source.AuthBasic, HasUsername: &user, HasPassword: &pass}
	}
	if document.Source.LastError != nil {
		read.LastError = &ErrorBody{Code: Code(document.Source.LastError.Code), Message: document.Source.LastError.Message, Details: []Detail{}}
	}
	return SourceResponse{RequestID: safeRequestID(requestID), Data: SourceResource{Metadata: metadata(document.Metadata), Source: read}}, nil
}

func NewSourceListResponse(requestID string, items []source.Document, page PageInfo) (SourceListResponse, error) {
	response := SourceListResponse{RequestID: safeRequestID(requestID), Data: make([]SourceResource, 0, len(items)), Page: page}
	for _, document := range items {
		read, err := NewSourceResponse(requestID, document)
		if err != nil {
			return SourceListResponse{}, err
		}
		response.Data = append(response.Data, read.Data)
	}
	return response, nil
}

func EnabledTags(request SourceCreateRequest) (bool, []string) {
	tags := request.Tags
	if tags == nil {
		tags = []string{}
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	return enabled, tags
}
