package apicontract

import (
	"encoding/json"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

type DNSProfileCreateRequest struct {
	Name       string        `json:"name"`
	Tags       []string      `json:"tags,omitempty"`
	Enabled    *bool         `json:"enabled,omitempty"`
	DNSProfile ir.DNSProfile `json:"dns_profile"`
}

func (r *DNSProfileCreateRequest) UnmarshalJSON(data []byte) error {
	type plain DNSProfileCreateRequest
	var next plain
	if err := json.Unmarshal(data, &next); err != nil {
		return routingError(err, "/dns_profile")
	}
	*r = DNSProfileCreateRequest(next)
	return nil
}

func (r DNSProfileCreateRequest) Input() (catalog.CreateInput, error) {
	if err := ValidateDTO("DNSProfileCreateRequest", r); err != nil {
		return catalog.CreateInput{}, err
	}
	if err := r.DNSProfile.Validate(); err != nil {
		return catalog.CreateInput{}, routingError(err, "/dns_profile")
	}
	payload := r.DNSProfile.Clone()
	enabled := true
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	return catalog.CreateInput{Name: r.Name, Tags: append([]string{}, r.Tags...), Enabled: enabled, Payload: &payload}, nil
}

type DNSProfilePatch struct {
	Bootstrap     *[]ir.BootstrapResolver `json:"bootstrap,omitempty"`
	Resolvers     *[]ir.DNSResolver       `json:"resolvers,omitempty"`
	Rules         *[]ir.DNSRule           `json:"rules,omitempty"`
	FinalResolver *string                 `json:"final_resolver,omitempty"`
}

// Resolver semantics depend on the complete merged profile. Delay per-resolver
// validation so diagnostics retain the containing resolver array index.
func (p *DNSProfilePatch) UnmarshalJSON(data []byte) error {
	type resolverFields ir.DNSResolver
	var raw struct {
		Bootstrap     *[]ir.BootstrapResolver `json:"bootstrap"`
		Resolvers     *[]resolverFields       `json:"resolvers"`
		Rules         *[]ir.DNSRule           `json:"rules"`
		FinalResolver *string                 `json:"final_resolver"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	next := DNSProfilePatch{Bootstrap: raw.Bootstrap, Rules: raw.Rules, FinalResolver: raw.FinalResolver}
	if raw.Resolvers != nil {
		resolvers := make([]ir.DNSResolver, len(*raw.Resolvers))
		for i, resolver := range *raw.Resolvers {
			resolvers[i] = ir.DNSResolver(resolver)
		}
		next.Resolvers = &resolvers
	}
	*p = next
	return nil
}

type DNSProfilePatchRequest struct {
	Name       *string          `json:"name,omitempty"`
	Tags       *[]string        `json:"tags,omitempty"`
	Enabled    *bool            `json:"enabled,omitempty"`
	DNSProfile *DNSProfilePatch `json:"dns_profile,omitempty"`
}

func (r *DNSProfilePatchRequest) UnmarshalJSON(data []byte) error {
	type plain DNSProfilePatchRequest
	var next plain
	if err := json.Unmarshal(data, &next); err != nil {
		return routingError(err, "/dns_profile")
	}
	*r = DNSProfilePatchRequest(next)
	return nil
}

func (r DNSProfilePatchRequest) Merge(old ir.Resource) (catalog.UpdateInput, error) {
	if err := ValidateDTO("DNSProfilePatchRequest", r); err != nil {
		return catalog.UpdateInput{}, err
	}
	if old.Validate() != nil {
		return catalog.UpdateInput{}, NewError(InternalError)
	}
	payload, ok := old.Payload.(*ir.DNSProfile)
	if !ok {
		return catalog.UpdateInput{}, NewError(ValidationFailed)
	}
	next := payload.Clone()
	if patch := r.DNSProfile; patch != nil {
		if patch.Bootstrap != nil {
			next.Bootstrap = *patch.Bootstrap
		}
		if patch.Resolvers != nil {
			next.Resolvers = *patch.Resolvers
		}
		if patch.Rules != nil {
			next.Rules = *patch.Rules
		}
		if patch.FinalResolver != nil {
			next.FinalResolver = *patch.FinalResolver
		}
	}
	if err := next.Validate(); err != nil {
		return catalog.UpdateInput{}, routingError(err, "/dns_profile")
	}
	next = next.Clone()
	return routingUpdateInput(old, r.Name, r.Tags, r.Enabled, &next), nil
}

type DNSProfileResource struct {
	Metadata    ResourceMetadata `json:"metadata"`
	DNSProfile  ir.DNSProfile    `json:"dns_profile"`
	Diagnostics []ir.Diagnostic  `json:"diagnostics"`
}
type DNSProfileResponse struct {
	RequestID string             `json:"request_id"`
	Data      DNSProfileResource `json:"data"`
}
type DNSProfileListResponse struct {
	RequestID string               `json:"request_id"`
	Data      []DNSProfileResource `json:"data"`
	Page      PageInfo             `json:"page"`
}

func NewDNSProfileResponse(requestID string, resource ir.Resource) (DNSProfileResponse, error) {
	if resource.Validate() != nil {
		return DNSProfileResponse{}, NewError(InternalError)
	}
	payload, ok := resource.Payload.(*ir.DNSProfile)
	if !ok {
		return DNSProfileResponse{}, NewError(InternalError)
	}
	return DNSProfileResponse{RequestID: safeRequestID(requestID), Data: DNSProfileResource{
		Metadata: metadata(resource.Metadata), DNSProfile: payload.Clone(), Diagnostics: []ir.Diagnostic{},
	}}, nil
}

func NewDNSProfileListResponse(requestID string, items []ir.Resource, page PageInfo) (DNSProfileListResponse, error) {
	response := DNSProfileListResponse{RequestID: safeRequestID(requestID), Data: make([]DNSProfileResource, 0, len(items)), Page: page}
	for _, item := range items {
		read, err := NewDNSProfileResponse(requestID, item)
		if err != nil {
			return DNSProfileListResponse{}, err
		}
		response.Data = append(response.Data, read.Data)
	}
	return response, nil
}
