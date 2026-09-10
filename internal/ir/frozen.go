package ir

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strconv"
)

type CoreFamily string
type OutputFormat string

// MaxFrozenResources bounds the complete immutable dependency closure, not
// just the number of native outbounds produced for an individual target.
const MaxFrozenResources = 2000

const (
	Xray        CoreFamily   = "xray"
	SingBox     CoreFamily   = "sing-box"
	Mihomo      CoreFamily   = "mihomo"
	XrayJSON    OutputFormat = "xray_json"
	SingBoxJSON OutputFormat = "singbox_json"
	MihomoYAML  OutputFormat = "mihomo_yaml"
)

// Target pins a build and preset revision. IDs and digests here are declarations
// of frozen identity, not evidence that a build, preset or capability is verified.
type Target struct {
	Key                  string                `json:"key"`
	CoreFamily           CoreFamily            `json:"core_family"`
	CoreBuildID          ID                    `json:"core_build_id"`
	CoreBuildSHA256      string                `json:"core_build_sha256"`
	AdapterVersion       string                `json:"adapter_version"`
	ClientPresetID       ID                    `json:"client_preset_id"`
	ClientPresetRevision int64                 `json:"client_preset_revision"`
	Format               OutputFormat          `json:"format"`
	PolicyOverrides      map[ID]PolicyOverride `json:"policy_overrides,omitempty"`
}

func (v Target) Validate() error {
	if err := validateValue(v, "target"); err != nil {
		return err
	}
	for i, id := range v.overrideIDs() {
		override := v.PolicyOverrides[id]
		path := "/policy_overrides/" + strconv.Itoa(i)
		if id != override.PolicyGroupID {
			return Diagnostics{issue(ReferenceKind, path+"/policy_group_id")}
		}
		if err := override.Validate(); err != nil {
			return prefixDiagnostics(err, path, override.PolicyGroupID)
		}
	}
	return nil
}
func (v Target) overrideIDs() []ID {
	ids := make([]ID, 0, len(v.PolicyOverrides))
	for id := range v.PolicyOverrides {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// The API contract uses an array; the typed lookup is serialized by stable ID.
func (v Target) MarshalJSON() ([]byte, error) {
	type plain Target
	var overrides []PolicyOverride
	for _, id := range v.overrideIDs() {
		overrides = append(overrides, v.PolicyOverrides[id])
	}
	return json.Marshal(struct {
		plain
		PolicyOverrides []PolicyOverride `json:"policy_overrides,omitempty"`
	}{plain(v), overrides})
}
func (v *Target) UnmarshalJSON(data []byte) error {
	type plain Target
	var next struct {
		plain
		PolicyOverrides []json.RawMessage `json:"policy_overrides,omitempty"`
	}
	if err := decodePlain(data, "target", &next); err != nil {
		return err
	}
	candidate := Target(next.plain)
	if len(next.PolicyOverrides) > 0 {
		candidate.PolicyOverrides = make(map[ID]PolicyOverride, len(next.PolicyOverrides))
	}
	for i, data := range next.PolicyOverrides {
		override, err := DecodePolicyOverride(data)
		path := "/policy_overrides/" + strconv.Itoa(i)
		if err != nil {
			return prefixDiagnostics(err, path, "")
		}
		if _, exists := candidate.PolicyOverrides[override.PolicyGroupID]; exists {
			return Diagnostics{issue(DuplicateResource, path+"/policy_group_id")}
		}
		candidate.PolicyOverrides[override.PolicyGroupID] = override
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*v = candidate
	return nil
}

func (v Target) Clone() Target {
	if v.PolicyOverrides != nil {
		overrides := make(map[ID]PolicyOverride, len(v.PolicyOverrides))
		for id, override := range v.PolicyOverrides {
			overrides[id] = override.Clone()
		}
		v.PolicyOverrides = overrides
	}
	return v
}

// Equal compares the complete frozen descriptor, including override values.
// Omitted and empty override arrays have the same meaning.
func (v Target) Equal(other Target) bool {
	left, right := v, other
	left.PolicyOverrides, right.PolicyOverrides = nil, nil
	if !reflect.DeepEqual(left, right) || len(v.PolicyOverrides) != len(other.PolicyOverrides) {
		return false
	}
	for id, override := range v.PolicyOverrides {
		if candidate, exists := other.PolicyOverrides[id]; !exists || !reflect.DeepEqual(override, candidate) {
			return false
		}
	}
	return true
}

type FrozenRef struct {
	ResourceID    ID           `json:"resource_id"`
	Kind          ResourceKind `json:"kind"`
	Revision      int64        `json:"revision"`
	SecurityEpoch int64        `json:"security_epoch"`
}

func (v FrozenRef) Validate() error { return validateValue(v, "frozen_ref") }
func (v *FrozenRef) UnmarshalJSON(data []byte) error {
	type plain FrozenRef
	var next plain
	if err := decodePlain(data, "frozen_ref", &next); err != nil {
		return err
	}
	*v = FrozenRef(next)
	return nil
}

// FrozenInputSpec is mutable construction data, not a compile input. Members and
// resources form exactly the member, routing, DNS and preset dependency closure.
type FrozenInputSpec struct {
	SchemaVersion   int         `json:"schema_version"`
	SnapshotID      ID          `json:"snapshot_id"`
	ScopeID         ID          `json:"scope_id"`
	CatalogRevision int64       `json:"catalog_revision"`
	SecurityEpoch   int64       `json:"security_epoch"`
	Resources       []Resource  `json:"resources"`
	Members         []FrozenRef `json:"members"`
	Targets         []Target    `json:"targets"`
	RoutingProfile  *FrozenRef  `json:"routing_profile,omitempty"`
	DNSProfile      *FrozenRef  `json:"dns_profile,omitempty"`
}

func (v *FrozenInputSpec) UnmarshalJSON(data []byte) error {
	type plain FrozenInputSpec
	var next plain
	if err := decodePlain(data, "frozen_input", &next); err != nil {
		return err
	}
	candidate := FrozenInputSpec(next)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*v = candidate
	return nil
}

func (v FrozenInputSpec) Validate() error {
	if len(v.Resources) > MaxFrozenResources {
		return Diagnostics{issue(InputLimitExceeded, "/resources")}
	}
	for i, resource := range v.Resources {
		if err := resource.Validate(); err != nil {
			return prefixDiagnostics(err, "/resources/"+strconv.Itoa(i), resource.Metadata.ResourceID)
		}
	}
	for i, target := range v.Targets {
		if err := target.Validate(); err != nil {
			return prefixDiagnostics(err, "/targets/"+strconv.Itoa(i), "")
		}
	}
	if err := validateValue(v, "frozen_input"); err != nil {
		return err
	}
	var diagnostics Diagnostics
	add := func(code DiagnosticCode, path string, id ID) {
		d := issue(code, path)
		d.ResourceID = id
		diagnostics = append(diagnostics, d)
	}
	resources := make(map[ID]Resource, len(v.Resources))
	for i, resource := range v.Resources {
		metadata := resource.Metadata
		path := "/resources/" + strconv.Itoa(i) + "/metadata"
		if _, exists := resources[metadata.ResourceID]; exists {
			add(DuplicateResource, path+"/resource_id", metadata.ResourceID)
		}
		resources[metadata.ResourceID] = resource
		if metadata.ScopeID != v.ScopeID {
			add(ScopeMismatch, path+"/scope_id", metadata.ResourceID)
		}
		if !metadata.Enabled {
			add(ResourceDisabled, path+"/enabled", metadata.ResourceID)
		}
	}
	seenTargets := make(map[string]bool, len(v.Targets))
	builds := make(map[ID]Target)
	for i, target := range v.Targets {
		path := "/targets/" + strconv.Itoa(i)
		if seenTargets[target.Key] {
			add(DuplicateTarget, path+"/key", "")
		}
		seenTargets[target.Key] = true
		if earlier, exists := builds[target.CoreBuildID]; exists {
			if earlier.CoreFamily != target.CoreFamily {
				add(InvalidValue, path+"/core_family", "")
			}
			if earlier.CoreBuildSHA256 != target.CoreBuildSHA256 {
				add(InvalidValue, path+"/core_build_sha256", "")
			}
		}
		builds[target.CoreBuildID] = target
	}
	reachable := make(map[ID]bool, len(resources))
	roots := make([]ID, 0, len(v.Members)+len(v.Targets)+2)
	checkRoot := func(ref FrozenRef, path string) {
		resource, exists := resources[ref.ResourceID]
		if !exists {
			add(ReferenceMissing, path+"/resource_id", ref.ResourceID)
			return
		}
		roots = append(roots, ref.ResourceID)
		reachable[ref.ResourceID] = true
		if ref.Kind != resource.Metadata.Kind {
			add(ReferenceKind, path+"/kind", ref.ResourceID)
		}
		if ref.Revision != resource.Metadata.Revision {
			add(ReferenceRevision, path+"/revision", ref.ResourceID)
		}
		if ref.SecurityEpoch != resource.Metadata.SecurityEpoch {
			add(ReferenceEpoch, path+"/security_epoch", ref.ResourceID)
		}
	}
	if v.RoutingProfile != nil {
		checkRoot(*v.RoutingProfile, "/routing_profile")
	}
	if v.DNSProfile != nil {
		checkRoot(*v.DNSProfile, "/dns_profile")
	}
	for i, target := range v.Targets {
		// M0 targets pin catalog presets without embedding resources. When a
		// preset is embedded its identity, revision and target semantics must agree.
		if resource, exists := resources[target.ClientPresetID]; exists {
			path := "/targets/" + strconv.Itoa(i)
			if resource.Metadata.Kind != KindClientPreset {
				add(ReferenceKind, path+"/client_preset_id", target.ClientPresetID)
				continue
			}
			roots = append(roots, target.ClientPresetID)
			reachable[target.ClientPresetID] = true
			if resource.Metadata.Revision != target.ClientPresetRevision {
				add(ReferenceRevision, path+"/client_preset_revision", target.ClientPresetID)
			}
			preset := resource.Payload.(*ClientPreset)
			if preset.CoreFamily != target.CoreFamily {
				add(InvalidValue, path+"/core_family", target.ClientPresetID)
			}
			if preset.Format != target.Format {
				add(InvalidValue, path+"/format", target.ClientPresetID)
			}
		}
	}
	seenMembers := make(map[ID]bool, len(v.Members))
	for i, member := range v.Members {
		path := "/members/" + strconv.Itoa(i)
		if seenMembers[member.ResourceID] {
			add(DuplicateResource, path+"/resource_id", member.ResourceID)
		}
		seenMembers[member.ResourceID] = true
		resource, exists := resources[member.ResourceID]
		if !exists {
			add(ReferenceMissing, path+"/resource_id", member.ResourceID)
			continue
		}
		reachable[member.ResourceID] = true
		roots = append(roots, member.ResourceID)
		if member.Kind != resource.Metadata.Kind {
			add(ReferenceKind, path+"/kind", member.ResourceID)
		}
		if member.Revision != resource.Metadata.Revision {
			add(ReferenceRevision, path+"/revision", member.ResourceID)
		}
		if member.SecurityEpoch != resource.Metadata.SecurityEpoch {
			add(ReferenceEpoch, path+"/security_epoch", member.ResourceID)
		}
	}
	// Validate all typed edges, then walk from the roots independently of the
	// resource array order. P0 groups cannot nest and chain hops are concrete nodes.
	edges := make(map[ID][]ID, len(resources))
	for i, resource := range v.Resources {
		check := func(id ID, kind ResourceKind, field string) {
			path := "/resources/" + strconv.Itoa(i) + "/payload" + field
			target, exists := resources[id]
			if !exists {
				add(ReferenceMissing, path, resource.Metadata.ResourceID)
				return
			}
			if target.Metadata.Kind != kind {
				add(ReferenceKind, path, resource.Metadata.ResourceID)
				return
			}
			edges[resource.Metadata.ResourceID] = append(edges[resource.Metadata.ResourceID], id)
		}
		checkTarget := func(ref TargetRef, field string) {
			if ref.Type == ResourceRef {
				check(ref.ResourceID, ref.Kind, field+"/resource_id")
			}
		}
		switch payload := resource.Payload.(type) {
		case *Chain:
			for hopIndex, hop := range payload.Hops {
				check(hop.NodeID, KindNode, "/hops/"+strconv.Itoa(hopIndex)+"/node_id")
			}
		case *PolicyGroup:
			for memberIndex, member := range payload.Members {
				check(member.ResourceID, member.Kind, "/members/"+strconv.Itoa(memberIndex)+"/resource_id")
			}
		case *RoutingProfile:
			checkTarget(payload.Final, "/final")
			if payload.DomainResolutionMode == ResolveForIPRules && v.DNSProfile == nil {
				add(ReferenceMissing, "/resources/"+strconv.Itoa(i)+"/payload/domain_resolution_mode", resource.Metadata.ResourceID)
			}
			for j, rule := range payload.Rules {
				path := "/rules/" + strconv.Itoa(j)
				checkTarget(rule.Action, path+"/action")
				for k, id := range rule.Match.RuleSetIDs {
					check(id, KindRuleSet, path+"/match/rule_set_ids/"+strconv.Itoa(k))
				}
			}
		case *DNSProfile:
			for j, resolver := range payload.Resolvers {
				if resolver.Outbound != nil {
					checkTarget(*resolver.Outbound, "/resolvers/"+strconv.Itoa(j)+"/outbound")
				}
			}
			for j, rule := range payload.Rules {
				for k, id := range rule.Match.RuleSetIDs {
					path := "/rules/" + strconv.Itoa(j) + "/match/rule_set_ids/" + strconv.Itoa(k)
					check(id, KindRuleSet, path)
					if target, exists := resources[id]; exists && target.Metadata.Kind == KindRuleSet {
						for _, entry := range target.Payload.(*RuleSet).Entries {
							if entry.Kind != RuleSetDomain {
								add(ReferenceKind, "/resources/"+strconv.Itoa(i)+"/payload"+path, resource.Metadata.ResourceID)
								break
							}
						}
					}
				}
			}
		}
	}
	queue := slices.Clone(roots)
	for i := 0; i < len(queue); i++ {
		for _, id := range edges[queue[i]] {
			if !reachable[id] {
				reachable[id] = true
				queue = append(queue, id)
			}
		}
	}
	for i, target := range v.Targets {
		for j, id := range target.overrideIDs() {
			path := "/targets/" + strconv.Itoa(i) + "/policy_overrides/" + strconv.Itoa(j)
			resource, exists := resources[id]
			if !exists {
				add(ReferenceMissing, path+"/policy_group_id", id)
				continue
			}
			if resource.Metadata.Kind != KindPolicyGroup {
				add(ReferenceKind, path+"/policy_group_id", id)
				continue
			}
			if !reachable[id] {
				add(UnreachableResource, path+"/policy_group_id", id)
				continue
			}
			if _, err := MergePolicyOverride(resource, target.PolicyOverrides[id]); err != nil {
				diagnostics = append(diagnostics, prefixDiagnostics(err, path, id).(Diagnostics)...)
			}
		}
	}
	for i, resource := range v.Resources {
		if !reachable[resource.Metadata.ResourceID] {
			add(UnreachableResource, "/resources/"+strconv.Itoa(i), resource.Metadata.ResourceID)
		}
	}
	if len(diagnostics) != 0 {
		return stableDiagnostics(diagnostics)
	}
	return nil
}

// FrozenInput has no exported mutable fields. Only NewFrozenInput (also used by
// DecodeFrozenInput/UnmarshalJSON) creates a usable value. Its zero value fails
// Validate. Copies of this handle share an immutable private snapshot safely.
type FrozenInput struct{ spec *FrozenInputSpec }

func NewFrozenInput(spec FrozenInputSpec) (FrozenInput, error) {
	if len(spec.Resources) > MaxFrozenResources {
		return FrozenInput{}, Diagnostics{issue(InputLimitExceeded, "/resources")}
	}
	copy, err := cloneSpec(spec)
	if err != nil {
		return FrozenInput{}, err
	}
	if err := copy.Validate(); err != nil {
		return FrozenInput{}, err
	}
	// These are sets. Hop order, ALPN and future ordered semantic lists must
	// never pass through this normalization.
	slices.SortFunc(copy.Resources, func(a, b Resource) int {
		return compareString(string(a.Metadata.ResourceID), string(b.Metadata.ResourceID))
	})
	for i := range copy.Resources {
		slices.Sort(copy.Resources[i].Metadata.Tags)
	}
	slices.SortFunc(copy.Members, func(a, b FrozenRef) int { return compareString(string(a.ResourceID), string(b.ResourceID)) })
	slices.SortFunc(copy.Targets, func(a, b Target) int { return compareString(a.Key, b.Key) })
	return FrozenInput{spec: &copy}, nil
}

func compareString(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func DecodeFrozenInput(data []byte) (FrozenInput, error) {
	var value FrozenInput
	err := json.Unmarshal(data, &value)
	return value, safeDecodeError(err)
}

func (v *FrozenInput) UnmarshalJSON(data []byte) error {
	var spec FrozenInputSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return safeDecodeError(err)
	}
	next, err := NewFrozenInput(spec)
	if err != nil {
		return err
	}
	*v = next
	return nil
}

func (v FrozenInput) Validate() error {
	if v.spec == nil {
		return Diagnostics{issue(InvalidSnapshot, "")}
	}
	return v.spec.Validate()
}

func (v FrozenInput) MarshalJSON() ([]byte, error) {
	if v.spec == nil {
		return nil, Diagnostics{issue(InvalidSnapshot, "")}
	}
	return json.Marshal(v.spec)
}

func (FrozenInput) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "FrozenInput{[REDACTED]}")
}
func (FrozenInput) LogValue() slog.Value { return slog.StringValue("FrozenInput{[REDACTED]}") }

// Spec returns a deep copy, including auth variants, optional booleans, ALPN,
// transport parameters, provenance, resource tags, chain hops and all sets.
// A zero handle returns an empty spec and still fails Validate.
func (v FrozenInput) Spec() FrozenInputSpec {
	if v.spec == nil {
		return FrozenInputSpec{}
	}
	copy, _ := cloneSpec(*v.spec) // The private snapshot contains validated variants.
	return copy
}

// Target returns an independent copy of the exact frozen target descriptor.
func (v FrozenInput) Target(key string) (Target, bool) {
	if v.spec == nil {
		return Target{}, false
	}
	for _, target := range v.spec.Targets {
		if target.Key == key {
			return target.Clone(), true
		}
	}
	return Target{}, false
}

func clonePointer[T any](source *T) *T {
	if source == nil {
		return nil
	}
	copy := *source
	return &copy
}

func cloneSpec(source FrozenInputSpec) (FrozenInputSpec, error) {
	copy := source
	copy.Members = slices.Clone(source.Members)
	copy.RoutingProfile = clonePointer(source.RoutingProfile)
	copy.DNSProfile = clonePointer(source.DNSProfile)
	copy.Targets = slices.Clone(source.Targets)
	for i := range copy.Targets {
		copy.Targets[i] = copy.Targets[i].Clone()
	}
	copy.Resources = slices.Clone(source.Resources)
	for i, resource := range source.Resources {
		copy.Resources[i].Metadata.Tags = slices.Clone(resource.Metadata.Tags)
		switch payload := resource.Payload.(type) {
		case *Node:
			if payload == nil {
				return FrozenInputSpec{}, Diagnostics{issue(InvalidUnion, "/resources/"+strconv.Itoa(i)+"/payload")}
			}
			node, err := cloneNode(*payload)
			if err != nil {
				return FrozenInputSpec{}, prefixDiagnostics(err, "/resources/"+strconv.Itoa(i)+"/payload", resource.Metadata.ResourceID)
			}
			copy.Resources[i].Payload = &node
		case *Chain:
			if payload == nil {
				return FrozenInputSpec{}, Diagnostics{issue(InvalidUnion, "/resources/"+strconv.Itoa(i)+"/payload")}
			}
			chain := *payload
			chain.Hops = slices.Clone(payload.Hops)
			copy.Resources[i].Payload = &chain
		case *PolicyGroup:
			if payload == nil {
				return FrozenInputSpec{}, Diagnostics{issue(InvalidUnion, "/resources/"+strconv.Itoa(i)+"/payload")}
			}
			group := payload.Clone()
			copy.Resources[i].Payload = &group
		case *RoutingProfile:
			if payload == nil {
				return FrozenInputSpec{}, Diagnostics{issue(InvalidUnion, "/resources/"+strconv.Itoa(i)+"/payload")}
			}
			value := payload.Clone()
			copy.Resources[i].Payload = &value
		case *DNSProfile:
			if payload == nil {
				return FrozenInputSpec{}, Diagnostics{issue(InvalidUnion, "/resources/"+strconv.Itoa(i)+"/payload")}
			}
			value := payload.Clone()
			copy.Resources[i].Payload = &value
		case *RuleSet:
			if payload == nil {
				return FrozenInputSpec{}, Diagnostics{issue(InvalidUnion, "/resources/"+strconv.Itoa(i)+"/payload")}
			}
			value := payload.Clone()
			copy.Resources[i].Payload = &value
		case *ClientPreset:
			if payload == nil {
				return FrozenInputSpec{}, Diagnostics{issue(InvalidUnion, "/resources/"+strconv.Itoa(i)+"/payload")}
			}
			value := payload.Clone()
			copy.Resources[i].Payload = &value
		default:
			return FrozenInputSpec{}, Diagnostics{issue(InvalidUnion, "/resources/"+strconv.Itoa(i)+"/payload")}
		}
	}
	return copy, nil
}

func cloneNode(source Node) (Node, error) {
	copy := source
	copy.Features.UDP = clonePointer(source.Features.UDP)
	copy.Features.Multiplex = clonePointer(source.Features.Multiplex)
	copy.Features.ProtocolVariant = clonePointer(source.Features.ProtocolVariant)
	copy.Origin = clonePointer(source.Origin)
	switch auth := source.Auth.(type) {
	case *MethodPasswordAuth:
		copy.Auth = clonePointer(auth)
	case *VMessAuth:
		copy.Auth = clonePointer(auth)
	case *UUIDAuth:
		copy.Auth = clonePointer(auth)
	case *PasswordAuth:
		copy.Auth = clonePointer(auth)
	case *UsernamePasswordAuth:
		copy.Auth = clonePointer(auth)
	case *NoAuth:
		copy.Auth = clonePointer(auth)
	default:
		return Node{}, Diagnostics{issue(InvalidUnion, "/auth")}
	}
	switch transport := source.Transport.(type) {
	case *NativeTCPTransport:
		copy.Transport = clonePointer(transport)
	case *WebSocketTransport:
		ws := clonePointer(transport)
		if ws != nil {
			ws.Host = clonePointer(ws.Host)
		}
		copy.Transport = ws
	default:
		return Node{}, Diagnostics{issue(InvalidUnion, "/transport")}
	}
	switch security := source.Security.(type) {
	case *NoSecurity:
		copy.Security = clonePointer(security)
	case *TLSSecurity:
		tls := clonePointer(security)
		if tls != nil {
			tls.VerifyCertificate = clonePointer(tls.VerifyCertificate)
			tls.ALPN = slices.Clone(tls.ALPN)
			tls.ClientFingerprint = clonePointer(tls.ClientFingerprint)
		}
		copy.Security = tls
	case *RealitySecurity:
		reality := clonePointer(security)
		if reality != nil {
			reality.ALPN = slices.Clone(reality.ALPN)
		}
		copy.Security = reality
	default:
		return Node{}, Diagnostics{issue(InvalidUnion, "/security")}
	}
	return copy, nil
}
