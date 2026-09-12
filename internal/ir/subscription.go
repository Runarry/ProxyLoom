package ir

import (
	"encoding/json"
	"slices"
	"strconv"
)

type TagSelector struct {
	AllTags  []string `json:"all_tags"`
	AnyTags  []string `json:"any_tags"`
	NoneTags []string `json:"none_tags"`
}

// Empty selectors select nothing; manual membership remains independent.
func (s TagSelector) Matches(tags []string) bool {
	if len(s.AllTags)+len(s.AnyTags)+len(s.NoneTags) == 0 {
		return false
	}
	for _, tag := range s.AllTags {
		if !slices.Contains(tags, tag) {
			return false
		}
	}
	for _, tag := range s.NoneTags {
		if slices.Contains(tags, tag) {
			return false
		}
	}
	if len(s.AnyTags) == 0 {
		return true
	}
	for _, tag := range s.AnyTags {
		if slices.Contains(tags, tag) {
			return true
		}
	}
	return false
}

type SubscriptionMembers struct {
	IncludeIDs []ID        `json:"include_ids"`
	ExcludeIDs []ID        `json:"exclude_ids"`
	Selector   TagSelector `json:"selector"`
}
type ClientParameters struct {
	LocalPort         *int  `json:"local_port,omitempty"`
	ControlAPIEnabled *bool `json:"control_api_enabled,omitempty"`
}
type SubscriptionTarget struct {
	Key              string            `json:"key"`
	CoreBuildID      ID                `json:"core_build_id"`
	ClientPresetID   ID                `json:"client_preset_id"`
	Format           OutputFormat      `json:"format"`
	PolicyOverrides  []PolicyOverride  `json:"policy_overrides"`
	ClientParameters *ClientParameters `json:"client_parameters,omitempty"`
	Enabled          *bool             `json:"enabled,omitempty"`
}

func (t SubscriptionTarget) IsEnabled() bool { return t.Enabled == nil || *t.Enabled }
func (t SubscriptionTarget) CheckPreset(p ClientPreset) error {
	if t.Format != p.Format {
		return Diagnostics{issue(InvalidValue, "/format")}
	}
	if t.ClientParameters != nil {
		if t.ClientParameters.LocalPort != nil && *t.ClientParameters.LocalPort != p.LocalListener.Port {
			return Diagnostics{issue(CapabilityUnsupported, "/client_parameters/local_port")}
		}
		if t.ClientParameters.ControlAPIEnabled != nil && *t.ClientParameters.ControlAPIEnabled != p.ControlAPI.Enabled {
			return Diagnostics{issue(CapabilityUnsupported, "/client_parameters/control_api_enabled")}
		}
	}
	return nil
}

type SubscriptionProfile struct {
	SchemaVersion    int                  `json:"schema_version"`
	Members          SubscriptionMembers  `json:"members"`
	RoutingProfileID ID                   `json:"routing_profile_id"`
	DNSProfileID     ID                   `json:"dns_profile_id"`
	Targets          []SubscriptionTarget `json:"targets"`
	PublishPolicy    string               `json:"publish_policy"`
}

func (*SubscriptionProfile) resourcePayload() {}
func (v SubscriptionProfile) Validate() error {
	if err := validateValue(v, "subscription_profile"); err != nil {
		return err
	}
	keys := map[string]bool{}
	for i, t := range v.Targets {
		path := "/targets/" + strconv.Itoa(i)
		if keys[t.Key] {
			return Diagnostics{issue(DuplicateResource, path+"/key")}
		}
		keys[t.Key] = true
		groups := map[ID]bool{}
		for j, o := range t.PolicyOverrides {
			p := path + "/policy_overrides/" + strconv.Itoa(j)
			if groups[o.PolicyGroupID] {
				return Diagnostics{issue(DuplicateResource, p+"/policy_group_id")}
			}
			groups[o.PolicyGroupID] = true
			if err := o.Validate(); err != nil {
				return prefixDiagnostics(err, p, o.PolicyGroupID)
			}
		}
	}
	return nil
}
func (v *SubscriptionProfile) UnmarshalJSON(data []byte) error {
	type plain SubscriptionProfile
	var next plain
	if err := decodePlain(data, "subscription_profile", &next); err != nil {
		return err
	}
	candidate := SubscriptionProfile(next)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*v = candidate
	return nil
}
func (v SubscriptionProfile) Clone() SubscriptionProfile {
	data, _ := json.Marshal(v)
	var out SubscriptionProfile
	_ = json.Unmarshal(data, &out)
	return out
}
