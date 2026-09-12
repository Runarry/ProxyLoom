package apicontract

import (
	"encoding/json"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

type SubscriptionCreateRequest struct {
	Name         string                 `json:"name"`
	Tags         []string               `json:"tags,omitempty"`
	Enabled      *bool                  `json:"enabled,omitempty"`
	Subscription ir.SubscriptionProfile `json:"subscription"`
}

func (r SubscriptionCreateRequest) Input() (catalog.CreateInput, error) {
	if err := ValidateDTO("SubscriptionCreateRequest", r); err != nil {
		return catalog.CreateInput{}, err
	}
	if err := r.Subscription.Validate(); err != nil {
		return catalog.CreateInput{}, routingError(err, "/subscription")
	}
	p := r.Subscription.Clone()
	enabled := true
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	return catalog.CreateInput{Name: r.Name, Tags: append([]string{}, r.Tags...), Enabled: enabled, Payload: &p}, nil
}

type SubscriptionPatchRequest struct {
	Name         *string                    `json:"name,omitempty"`
	Tags         *[]string                  `json:"tags,omitempty"`
	Enabled      *bool                      `json:"enabled,omitempty"`
	Subscription map[string]json.RawMessage `json:"subscription,omitempty"`
}

func (r SubscriptionPatchRequest) Merge(old ir.Resource) (catalog.UpdateInput, error) {
	if err := ValidateDTO("SubscriptionPatchRequest", r); err != nil {
		return catalog.UpdateInput{}, err
	}
	p, ok := old.Payload.(*ir.SubscriptionProfile)
	if !ok {
		return catalog.UpdateInput{}, catalog.ErrNotFound
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		return catalog.UpdateInput{}, catalog.ErrInvalidInput
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &fields); err != nil {
		return catalog.UpdateInput{}, catalog.ErrInvalidInput
	}
	for key, value := range r.Subscription {
		fields[key] = value
	}
	encoded, err = json.Marshal(fields)
	if err != nil {
		return catalog.UpdateInput{}, catalog.ErrInvalidInput
	}
	var next ir.SubscriptionProfile
	if err = json.Unmarshal(encoded, &next); err != nil {
		return catalog.UpdateInput{}, routingError(err, "/subscription")
	}
	in := catalog.UpdateInput{Name: old.Metadata.Name, Tags: append([]string{}, old.Metadata.Tags...), Enabled: old.Metadata.Enabled, Payload: &next}
	if r.Name != nil {
		in.Name = *r.Name
	}
	if r.Tags != nil {
		in.Tags = append([]string{}, (*r.Tags)...)
	}
	if r.Enabled != nil {
		in.Enabled = *r.Enabled
	}
	return in, nil
}
func SubscriptionMetadata(r ir.Resource) ResourceMetadata { return metadata(r.Metadata) }
