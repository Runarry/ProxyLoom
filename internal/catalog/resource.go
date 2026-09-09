package catalog

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

func New(scope ir.ID, input CreateInput) (ir.Resource, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return ir.Resource{}, ErrUnavailable
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	r := ir.Resource{Metadata: ir.Metadata{
		ResourceID: ir.ID(fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])),
		ScopeID:    scope, Kind: payloadKind(input.Payload), Revision: 1,
		SchemaVersion: ir.SchemaVersion, Name: input.Name, Tags: input.Tags,
		Enabled: input.Enabled, SecurityEpoch: 1,
	}, Payload: input.Payload}
	return clone(r)
}

// Apply compares complete validated typed authentication, including its kind.
// Metadata-only edits and re-enabling preserve the accumulated security epoch.
func Apply(old ir.Resource, input UpdateInput) (ir.Resource, error) {
	if _, err := Canonical(old); err != nil {
		return ir.Resource{}, err
	}
	if old.Metadata.Revision == math.MaxInt64 || payloadKind(input.Payload) != old.Metadata.Kind {
		return ir.Resource{}, ErrInvalidInput
	}
	next := old
	next.Metadata.Revision++
	next.Metadata.Name, next.Metadata.Tags, next.Metadata.Enabled = input.Name, input.Tags, input.Enabled
	next.Payload = input.Payload
	if _, err := Canonical(next); err != nil {
		return ir.Resource{}, err
	}
	authChanged := false
	if previous, ok := old.Payload.(*ir.Node); ok {
		current := next.Payload.(*ir.Node)
		authChanged = !reflect.DeepEqual(previous.Auth, current.Auth)
		previousReality, hadReality := previous.Security.(*ir.RealitySecurity)
		currentReality, hasReality := current.Security.(*ir.RealitySecurity)
		// REALITY authentication is stored in the security union rather than
		// Auth. Rotating it must revoke old published credentials as well.
		if hadReality != hasReality || (hadReality && (previousReality.PublicKey != currentReality.PublicKey || previousReality.ShortID != currentReality.ShortID)) {
			authChanged = true
		}
	}
	if authChanged || (old.Metadata.Enabled && !input.Enabled) {
		if next.Metadata.SecurityEpoch == math.MaxInt64 {
			return ir.Resource{}, ErrInvalidInput
		}
		next.Metadata.SecurityEpoch++
	}
	return clone(next)
}

func Revoke(old ir.Resource) (ir.Resource, error) { return invalidate(old, false) }
func Delete(old ir.Resource) (ir.Resource, error) { return invalidate(old, true) }

func invalidate(old ir.Resource, disable bool) (ir.Resource, error) {
	if old.Metadata.Revision == math.MaxInt64 || old.Metadata.SecurityEpoch == math.MaxInt64 {
		return ir.Resource{}, ErrInvalidInput
	}
	next, err := clone(old)
	if err != nil {
		return ir.Resource{}, err
	}
	next.Metadata.Revision++
	next.Metadata.SecurityEpoch++
	if disable {
		next.Metadata.Enabled = false
	}
	return next, nil
}

// Canonical returns secret-bearing storage bytes, never an API response. Tags
// form a set; ordered payload fields (notably hops and ALPN) retain their order.
func Canonical(resource ir.Resource) ([]byte, error) {
	resource.Metadata.Tags = append([]string{}, resource.Metadata.Tags...)
	sort.Strings(resource.Metadata.Tags)
	if err := resource.Validate(); err != nil {
		return nil, ErrInvalidInput
	}
	if node, ok := resource.Payload.(*ir.Node); ok && node.Origin != nil {
		if node.Origin.SourceResourceID.Validate() != nil || node.Origin.SourceItemID.Validate() != nil {
			return nil, ErrInvalidReference
		}
		switch node.Origin.MatchMethod {
		case ir.StableExternalKey, ir.ManualBinding, ir.ExactFingerprint:
		default:
			return nil, ErrInvalidReference
		}
	}
	data, err := json.Marshal(resource)
	if err != nil {
		return nil, ErrInvalidInput
	}
	return data, nil
}

func clone(resource ir.Resource) (ir.Resource, error) {
	data, err := Canonical(resource)
	if err != nil {
		return ir.Resource{}, err
	}
	next, err := ir.DecodeResource(data)
	if err != nil {
		return ir.Resource{}, ErrInvalidInput
	}
	return next, nil
}

func payloadKind(payload ir.ResourcePayload) ir.ResourceKind {
	switch payload.(type) {
	case *ir.Node:
		return ir.KindNode
	case *ir.Chain:
		return ir.KindChain
	}
	return ""
}

// ExtractReferences derives the single reference fact source from the payload.
// Editing NodeRefs float to the head; storage resolves scope and availability.
func ExtractReferences(resource ir.Resource) ([]Reference, error) {
	if err := resource.Validate(); err != nil || payloadKind(resource.Payload) == "" {
		return nil, ErrInvalidInput
	}
	refs := []Reference{}
	if chain, ok := resource.Payload.(*ir.Chain); ok {
		for i, hop := range chain.Hops {
			refs = append(refs, Reference{SourceID: resource.Metadata.ResourceID, SourceRevision: resource.Metadata.Revision,
				TargetID: hop.NodeID, ExpectedKind: ir.KindNode, Path: "/payload/hops/" + strconv.Itoa(i) + "/node_id", Current: true})
		}
	}
	return refs, nil
}
