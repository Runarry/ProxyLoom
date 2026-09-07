package ir

import "encoding/json"

type ResourceKind string

const (
	KindNode                ResourceKind = "node"
	KindChain               ResourceKind = "chain"
	KindPolicyGroup         ResourceKind = "policy_group"
	KindRoutingProfile      ResourceKind = "routing_profile"
	KindDNSProfile          ResourceKind = "dns_profile"
	KindRuleSet             ResourceKind = "rule_set"
	KindClientPreset        ResourceKind = "client_preset"
	KindSubscriptionProfile ResourceKind = "subscription_profile"
	KindSource              ResourceKind = "source"
)

type RefType string
type BuiltinTarget string

const (
	ResourceRef RefType       = "resource_ref"
	BuiltinRef  RefType       = "builtin"
	Direct      BuiltinTarget = "direct"
	Reject      BuiltinTarget = "reject"
)

// TargetRef is a validated discriminated union. A resource_ref has only Kind
// and ResourceID; a builtin has only Builtin. Mixed variants are invalid.
type TargetRef struct {
	Type       RefType       `json:"type"`
	Kind       ResourceKind  `json:"kind,omitempty"`
	ResourceID ID            `json:"resource_id,omitempty"`
	Builtin    BuiltinTarget `json:"builtin,omitempty"`
}

func (v TargetRef) Validate() error { return validateValue(v, "target_ref") }

// ValidateFor checks a containing field's allowed resource kinds and builtin
// policy. For example policy members allow node/chain and no builtins.
func (v TargetRef) ValidateFor(allowBuiltin bool, allowedKinds ...ResourceKind) error {
	if err := v.Validate(); err != nil {
		return err
	}
	if v.Type == BuiltinRef {
		if allowBuiltin {
			return nil
		}
		return Diagnostics{issue(ReferenceKind, "/type")}
	}
	for _, kind := range allowedKinds {
		if v.Kind == kind {
			return nil
		}
	}
	return Diagnostics{issue(ReferenceKind, "/kind")}
}

func (v *TargetRef) UnmarshalJSON(data []byte) error {
	type plain TargetRef
	var next plain
	if err := decodePlain(data, "target_ref", &next); err != nil {
		return err
	}
	*v = TargetRef(next)
	return nil
}

func DecodeTargetRef(data []byte) (TargetRef, error) {
	var value TargetRef
	err := json.Unmarshal(data, &value)
	return value, safeDecodeError(err)
}

// NodeRef follows the editing head. FrozenInput resolves it against exactly one
// immutable Resource revision in its snapshot, never a repository lookup.
type NodeRef struct {
	NodeID ID `json:"node_id"`
}

func (v NodeRef) Validate() error { return validateValue(v, "node_ref") }
func (v *NodeRef) UnmarshalJSON(data []byte) error {
	type plain NodeRef
	var next plain
	if err := decodePlain(data, "node_ref", &next); err != nil {
		return err
	}
	*v = NodeRef(next)
	return nil
}

type FailurePolicy string

const FailClosed FailurePolicy = "fail_closed"

type Chain struct {
	SchemaVersion int           `json:"schema_version"`
	Hops          []NodeRef     `json:"hops"`
	FailurePolicy FailurePolicy `json:"failure_policy"`
}

func (*Chain) resourcePayload() {}
func (v Chain) Validate() error { return validateValue(v, "chain") }
func (v *Chain) UnmarshalJSON(data []byte) error {
	type plain Chain
	var next plain
	if err := decodePlain(data, "chain", &next); err != nil {
		return err
	}
	*v = Chain(next)
	return nil
}
func DecodeChain(data []byte) (Chain, error) {
	var value Chain
	err := json.Unmarshal(data, &value)
	return value, safeDecodeError(err)
}

// Metadata is independent of the immutable typed payload. ResourceKind reserves
// future names, but Resource admits only node and chain payloads in this slice.
type Metadata struct {
	ResourceID    ID           `json:"resource_id"`
	ScopeID       ID           `json:"scope_id"`
	Kind          ResourceKind `json:"kind"`
	Revision      int64        `json:"revision"`
	SchemaVersion int          `json:"schema_version"`
	Name          string       `json:"name"`
	Tags          []string     `json:"tags"`
	Enabled       bool         `json:"enabled"`
	SecurityEpoch int64        `json:"security_epoch"`
}

func (v Metadata) Validate() error { return validateValue(v, "metadata") }
func (v *Metadata) UnmarshalJSON(data []byte) error {
	type plain Metadata
	var next plain
	if err := decodePlain(data, "metadata", &next); err != nil {
		return err
	}
	*v = Metadata(next)
	return nil
}

type ResourcePayload interface {
	resourcePayload()
	Validate() error
}
type Resource struct {
	Metadata Metadata        `json:"metadata"`
	Payload  ResourcePayload `json:"payload"`
}

func (v Resource) Validate() error {
	switch payload := v.Payload.(type) {
	case *Node:
		if payload == nil || v.Metadata.Kind != KindNode {
			return Diagnostics{issue(InvalidUnion, "/payload")}
		}
		if err := payload.Validate(); err != nil {
			return prefixDiagnostics(err, "/payload", v.Metadata.ResourceID)
		}
	case *Chain:
		if payload == nil || v.Metadata.Kind != KindChain {
			return Diagnostics{issue(InvalidUnion, "/payload")}
		}
	default:
		return Diagnostics{issue(InvalidUnion, "/payload")}
	}
	return validateValue(v, "resource")
}

func (v *Resource) UnmarshalJSON(data []byte) error {
	if err := validateDocument(data, "resource"); err != nil {
		return err
	}
	var wire struct {
		Metadata Metadata        `json:"metadata"`
		Payload  json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return safeDecodeError(err)
	}
	var payload ResourcePayload
	switch wire.Metadata.Kind {
	case KindNode:
		payload = &Node{}
	case KindChain:
		payload = &Chain{}
	default:
		return Diagnostics{issue(InvalidUnion, "/metadata/kind")}
	}
	if err := json.Unmarshal(wire.Payload, payload); err != nil {
		return prefixDiagnostics(err, "/payload", wire.Metadata.ResourceID)
	}
	*v = Resource{Metadata: wire.Metadata, Payload: payload}
	return nil
}

func DecodeResource(data []byte) (Resource, error) {
	var value Resource
	err := json.Unmarshal(data, &value)
	return value, safeDecodeError(err)
}

func prefixDiagnostics(err error, prefix string, id ID) error {
	if id.Validate() != nil {
		id = ""
	}
	diagnostics, ok := err.(Diagnostics)
	if !ok {
		return Diagnostics{issue(InvalidType, prefix)}
	}
	out := make(Diagnostics, len(diagnostics))
	for i, d := range diagnostics {
		out[i] = d
		out[i].FieldPath = prefix + d.FieldPath
		out[i].ResourceID = id
	}
	return out
}
