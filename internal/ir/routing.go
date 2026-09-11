package ir

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"slices"
	"strconv"
)

type Network string
type DomainResolutionMode string
type RuleSetEntryKind string
type DomainMatchKind string
type RuleSetFormat string

const (
	NetworkTCP        Network              = "tcp"
	NetworkUDP        Network              = "udp"
	PreserveDomain    DomainResolutionMode = "preserve_domain"
	ResolveForIPRules DomainResolutionMode = "resolve_for_ip_rules"
	RuleSetDomain     RuleSetEntryKind     = "domain"
	RuleSetCIDR       RuleSetEntryKind     = "cidr"
	DomainExact       DomainMatchKind      = "exact"
	DomainSuffix      DomainMatchKind      = "suffix"
	DomainCIDRText    RuleSetFormat        = "domain_cidr_text"
)

type PortRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

func (v PortRange) Validate() error {
	if err := validateValue(v, "port_range"); err != nil {
		return err
	}
	if v.From > v.To {
		return Diagnostics{issue(InvalidValue, "/to")}
	}
	return nil
}

func (v *PortRange) UnmarshalJSON(data []byte) error {
	type plain PortRange
	var next plain
	if err := decodePlain(data, "port_range", &next); err != nil {
		return err
	}
	candidate := PortRange(next)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*v = candidate
	return nil
}

// Different populated fields are ANDed; entries within each field are ORed.
// Optional empty arrays are rejected at the JSON boundary, never match-all.
type DomainMatch struct {
	DomainExact  []string `json:"domain_exact,omitempty"`
	DomainSuffix []string `json:"domain_suffix,omitempty"`
	RuleSetIDs   []ID     `json:"rule_set_ids,omitempty"`
}

func (v DomainMatch) Validate() error { return validateValue(v, "domain_match") }
func (v *DomainMatch) UnmarshalJSON(data []byte) error {
	type plain DomainMatch
	var next plain
	if err := decodePlain(data, "domain_match", &next); err != nil {
		return err
	}
	*v = DomainMatch(next)
	return nil
}
func (v DomainMatch) Clone() DomainMatch {
	v.DomainExact = slices.Clone(v.DomainExact)
	v.DomainSuffix = slices.Clone(v.DomainSuffix)
	v.RuleSetIDs = slices.Clone(v.RuleSetIDs)
	return v
}

type RouteMatch struct {
	DomainExact      []string    `json:"domain_exact,omitempty"`
	DomainSuffix     []string    `json:"domain_suffix,omitempty"`
	RuleSetIDs       []ID        `json:"rule_set_ids,omitempty"`
	IPCIDRs          []string    `json:"ip_cidrs,omitempty"`
	DestinationPorts []PortRange `json:"destination_ports,omitempty"`
	Network          []Network   `json:"network,omitempty"`
}

func canonicalCIDR(value string) bool {
	prefix, err := netip.ParsePrefix(value)
	return err == nil && prefix == prefix.Masked() && prefix.String() == value
}

func (v RouteMatch) Validate() error {
	if err := validateValue(v, "route_match"); err != nil {
		return err
	}
	for i, cidr := range v.IPCIDRs {
		if !canonicalCIDR(cidr) {
			return Diagnostics{issue(InvalidValue, "/ip_cidrs/"+strconv.Itoa(i))}
		}
	}
	for i, ports := range v.DestinationPorts {
		if err := ports.Validate(); err != nil {
			return prefixDiagnostics(err, "/destination_ports/"+strconv.Itoa(i), "")
		}
	}
	return nil
}

type portRangeFields PortRange
type routeMatchFields struct {
	DomainExact      []string          `json:"domain_exact,omitempty"`
	DomainSuffix     []string          `json:"domain_suffix,omitempty"`
	RuleSetIDs       []ID              `json:"rule_set_ids,omitempty"`
	IPCIDRs          []string          `json:"ip_cidrs,omitempty"`
	DestinationPorts []portRangeFields `json:"destination_ports,omitempty"`
	Network          []Network         `json:"network,omitempty"`
}

func (v routeMatchFields) value() RouteMatch {
	next := RouteMatch{DomainExact: v.DomainExact, DomainSuffix: v.DomainSuffix, RuleSetIDs: v.RuleSetIDs, IPCIDRs: v.IPCIDRs, Network: v.Network}
	if v.DestinationPorts != nil {
		next.DestinationPorts = make([]PortRange, len(v.DestinationPorts))
		for i, p := range v.DestinationPorts {
			next.DestinationPorts[i] = PortRange(p)
		}
	}
	return next
}
func (v *RouteMatch) UnmarshalJSON(data []byte) error {
	var next routeMatchFields
	if err := decodePlain(data, "route_match", &next); err != nil {
		return err
	}
	candidate := next.value()
	if err := candidate.Validate(); err != nil {
		return err
	}
	*v = candidate
	return nil
}
func (v RouteMatch) Clone() RouteMatch {
	v.DomainExact = slices.Clone(v.DomainExact)
	v.DomainSuffix = slices.Clone(v.DomainSuffix)
	v.RuleSetIDs = slices.Clone(v.RuleSetIDs)
	v.IPCIDRs = slices.Clone(v.IPCIDRs)
	v.DestinationPorts = slices.Clone(v.DestinationPorts)
	v.Network = slices.Clone(v.Network)
	return v
}

type RoutingRule struct {
	Match   RouteMatch `json:"match"`
	Action  TargetRef  `json:"action"`
	Enabled bool       `json:"enabled"`
	Comment string     `json:"comment"`
}

func (v RoutingRule) Validate() error {
	if err := validateValue(v, "routing_rule"); err != nil {
		return err
	}
	if err := v.Match.Validate(); err != nil {
		return prefixDiagnostics(err, "/match", "")
	}
	return nil
}

type routingRuleFields struct {
	Match   routeMatchFields `json:"match"`
	Action  TargetRef        `json:"action"`
	Enabled bool             `json:"enabled"`
	Comment string           `json:"comment"`
}

func (v routingRuleFields) value() RoutingRule {
	return RoutingRule{Match: v.Match.value(), Action: v.Action, Enabled: v.Enabled, Comment: v.Comment}
}
func (v *RoutingRule) UnmarshalJSON(data []byte) error {
	var next routingRuleFields
	if err := decodePlain(data, "routing_rule", &next); err != nil {
		return err
	}
	candidate := next.value()
	if err := candidate.Validate(); err != nil {
		return err
	}
	*v = candidate
	return nil
}

// Rule order is semantic: first enabled matching rule wins, followed by Final.
type RoutingProfile struct {
	SchemaVersion        int                  `json:"schema_version"`
	Rules                []RoutingRule        `json:"rules"`
	Final                TargetRef            `json:"final"`
	DomainResolutionMode DomainResolutionMode `json:"domain_resolution_mode"`
}

func (*RoutingProfile) resourcePayload() {}
func (v RoutingProfile) Validate() error {
	if err := validateValue(v, "routing_profile"); err != nil {
		return err
	}
	for i, rule := range v.Rules {
		if err := rule.Validate(); err != nil {
			return prefixDiagnostics(err, "/rules/"+strconv.Itoa(i), "")
		}
	}
	return nil
}
func (v *RoutingProfile) UnmarshalJSON(data []byte) error {
	type plain RoutingProfile
	var next struct {
		plain
		Rules []routingRuleFields `json:"rules"`
	}
	if err := decodePlain(data, "routing_profile", &next); err != nil {
		return err
	}
	candidate := RoutingProfile(next.plain)
	candidate.Rules = make([]RoutingRule, len(next.Rules))
	for i, rule := range next.Rules {
		candidate.Rules[i] = rule.value()
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*v = candidate
	return nil
}
func DecodeRoutingProfile(data []byte) (RoutingProfile, error) {
	var value RoutingProfile
	err := json.Unmarshal(data, &value)
	return value, safeDecodeError(err)
}
func (v RoutingProfile) Clone() RoutingProfile {
	v.Rules = slices.Clone(v.Rules)
	for i := range v.Rules {
		v.Rules[i].Match = v.Rules[i].Match.Clone()
	}
	return v
}

// RuleSetEntry is a strict discriminated union: domain has Domain/Match only;
// cidr has CIDR only. Values must already be normalized to the wire contract.
type RuleSetEntry struct {
	Kind   RuleSetEntryKind `json:"kind"`
	Domain string           `json:"domain,omitempty"`
	Match  DomainMatchKind  `json:"match,omitempty"`
	CIDR   string           `json:"cidr,omitempty"`
}

func (v RuleSetEntry) Validate() error {
	if err := validateValue(v, "rule_set_entry"); err != nil {
		return err
	}
	if v.Kind == RuleSetCIDR && !canonicalCIDR(v.CIDR) {
		return Diagnostics{issue(InvalidValue, "/cidr")}
	}
	return nil
}
func (v *RuleSetEntry) UnmarshalJSON(data []byte) error {
	type plain RuleSetEntry
	var next plain
	if err := decodePlain(data, "rule_set_entry", &next); err != nil {
		return err
	}
	candidate := RuleSetEntry(next)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*v = candidate
	return nil
}

type RuleSetWrite struct {
	SchemaVersion int            `json:"schema_version"`
	Format        RuleSetFormat  `json:"format"`
	Entries       []RuleSetEntry `json:"entries"`
}

func (v RuleSetWrite) Validate() error {
	if err := validateValue(v, "rule_set_write"); err != nil {
		return err
	}
	for i, entry := range v.Entries {
		if err := entry.Validate(); err != nil {
			return prefixDiagnostics(err, "/entries/"+strconv.Itoa(i), "")
		}
	}
	return nil
}

type ruleSetEntryFields RuleSetEntry

func (v *RuleSetWrite) UnmarshalJSON(data []byte) error {
	type plain RuleSetWrite
	var next struct {
		plain
		Entries []ruleSetEntryFields `json:"entries"`
	}
	if err := decodePlain(data, "rule_set_write", &next); err != nil {
		return err
	}
	candidate := RuleSetWrite(next.plain)
	candidate.Entries = make([]RuleSetEntry, len(next.Entries))
	for i, entry := range next.Entries {
		candidate.Entries[i] = RuleSetEntry(entry)
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*v = candidate
	return nil
}
func DecodeRuleSetWrite(data []byte) (RuleSetWrite, error) {
	var value RuleSetWrite
	err := json.Unmarshal(data, &value)
	return value, safeDecodeError(err)
}

type RuleSet struct {
	SchemaVersion int            `json:"schema_version"`
	Format        RuleSetFormat  `json:"format"`
	Entries       []RuleSetEntry `json:"entries"`
	ContentHash   string         `json:"content_hash"`
}

func (*RuleSet) resourcePayload() {}

// contentHash hashes sorted, duplicate-free entry JSON. Entries are an OR set;
// preserving their input order separately keeps validation line pointers useful.
func (v RuleSetWrite) contentHash() string {
	entries := make([]string, len(v.Entries))
	for i, entry := range v.Entries {
		encoded, _ := json.Marshal(entry)
		entries[i] = string(encoded)
	}
	slices.Sort(entries)
	encoded, _ := json.Marshal(struct {
		SchemaVersion int           `json:"schema_version"`
		Format        RuleSetFormat `json:"format"`
		Entries       []string      `json:"entries"`
	}{v.SchemaVersion, v.Format, slices.Compact(entries)})
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}
func NewRuleSet(write RuleSetWrite) (RuleSet, error) {
	if err := write.Validate(); err != nil {
		return RuleSet{}, err
	}
	return RuleSet{SchemaVersion: write.SchemaVersion, Format: write.Format, Entries: slices.Clone(write.Entries), ContentHash: write.contentHash()}, nil
}
func (v RuleSet) Validate() error {
	if err := validateValue(v, "rule_set"); err != nil {
		return err
	}
	write := RuleSetWrite{SchemaVersion: v.SchemaVersion, Format: v.Format, Entries: v.Entries}
	if err := write.Validate(); err != nil {
		return err
	}
	if v.ContentHash != write.contentHash() {
		return Diagnostics{issue(InvalidValue, "/content_hash")}
	}
	return nil
}
func (v *RuleSet) UnmarshalJSON(data []byte) error {
	type plain RuleSet
	var next struct {
		plain
		Entries []ruleSetEntryFields `json:"entries"`
	}
	if err := decodePlain(data, "rule_set", &next); err != nil {
		return err
	}
	candidate := RuleSet(next.plain)
	candidate.Entries = make([]RuleSetEntry, len(next.Entries))
	for i, entry := range next.Entries {
		candidate.Entries[i] = RuleSetEntry(entry)
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*v = candidate
	return nil
}
func DecodeRuleSet(data []byte) (RuleSet, error) {
	var value RuleSet
	err := json.Unmarshal(data, &value)
	return value, safeDecodeError(err)
}
func (v RuleSet) Clone() RuleSet { v.Entries = slices.Clone(v.Entries); return v }
