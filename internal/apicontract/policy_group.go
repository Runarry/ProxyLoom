package apicontract

import (
	"encoding/json"
	"errors"
	"slices"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

type PolicyGroupCreateRequest struct {
	Name        string         `json:"name"`
	Tags        []string       `json:"tags,omitempty"`
	Enabled     *bool          `json:"enabled,omitempty"`
	PolicyGroup ir.PolicyGroup `json:"policy_group"`
}

func policyGroupError(err error) error {
	var diagnostics ir.Diagnostics
	if errors.As(err, &diagnostics) {
		details := make([]Detail, 0, len(diagnostics))
		for _, diagnostic := range diagnostics {
			details = append(details, Detail{FieldPath: "/policy_group" + diagnostic.FieldPath, ResourceID: diagnostic.ResourceID})
		}
		return NewError(ValidationFailed, details...)
	}
	return err
}

func (r *PolicyGroupCreateRequest) UnmarshalJSON(data []byte) error {
	type plain PolicyGroupCreateRequest
	var next plain
	if err := json.Unmarshal(data, &next); err != nil {
		return policyGroupError(err)
	}
	*r = PolicyGroupCreateRequest(next)
	return nil
}

func (request PolicyGroupCreateRequest) Input() (catalog.CreateInput, error) {
	if err := ValidateDTO("PolicyGroupCreateRequest", request); err != nil {
		return catalog.CreateInput{}, err
	}
	if err := request.PolicyGroup.Validate(); err != nil {
		return catalog.CreateInput{}, policyGroupError(err)
	}
	group := request.PolicyGroup.Clone()
	tags := append([]string{}, request.Tags...)
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	return catalog.CreateInput{Name: request.Name, Tags: tags, Enabled: enabled, Payload: &group}, nil
}

type PolicyGroupPatch struct {
	Strategy      *ir.PolicyStrategy    `json:"strategy,omitempty"`
	Members       *[]ir.TargetRef       `json:"members,omitempty"`
	DefaultMember *ir.TargetRef         `json:"default_member,omitempty"`
	HealthCheck   *ir.PolicyHealthCheck `json:"health_check,omitempty"`
	OnUnavailable *ir.FailurePolicy     `json:"on_unavailable,omitempty"`
}

func (p *PolicyGroupPatch) UnmarshalJSON(data []byte) error {
	type plain PolicyGroupPatch
	var next struct {
		plain
		HealthCheck json.RawMessage `json:"health_check"`
	}
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	if len(next.HealthCheck) > 0 {
		var health ir.PolicyHealthCheck
		if err := json.Unmarshal(next.HealthCheck, &health); err != nil {
			var diagnostics ir.Diagnostics
			if errors.As(err, &diagnostics) {
				for i := range diagnostics {
					diagnostics[i].FieldPath = "/health_check" + diagnostics[i].FieldPath
				}
				return diagnostics
			}
			return err
		}
		next.plain.HealthCheck = &health
	}
	*p = PolicyGroupPatch(next.plain)
	return nil
}

type PolicyGroupPatchRequest struct {
	Name        *string           `json:"name,omitempty"`
	Tags        *[]string         `json:"tags,omitempty"`
	Enabled     *bool             `json:"enabled,omitempty"`
	PolicyGroup *PolicyGroupPatch `json:"policy_group,omitempty"`
}

func (r *PolicyGroupPatchRequest) UnmarshalJSON(data []byte) error {
	type plain PolicyGroupPatchRequest
	var next plain
	if err := json.Unmarshal(data, &next); err != nil {
		return policyGroupError(err)
	}
	*r = PolicyGroupPatchRequest(next)
	return nil
}

func (request PolicyGroupPatchRequest) Merge(old ir.Resource) (catalog.UpdateInput, error) {
	if err := ValidateDTO("PolicyGroupPatchRequest", request); err != nil {
		return catalog.UpdateInput{}, err
	}
	if old.Validate() != nil {
		return catalog.UpdateInput{}, NewError(InternalError)
	}
	group, ok := old.Payload.(*ir.PolicyGroup)
	if !ok {
		return catalog.UpdateInput{}, NewError(ValidationFailed)
	}
	next := group.Clone()
	if patch := request.PolicyGroup; patch != nil {
		if patch.Strategy != nil {
			next.Strategy = *patch.Strategy
		}
		if patch.Members != nil {
			next.Members = slices.Clone(*patch.Members)
		}
		if patch.DefaultMember != nil {
			next.DefaultMember = *patch.DefaultMember
		}
		if patch.HealthCheck != nil {
			next.HealthCheck = patch.HealthCheck.Clone()
		}
		if patch.OnUnavailable != nil {
			next.OnUnavailable = *patch.OnUnavailable
		}
	}
	if err := next.Validate(); err != nil {
		return catalog.UpdateInput{}, policyGroupError(err)
	}
	input := catalog.UpdateInput{Name: old.Metadata.Name, Tags: slices.Clone(old.Metadata.Tags), Enabled: old.Metadata.Enabled, Payload: &next}
	if request.Name != nil {
		input.Name = *request.Name
	}
	if request.Tags != nil {
		input.Tags = slices.Clone(*request.Tags)
	}
	if request.Enabled != nil {
		input.Enabled = *request.Enabled
	}
	return input, nil
}

type PolicyGroupResource struct {
	Metadata    ResourceMetadata `json:"metadata"`
	PolicyGroup ir.PolicyGroup   `json:"policy_group"`
	Diagnostics []ir.Diagnostic  `json:"diagnostics"`
}

type PolicyGroupResponse struct {
	RequestID string              `json:"request_id"`
	Data      PolicyGroupResource `json:"data"`
}

type PolicyGroupListResponse struct {
	RequestID string                `json:"request_id"`
	Data      []PolicyGroupResource `json:"data"`
	Page      PageInfo              `json:"page"`
}

func NewPolicyGroupResponse(requestID string, resource ir.Resource) (PolicyGroupResponse, error) {
	if resource.Validate() != nil {
		return PolicyGroupResponse{}, NewError(InternalError)
	}
	group, ok := resource.Payload.(*ir.PolicyGroup)
	if !ok {
		return PolicyGroupResponse{}, NewError(InternalError)
	}
	return PolicyGroupResponse{RequestID: safeRequestID(requestID), Data: PolicyGroupResource{
		Metadata: metadata(resource.Metadata), PolicyGroup: group.Clone(), Diagnostics: []ir.Diagnostic{}}}, nil
}

func NewPolicyGroupListResponse(requestID string, items []ir.Resource, page PageInfo) (PolicyGroupListResponse, error) {
	response := PolicyGroupListResponse{RequestID: safeRequestID(requestID), Data: make([]PolicyGroupResource, 0, len(items)), Page: page}
	for _, resource := range items {
		read, err := NewPolicyGroupResponse(requestID, resource)
		if err != nil {
			return PolicyGroupListResponse{}, err
		}
		response.Data = append(response.Data, read.Data)
	}
	return response, nil
}
