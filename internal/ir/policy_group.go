package ir

import (
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

type PolicyStrategy string

const (
	PolicyFixed        PolicyStrategy = "fixed"
	PolicyManualSelect PolicyStrategy = "manual_select"
	PolicyLatencyBest  PolicyStrategy = "latency_best"
	PolicyRoundRobin   PolicyStrategy = "round_robin"
)

// PolicyHealthCheck describes client runtime observation. Validation is pure:
// it checks URL syntax and never resolves a host or performs a platform test.
type PolicyHealthCheck struct {
	Enabled     bool   `json:"enabled"`
	URL         string `json:"url,omitempty"`
	IntervalMS  *int   `json:"interval_ms,omitempty"`
	TimeoutMS   *int   `json:"timeout_ms,omitempty"`
	ToleranceMS *int   `json:"tolerance_ms,omitempty"`
}

type policyHealthCheckFields PolicyHealthCheck

func (v PolicyHealthCheck) Validate() error {
	if err := validateValue(v, "policy_health_check"); err != nil {
		return err
	}
	if v.URL != "" {
		u, err := url.Parse(v.URL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.HasSuffix(u.Host, ":") {
			return Diagnostics{issue(InvalidValue, "/url")}
		}
		if port := u.Port(); port != "" {
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				return Diagnostics{issue(InvalidValue, "/url")}
			}
		}
	}
	return nil
}

func (v *PolicyHealthCheck) UnmarshalJSON(data []byte) error {
	type plain PolicyHealthCheck
	var next plain
	if err := decodePlain(data, "policy_health_check", &next); err != nil {
		return err
	}
	candidate := PolicyHealthCheck(next)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*v = candidate
	return nil
}

type PolicyGroup struct {
	SchemaVersion int               `json:"schema_version"`
	Strategy      PolicyStrategy    `json:"strategy"`
	Members       []TargetRef       `json:"members"`
	DefaultMember TargetRef         `json:"default_member"`
	HealthCheck   PolicyHealthCheck `json:"health_check"`
	OnUnavailable FailurePolicy     `json:"on_unavailable"`
}

func (*PolicyGroup) resourcePayload() {}

func (v PolicyGroup) Validate() error {
	if err := validateValue(v, "policy_group"); err != nil {
		return err
	}
	if err := v.HealthCheck.Validate(); err != nil {
		return prefixDiagnostics(err, "/health_check", "")
	}
	seen := make(map[ID]bool, len(v.Members))
	var diagnostics Diagnostics
	for i, member := range v.Members {
		if seen[member.ResourceID] {
			diagnostics = append(diagnostics, issue(DuplicateResource, "/members/"+strconv.Itoa(i)+"/resource_id"))
		}
		seen[member.ResourceID] = true
	}
	if !slices.Contains(v.Members, v.DefaultMember) {
		diagnostics = append(diagnostics, issue(ReferenceMissing, "/default_member"))
	}
	if len(diagnostics) > 0 {
		return stableDiagnostics(diagnostics)
	}
	return nil
}

func (v *PolicyGroup) UnmarshalJSON(data []byte) error {
	type plain PolicyGroup
	// Defer health semantics until the containing group can prefix its field
	// path. The full document schema still validates every nested field first.
	var next struct {
		plain
		HealthCheck policyHealthCheckFields `json:"health_check"`
	}
	if err := decodePlain(data, "policy_group", &next); err != nil {
		return err
	}
	candidate := PolicyGroup(next.plain)
	candidate.HealthCheck = PolicyHealthCheck(next.HealthCheck)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*v = candidate
	return nil
}

func DecodePolicyGroup(data []byte) (PolicyGroup, error) {
	var value PolicyGroup
	err := json.Unmarshal(data, &value)
	return value, safeDecodeError(err)
}

func (v PolicyGroup) Clone() PolicyGroup {
	v.Members = slices.Clone(v.Members)
	v.HealthCheck = v.HealthCheck.Clone()
	return v
}

func (v PolicyHealthCheck) Clone() PolicyHealthCheck {
	if v.IntervalMS != nil {
		value := *v.IntervalMS
		v.IntervalMS = &value
	}
	if v.TimeoutMS != nil {
		value := *v.TimeoutMS
		v.TimeoutMS = &value
	}
	if v.ToleranceMS != nil {
		value := *v.ToleranceMS
		v.ToleranceMS = &value
	}
	return v
}

// PolicyOverride is the target's reviewed field whitelist. It cannot alter
// member identity, dependency checks or the group's fail-closed behavior.
type PolicyOverride struct {
	PolicyGroupID ID                 `json:"policy_group_id"`
	Strategy      *PolicyStrategy    `json:"strategy,omitempty"`
	DefaultMember *TargetRef         `json:"default_member,omitempty"`
	HealthCheck   *PolicyHealthCheck `json:"health_check,omitempty"`
}

func (v PolicyOverride) Validate() error {
	if err := validateValue(v, "policy_override"); err != nil {
		return err
	}
	if v.HealthCheck != nil {
		if err := v.HealthCheck.Validate(); err != nil {
			return prefixDiagnostics(err, "/health_check", "")
		}
	}
	return nil
}

func (v *PolicyOverride) UnmarshalJSON(data []byte) error {
	type plain PolicyOverride
	var next struct {
		plain
		HealthCheck *policyHealthCheckFields `json:"health_check,omitempty"`
	}
	if err := decodePlain(data, "policy_override", &next); err != nil {
		return err
	}
	candidate := PolicyOverride(next.plain)
	if next.HealthCheck != nil {
		health := PolicyHealthCheck(*next.HealthCheck)
		candidate.HealthCheck = &health
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*v = candidate
	return nil
}

func DecodePolicyOverride(data []byte) (PolicyOverride, error) {
	var value PolicyOverride
	err := json.Unmarshal(data, &value)
	return value, safeDecodeError(err)
}

func (v PolicyOverride) Clone() PolicyOverride {
	v.Strategy = clonePointer(v.Strategy)
	v.DefaultMember = clonePointer(v.DefaultMember)
	if v.HealthCheck != nil {
		health := v.HealthCheck.Clone()
		v.HealthCheck = &health
	}
	return v
}

// MergePolicyOverride returns an independent validated payload. Neither the
// resource revision nor either caller-owned input is modified.
func MergePolicyOverride(base Resource, override PolicyOverride) (PolicyGroup, error) {
	if err := base.Validate(); err != nil {
		return PolicyGroup{}, err
	}
	group, ok := base.Payload.(*PolicyGroup)
	if !ok || base.Metadata.ResourceID != override.PolicyGroupID {
		return PolicyGroup{}, Diagnostics{issue(ReferenceKind, "/policy_group_id")}
	}
	if err := override.Validate(); err != nil {
		return PolicyGroup{}, err
	}
	next := group.Clone()
	if override.Strategy != nil {
		next.Strategy = *override.Strategy
	}
	if override.DefaultMember != nil {
		next.DefaultMember = *override.DefaultMember
	}
	if override.HealthCheck != nil {
		next.HealthCheck = override.HealthCheck.Clone()
	}
	if err := next.Validate(); err != nil {
		return PolicyGroup{}, err
	}
	return next, nil
}
