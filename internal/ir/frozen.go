package ir

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
)

type CoreFamily string
type OutputFormat string

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
	Key                  string       `json:"key"`
	CoreFamily           CoreFamily   `json:"core_family"`
	CoreBuildID          ID           `json:"core_build_id"`
	CoreBuildSHA256      string       `json:"core_build_sha256"`
	AdapterVersion       string       `json:"adapter_version"`
	ClientPresetID       ID           `json:"client_preset_id"`
	ClientPresetRevision int64        `json:"client_preset_revision"`
	Format               OutputFormat `json:"format"`
}

func (v Target) Validate() error { return validateValue(v, "target") }
func (v *Target) UnmarshalJSON(data []byte) error {
	type plain Target
	var next plain
	if err := decodePlain(data, "target", &next); err != nil {
		return err
	}
	*v = Target(next)
	return nil
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
// resources form exactly the node/chain dependency closure for this prototype.
type FrozenInputSpec struct {
	SchemaVersion   int         `json:"schema_version"`
	SnapshotID      ID          `json:"snapshot_id"`
	ScopeID         ID          `json:"scope_id"`
	CatalogRevision int64       `json:"catalog_revision"`
	SecurityEpoch   int64       `json:"security_epoch"`
	Resources       []Resource  `json:"resources"`
	Members         []FrozenRef `json:"members"`
	Targets         []Target    `json:"targets"`
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
	for i, resource := range v.Resources {
		if err := resource.Validate(); err != nil {
			return prefixDiagnostics(err, "/resources/"+strconv.Itoa(i), resource.Metadata.ResourceID)
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
	// Every Chain edge must resolve to a concrete Node. This also prevents self
	// references, nested chains, policy groups and cycles without a generic graph.
	for i, resource := range v.Resources {
		chain, ok := resource.Payload.(*Chain)
		if !ok {
			continue
		}
		for hopIndex, hop := range chain.Hops {
			path := "/resources/" + strconv.Itoa(i) + "/payload/hops/" + strconv.Itoa(hopIndex) + "/node_id"
			node, exists := resources[hop.NodeID]
			if !exists {
				add(ReferenceMissing, path, resource.Metadata.ResourceID)
				continue
			}
			if node.Metadata.Kind != KindNode {
				add(ReferenceKind, path, resource.Metadata.ResourceID)
				continue
			}
			if reachable[resource.Metadata.ResourceID] {
				reachable[hop.NodeID] = true
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

// Target returns the exact frozen target descriptor; it has no pointer fields.
func (v FrozenInput) Target(key string) (Target, bool) {
	if v.spec == nil {
		return Target{}, false
	}
	for _, target := range v.spec.Targets {
		if target.Key == key {
			return target, true
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
	copy.Targets = slices.Clone(source.Targets)
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
