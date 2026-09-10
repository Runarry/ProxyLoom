package apicontract

import (
	"encoding/json"
	"errors"
	"slices"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func routingError(err error, prefix string) error {
	var diagnostics ir.Diagnostics
	if errors.As(err, &diagnostics) {
		details := make([]Detail, 0, len(diagnostics))
		for _, diagnostic := range diagnostics {
			details = append(details, Detail{FieldPath: prefix + diagnostic.FieldPath, ResourceID: diagnostic.ResourceID})
		}
		return NewError(ValidationFailed, details...)
	}
	return err
}

type RoutingProfileCreateRequest struct {
	Name           string            `json:"name"`
	Tags           []string          `json:"tags,omitempty"`
	Enabled        *bool             `json:"enabled,omitempty"`
	RoutingProfile ir.RoutingProfile `json:"routing_profile"`
}

func (r *RoutingProfileCreateRequest) UnmarshalJSON(data []byte) error {
	type plain RoutingProfileCreateRequest
	var next plain
	if err := json.Unmarshal(data, &next); err != nil {
		return routingError(err, "/routing_profile")
	}
	*r = RoutingProfileCreateRequest(next)
	return nil
}

func (r RoutingProfileCreateRequest) Input() (catalog.CreateInput, error) {
	if err := ValidateDTO("RoutingProfileCreateRequest", r); err != nil {
		return catalog.CreateInput{}, err
	}
	if err := r.RoutingProfile.Validate(); err != nil {
		return catalog.CreateInput{}, routingError(err, "/routing_profile")
	}
	payload := r.RoutingProfile.Clone()
	enabled := true
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	return catalog.CreateInput{Name: r.Name, Tags: append([]string{}, r.Tags...), Enabled: enabled, Payload: &payload}, nil
}

type RoutingProfilePatch struct {
	Rules                *[]ir.RoutingRule        `json:"rules,omitempty"`
	Final                *ir.TargetRef            `json:"final,omitempty"`
	DomainResolutionMode *ir.DomainResolutionMode `json:"domain_resolution_mode,omitempty"`
}

// Decode replacement rules through a complete typed profile to preserve nested
// semantic diagnostic paths (including their rule index).
func (p *RoutingProfilePatch) UnmarshalJSON(data []byte) error {
	type plain RoutingProfilePatch
	var raw struct {
		Rules                json.RawMessage          `json:"rules"`
		Final                *ir.TargetRef            `json:"final"`
		DomainResolutionMode *ir.DomainResolutionMode `json:"domain_resolution_mode"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	next := plain{Final: raw.Final, DomainResolutionMode: raw.DomainResolutionMode}
	if len(raw.Rules) > 0 {
		profileJSON, err := json.Marshal(map[string]any{"schema_version": ir.SchemaVersion, "rules": raw.Rules,
			"final": map[string]string{"type": "builtin", "builtin": "reject"}, "domain_resolution_mode": "preserve_domain"})
		if err != nil {
			return err
		}
		var profile ir.RoutingProfile
		if err := json.Unmarshal(profileJSON, &profile); err != nil {
			return err
		}
		next.Rules = &profile.Rules
	}
	*p = RoutingProfilePatch(next)
	return nil
}

type RoutingProfilePatchRequest struct {
	Name           *string              `json:"name,omitempty"`
	Tags           *[]string            `json:"tags,omitempty"`
	Enabled        *bool                `json:"enabled,omitempty"`
	RoutingProfile *RoutingProfilePatch `json:"routing_profile,omitempty"`
}

func (r *RoutingProfilePatchRequest) UnmarshalJSON(data []byte) error {
	type plain RoutingProfilePatchRequest
	var next plain
	if err := json.Unmarshal(data, &next); err != nil {
		return routingError(err, "/routing_profile")
	}
	*r = RoutingProfilePatchRequest(next)
	return nil
}

func (r RoutingProfilePatchRequest) Merge(old ir.Resource) (catalog.UpdateInput, error) {
	if err := ValidateDTO("RoutingProfilePatchRequest", r); err != nil {
		return catalog.UpdateInput{}, err
	}
	if old.Validate() != nil {
		return catalog.UpdateInput{}, NewError(InternalError)
	}
	payload, ok := old.Payload.(*ir.RoutingProfile)
	if !ok {
		return catalog.UpdateInput{}, NewError(ValidationFailed)
	}
	next := payload.Clone()
	if patch := r.RoutingProfile; patch != nil {
		if patch.Rules != nil {
			next.Rules = slices.Clone(*patch.Rules)
		}
		if patch.Final != nil {
			next.Final = *patch.Final
		}
		if patch.DomainResolutionMode != nil {
			next.DomainResolutionMode = *patch.DomainResolutionMode
		}
	}
	if err := next.Validate(); err != nil {
		return catalog.UpdateInput{}, routingError(err, "/routing_profile")
	}
	next = next.Clone()
	return routingUpdateInput(old, r.Name, r.Tags, r.Enabled, &next), nil
}

func routingUpdateInput(old ir.Resource, name *string, tags *[]string, enabled *bool, payload ir.ResourcePayload) catalog.UpdateInput {
	input := catalog.UpdateInput{Name: old.Metadata.Name, Tags: slices.Clone(old.Metadata.Tags), Enabled: old.Metadata.Enabled, Payload: payload}
	if name != nil {
		input.Name = *name
	}
	if tags != nil {
		input.Tags = slices.Clone(*tags)
	}
	if enabled != nil {
		input.Enabled = *enabled
	}
	return input
}

type RoutingProfileResource struct {
	Metadata       ResourceMetadata  `json:"metadata"`
	RoutingProfile ir.RoutingProfile `json:"routing_profile"`
}
type RoutingProfileResponse struct {
	RequestID string                 `json:"request_id"`
	Data      RoutingProfileResource `json:"data"`
}
type RoutingProfileListResponse struct {
	RequestID string                   `json:"request_id"`
	Data      []RoutingProfileResource `json:"data"`
	Page      PageInfo                 `json:"page"`
}

func NewRoutingProfileResponse(requestID string, resource ir.Resource) (RoutingProfileResponse, error) {
	if resource.Validate() != nil {
		return RoutingProfileResponse{}, NewError(InternalError)
	}
	payload, ok := resource.Payload.(*ir.RoutingProfile)
	if !ok {
		return RoutingProfileResponse{}, NewError(InternalError)
	}
	return RoutingProfileResponse{RequestID: safeRequestID(requestID), Data: RoutingProfileResource{Metadata: metadata(resource.Metadata), RoutingProfile: payload.Clone()}}, nil
}
func NewRoutingProfileListResponse(requestID string, items []ir.Resource, page PageInfo) (RoutingProfileListResponse, error) {
	response := RoutingProfileListResponse{RequestID: safeRequestID(requestID), Data: make([]RoutingProfileResource, 0, len(items)), Page: page}
	for _, item := range items {
		read, err := NewRoutingProfileResponse(requestID, item)
		if err != nil {
			return RoutingProfileListResponse{}, err
		}
		response.Data = append(response.Data, read.Data)
	}
	return response, nil
}

type RuleSetCreateRequest struct {
	Name    string          `json:"name"`
	Tags    []string        `json:"tags,omitempty"`
	Enabled *bool           `json:"enabled,omitempty"`
	RuleSet ir.RuleSetWrite `json:"rule_set"`
}

func (r *RuleSetCreateRequest) UnmarshalJSON(data []byte) error {
	type plain RuleSetCreateRequest
	var next plain
	if err := json.Unmarshal(data, &next); err != nil {
		return routingError(err, "/rule_set")
	}
	*r = RuleSetCreateRequest(next)
	return nil
}
func (r RuleSetCreateRequest) Input() (catalog.CreateInput, error) {
	if err := ValidateDTO("RuleSetCreateRequest", r); err != nil {
		return catalog.CreateInput{}, err
	}
	payload, err := ir.NewRuleSet(r.RuleSet)
	if err != nil {
		return catalog.CreateInput{}, routingError(err, "/rule_set")
	}
	enabled := true
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	return catalog.CreateInput{Name: r.Name, Tags: append([]string{}, r.Tags...), Enabled: enabled, Payload: &payload}, nil
}

type RuleSetPatch struct {
	Entries *[]ir.RuleSetEntry `json:"entries,omitempty"`
}

func (p *RuleSetPatch) UnmarshalJSON(data []byte) error {
	var raw struct {
		Entries json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var next RuleSetPatch
	if len(raw.Entries) > 0 {
		encoded, err := json.Marshal(map[string]any{"schema_version": ir.SchemaVersion, "format": "domain_cidr_text", "entries": raw.Entries})
		if err != nil {
			return err
		}
		var write ir.RuleSetWrite
		if err := json.Unmarshal(encoded, &write); err != nil {
			return err
		}
		next.Entries = &write.Entries
	}
	*p = next
	return nil
}

type RuleSetPatchRequest struct {
	Name    *string       `json:"name,omitempty"`
	Tags    *[]string     `json:"tags,omitempty"`
	Enabled *bool         `json:"enabled,omitempty"`
	RuleSet *RuleSetPatch `json:"rule_set,omitempty"`
}

func (r *RuleSetPatchRequest) UnmarshalJSON(data []byte) error {
	type plain RuleSetPatchRequest
	var next plain
	if err := json.Unmarshal(data, &next); err != nil {
		return routingError(err, "/rule_set")
	}
	*r = RuleSetPatchRequest(next)
	return nil
}
func (r RuleSetPatchRequest) Merge(old ir.Resource) (catalog.UpdateInput, error) {
	if err := ValidateDTO("RuleSetPatchRequest", r); err != nil {
		return catalog.UpdateInput{}, err
	}
	if old.Validate() != nil {
		return catalog.UpdateInput{}, NewError(InternalError)
	}
	payload, ok := old.Payload.(*ir.RuleSet)
	if !ok {
		return catalog.UpdateInput{}, NewError(ValidationFailed)
	}
	next := payload.Clone()
	if patch := r.RuleSet; patch != nil && patch.Entries != nil {
		var err error
		next, err = ir.NewRuleSet(ir.RuleSetWrite{SchemaVersion: next.SchemaVersion, Format: next.Format, Entries: slices.Clone(*patch.Entries)})
		if err != nil {
			return catalog.UpdateInput{}, routingError(err, "/rule_set")
		}
	}
	return routingUpdateInput(old, r.Name, r.Tags, r.Enabled, &next), nil
}

type RuleSetResource struct {
	Metadata ResourceMetadata `json:"metadata"`
	RuleSet  ir.RuleSet       `json:"rule_set"`
}
type RuleSetResponse struct {
	RequestID string          `json:"request_id"`
	Data      RuleSetResource `json:"data"`
}
type RuleSetListResponse struct {
	RequestID string            `json:"request_id"`
	Data      []RuleSetResource `json:"data"`
	Page      PageInfo          `json:"page"`
}

func NewRuleSetResponse(requestID string, resource ir.Resource) (RuleSetResponse, error) {
	if resource.Validate() != nil {
		return RuleSetResponse{}, NewError(InternalError)
	}
	payload, ok := resource.Payload.(*ir.RuleSet)
	if !ok {
		return RuleSetResponse{}, NewError(InternalError)
	}
	return RuleSetResponse{RequestID: safeRequestID(requestID), Data: RuleSetResource{Metadata: metadata(resource.Metadata), RuleSet: payload.Clone()}}, nil
}
func NewRuleSetListResponse(requestID string, items []ir.Resource, page PageInfo) (RuleSetListResponse, error) {
	response := RuleSetListResponse{RequestID: safeRequestID(requestID), Data: make([]RuleSetResource, 0, len(items)), Page: page}
	for _, item := range items {
		read, err := NewRuleSetResponse(requestID, item)
		if err != nil {
			return RuleSetListResponse{}, err
		}
		response.Data = append(response.Data, read.Data)
	}
	return response, nil
}
