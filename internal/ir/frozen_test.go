package ir_test

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

func mutateNode(node *ir.Node) {
	node.Endpoint.Host = "mutated.example.invalid"
	switch auth := node.Auth.(type) {
	case *ir.MethodPasswordAuth:
		auth.Password = "MUTATED"
	case *ir.VMessAuth:
		auth.UUID = "MUTATED"
	case *ir.UUIDAuth:
		auth.UUID = "MUTATED"
	case *ir.PasswordAuth:
		auth.Password = "MUTATED"
	case *ir.UsernamePasswordAuth:
		auth.Username = "MUTATED"
		auth.Password = "MUTATED"
	case *ir.NoAuth:
		auth.Kind = "MUTATED"
	}
	switch transport := node.Transport.(type) {
	case *ir.NativeTCPTransport:
		transport.Kind = "MUTATED"
	case *ir.WebSocketTransport:
		transport.Path = "/mutated"
		if transport.Host != nil {
			*transport.Host = "mutated.example.invalid"
		}
	}
	switch security := node.Security.(type) {
	case *ir.NoSecurity:
		security.Mode = "MUTATED"
	case *ir.TLSSecurity:
		security.ServerName = "mutated.example.invalid"
		if security.VerifyCertificate != nil {
			*security.VerifyCertificate = false
		}
		if len(security.ALPN) > 0 {
			security.ALPN[0] = "mutated"
		}
		if security.ClientFingerprint != nil {
			*security.ClientFingerprint = "mutated"
		}
	case *ir.RealitySecurity:
		security.PublicKey = "MUTATED"
		security.ShortID = "MUTATED"
		if len(security.ALPN) > 0 {
			security.ALPN[0] = "mutated"
		}
	}
	if node.Features.UDP != nil {
		*node.Features.UDP = true
	}
	if node.Features.Multiplex != nil {
		*node.Features.Multiplex = false
	}
	if node.Features.ProtocolVariant != nil {
		*node.Features.ProtocolVariant = "MUTATED"
	}
	if node.Origin != nil {
		node.Origin.SourceItemID = "MUTATED"
	}
}

func mutateSpec(spec *ir.FrozenInputSpec) {
	spec.SnapshotID = "MUTATED"
	for i := range spec.Resources {
		resource := &spec.Resources[i]
		resource.Metadata.Name = "MUTATED"
		if len(resource.Metadata.Tags) > 0 {
			resource.Metadata.Tags[0] = "MUTATED"
		}
		switch payload := resource.Payload.(type) {
		case *ir.Node:
			mutateNode(payload)
		case *ir.Chain:
			payload.Hops[0].NodeID = "MUTATED"
		}
	}
	spec.Members[0].Revision = 999
	spec.Targets[0].CoreBuildSHA256 = "MUTATED"
}

func TestFrozenInputOwnsEveryMutableAlias(t *testing.T) {
	for _, name := range []string{"trojan-a.json", "shadowsocks-aead.json", "vmess-websocket.json", "vless-reality.json", "socks5-anonymous.json", "http-connect.json"} {
		t.Run(name, func(t *testing.T) {
			spec := mustSpec(t)
			node := mustNode(t, name)
			node.Features.UDP = boolPtr(false)
			node.Features.Multiplex = boolPtr(true)
			node.Origin = &ir.Origin{SourceResourceID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", SourceItemID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", MatchMethod: ir.ManualBinding}
			if tls, ok := node.Security.(*ir.TLSSecurity); ok {
				tls.ClientFingerprint = stringPtr("chrome")
			}
			spec.Resources[0].Payload = &node
			spec.Resources[0].Metadata.Tags = []string{"zeta", "alpha"}
			frozen, err := ir.NewFrozenInput(spec)
			if err != nil {
				t.Fatal(err)
			}
			before := mustJSON(t, frozen)
			mutateSpec(&spec)
			if !bytes.Equal(before, mustJSON(t, frozen)) {
				t.Fatal("input mutation changed frozen data")
			}
			view := frozen.Spec()
			mutateSpec(&view)
			if !bytes.Equal(before, mustJSON(t, frozen)) {
				t.Fatal("getter mutation changed frozen data")
			}
			serialized := mustJSON(t, frozen)
			serialized[0] = 'x'
			if !bytes.Equal(before, mustJSON(t, frozen)) {
				t.Fatal("serialized bytes alias frozen data")
			}
			if err := frozen.Validate(); err != nil {
				t.Fatal(err)
			}
			target, ok := frozen.Target("xray-default")
			if !ok {
				t.Fatal("frozen target missing")
			}
			target.CoreBuildSHA256 = "MUTATED"
			next, _ := frozen.Target("xray-default")
			if next.CoreBuildSHA256 == target.CoreBuildSHA256 {
				t.Fatal("target getter aliases frozen data")
			}
		})
	}
}

func TestFrozenInputReferenceAndIdentityFailures(t *testing.T) {
	tests := []struct {
		name   string
		code   ir.DiagnosticCode
		mutate func(*ir.FrozenInputSpec)
	}{
		{"missing hop", ir.ReferenceMissing, func(s *ir.FrozenInputSpec) { s.Resources = append(s.Resources[:1], s.Resources[2:]...) }},
		{"duplicate resource", ir.DuplicateResource, func(s *ir.FrozenInputSpec) { s.Resources = append(s.Resources, s.Resources[0]) }},
		{"scope mismatch", ir.ScopeMismatch, func(s *ir.FrozenInputSpec) { s.Resources[0].Metadata.ScopeID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" }},
		{"disabled", ir.ResourceDisabled, func(s *ir.FrozenInputSpec) { s.Resources[0].Metadata.Enabled = false }},
		{"nested chain", ir.ReferenceKind, func(s *ir.FrozenInputSpec) {
			s.Resources[2].Payload.(*ir.Chain).Hops[0].NodeID = s.Resources[2].Metadata.ResourceID
		}},
		{"stale revision", ir.ReferenceRevision, func(s *ir.FrozenInputSpec) { s.Members[0].Revision++ }},
		{"stale epoch", ir.ReferenceEpoch, func(s *ir.FrozenInputSpec) { s.Members[0].SecurityEpoch++ }},
		{"wrong member kind", ir.ReferenceKind, func(s *ir.FrozenInputSpec) { s.Members[0].Kind = ir.KindNode }},
		{"unresolved revision", ir.InvalidValue, func(s *ir.FrozenInputSpec) { s.Resources[0].Metadata.Revision = 0 }},
		{"unresolved preset", ir.InvalidValue, func(s *ir.FrozenInputSpec) { s.Targets[0].ClientPresetRevision = 0 }},
		{"duplicate target", ir.DuplicateTarget, func(s *ir.FrozenInputSpec) { s.Targets = append(s.Targets, s.Targets[0]) }},
		{"conflicting build", ir.InvalidValue, func(s *ir.FrozenInputSpec) {
			target := s.Targets[0]
			target.Key = "xray-other"
			target.CoreBuildSHA256 = strings.Repeat("e", 64)
			s.Targets = append(s.Targets, target)
		}},
		{"orphan credentials", ir.UnreachableResource, func(s *ir.FrozenInputSpec) {
			extra := s.Resources[0]
			extra.Metadata.ResourceID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			s.Resources = append(s.Resources, extra)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := mustSpec(t)
			test.mutate(&spec)
			_, err := ir.NewFrozenInput(spec)
			hasDiagnostic(t, err, test.code, "*")
		})
	}
}

func TestFrozenInputBoundsCompleteResourceClosure(t *testing.T) {
	spec := mustSpec(t)
	seed := spec.Resources[0]
	spec.Resources = nil
	spec.Members = nil
	for i := 0; i <= ir.MaxFrozenResources; i++ {
		resource := seed
		resource.Metadata.ResourceID = ir.ID(fmt.Sprintf("10000000-0000-4000-8000-%012d", i))
		spec.Resources = append(spec.Resources, resource)
		spec.Members = append(spec.Members, ir.FrozenRef{ResourceID: resource.Metadata.ResourceID,
			Kind: resource.Metadata.Kind, Revision: resource.Metadata.Revision, SecurityEpoch: resource.Metadata.SecurityEpoch})
	}
	boundary := spec
	boundary.Resources = spec.Resources[:ir.MaxFrozenResources]
	boundary.Members = spec.Members[:ir.MaxFrozenResources]
	if _, err := ir.NewFrozenInput(boundary); err != nil {
		t.Fatalf("valid boundary rejected: %v", err)
	}
	_, err := ir.NewFrozenInput(spec)
	hasDiagnostic(t, err, ir.InputLimitExceeded, "/resources")
	hasDiagnostic(t, spec.Validate(), ir.InputLimitExceeded, "/resources")
	if _, err := ir.DecodeFrozenInput(mustJSON(t, spec)); err == nil {
		t.Fatal("JSON construction bypassed the frozen closure limit")
	}
}

func TestFreezeSortsOnlySetsAndPreservesDirection(t *testing.T) {
	spec := mustSpec(t)
	spec.Resources[0].Metadata.Tags = []string{"zeta", "alpha"}
	spec.Members = append(spec.Members, ir.FrozenRef{ResourceID: spec.Resources[0].Metadata.ResourceID, Kind: ir.KindNode, Revision: 1, SecurityEpoch: 1})
	original, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(spec.Resources)
	slices.Reverse(spec.Members)
	slices.Reverse(spec.Targets)
	for i := range spec.Resources {
		slices.Reverse(spec.Resources[i].Metadata.Tags)
	}
	reordered, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustJSON(t, original), mustJSON(t, reordered)) {
		t.Fatal("set order changed snapshot bytes")
	}
	for _, caseName := range []string{"hops", "alpn"} {
		t.Run(caseName, func(t *testing.T) {
			spec := original.Spec()
			if caseName == "hops" {
				slices.Reverse(spec.Resources[2].Payload.(*ir.Chain).Hops)
			} else {
				slices.Reverse(spec.Resources[0].Payload.(*ir.Node).Security.(*ir.TLSSecurity).ALPN)
			}
			changed, err := ir.NewFrozenInput(spec)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(mustJSON(t, original), mustJSON(t, changed)) {
				t.Fatal(fmt.Sprintf("ordered %s was normalized away", caseName))
			}
		})
	}
}
