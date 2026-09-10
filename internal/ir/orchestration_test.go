package ir_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/api"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func routingFixture() ir.RoutingProfile {
	return ir.RoutingProfile{SchemaVersion: 1, DomainResolutionMode: ir.PreserveDomain, Final: ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Reject}, Rules: []ir.RoutingRule{
		{Match: ir.RouteMatch{DomainExact: []string{"example.invalid"}, DomainSuffix: []string{"invalid"}, IPCIDRs: []string{"192.0.2.0/24", "2001:db8::/32"}, DestinationPorts: []ir.PortRange{{From: 80, To: 443}}, Network: []ir.Network{ir.NetworkTCP}}, Action: ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Direct}, Enabled: true, Comment: "first"},
		{Match: ir.RouteMatch{Network: []ir.Network{ir.NetworkUDP}}, Action: ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Reject}, Enabled: true, Comment: "second"},
	}}
}
func dnsFixture() ir.DNSProfile {
	outbound := ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Direct}
	return ir.DNSProfile{SchemaVersion: 1, Bootstrap: []ir.BootstrapResolver{{ResolverID: "bootstrap", Kind: ir.DNSLocal}, {ResolverID: "literal", Kind: ir.DNSUDP, Address: "192.0.2.53", Port: 53}}, Resolvers: []ir.DNSResolver{
		{ResolverID: "local", Kind: ir.DNSLocal},
		{ResolverID: "udp", Kind: ir.DNSUDP, Address: "2001:db8::53", Port: 53, Outbound: &outbound},
		{ResolverID: "https", Kind: ir.DNSHTTPS, URL: "https://dns.example.invalid/dns-query", BootstrapResolverID: "literal", Outbound: &outbound},
	}, Rules: []ir.DNSRule{{Match: ir.DomainMatch{DomainSuffix: []string{"example.invalid"}}, ResolverID: "https", Enabled: true, Comment: "business"}}, FinalResolver: "local"}
}
func presetFixture() ir.ClientPreset {
	return ir.ClientPreset{SchemaVersion: 1, CoreFamily: ir.Xray, Platform: "linux", Format: ir.XrayJSON, LocalListener: ir.LocalListener{Protocol: "socks5", Listen: "127.0.0.1", Port: 1080}, DNSMode: "profile", ControlAPI: ir.ControlAPIPreset{Enabled: false}, ImportMethod: "file", ReviewStatus: "approved", ReviewedAt: "2026-09-10T00:00:00Z"}
}

func TestRoutingValidationAndStrictDecoding(t *testing.T) {
	valid := routingFixture()
	decoded, err := ir.DecodeRoutingProfile(mustJSON(t, valid))
	if err != nil || decoded.Validate() != nil || decoded.Rules[0].Comment != "first" {
		t.Fatalf("roundtrip: %v", err)
	}
	for name, mutate := range map[string]func(*ir.RoutingProfile){
		"empty_match":    func(v *ir.RoutingProfile) { v.Rules[0].Match = ir.RouteMatch{} },
		"uppercase":      func(v *ir.RoutingProfile) { v.Rules[0].Match.DomainExact[0] = "EXAMPLE.invalid" },
		"bad_hostname":   func(v *ir.RoutingProfile) { v.Rules[0].Match.DomainExact[0] = "bad..invalid" },
		"host_bits":      func(v *ir.RoutingProfile) { v.Rules[0].Match.IPCIDRs[0] = "192.0.2.1/24" },
		"family_prefix":  func(v *ir.RoutingProfile) { v.Rules[0].Match.IPCIDRs[0] = "192.0.2.0/64" },
		"canonical_ipv6": func(v *ir.RoutingProfile) { v.Rules[0].Match.IPCIDRs[1] = "2001:0DB8::/32" },
		"reverse_ports":  func(v *ir.RoutingProfile) { v.Rules[0].Match.DestinationPorts[0] = ir.PortRange{From: 443, To: 80} },
		"missing_final":  func(v *ir.RoutingProfile) { v.Final = ir.TargetRef{} },
		"wrong_action": func(v *ir.RoutingProfile) {
			v.Rules[0].Action = ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindDNSProfile, ResourceID: "33333333-3333-4333-8333-333333333333"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := valid.Clone()
			mutate(&value)
			if value.Validate() == nil {
				t.Fatal("invalid construction accepted")
			}
			if _, err := ir.DecodeRoutingProfile(mustJSON(t, value)); err == nil {
				t.Fatal("invalid JSON accepted")
			}
		})
	}
	bad := valid.Clone()
	bad.Rules[0].Match.DestinationPorts[0].To = 1
	_, err = ir.DecodeRoutingProfile(mustJSON(t, bad))
	hasDiagnostic(t, err, ir.InvalidValue, "/rules/0/match/destination_ports/0/to")
	wire := string(mustJSON(t, valid))
	for _, bad := range []string{
		strings.Replace(wire, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1),
		strings.Replace(wire, `"enabled":true,`, "", 1),
		strings.Replace(wire, `"domain_exact":["example.invalid"]`, `"domain_exact":[]`, 1),
		strings.Replace(wire, `"schema_version":1`, `"schema_version":1,"SYNTHETIC_SECRET":"SYNTHETIC_SECRET"`, 1),
	} {
		before := mustJSON(t, decoded)
		err := json.Unmarshal([]byte(bad), &decoded)
		if err == nil || strings.Contains(err.Error(), "SYNTHETIC_SECRET") || !bytes.Equal(before, mustJSON(t, decoded)) {
			t.Fatal("strict decode or receiver isolation failed")
		}
	}
}

func TestRuleSetCanonicalHashAndLineDiagnostics(t *testing.T) {
	write := ir.RuleSetWrite{SchemaVersion: 1, Format: ir.DomainCIDRText, Entries: []ir.RuleSetEntry{{Kind: ir.RuleSetDomain, Domain: "example.invalid", Match: ir.DomainSuffix}, {Kind: ir.RuleSetCIDR, CIDR: "192.0.2.0/24"}}}
	one, err := ir.NewRuleSet(write)
	if err != nil {
		t.Fatal(err)
	}
	write.Entries = []ir.RuleSetEntry{write.Entries[1], write.Entries[0], write.Entries[0]}
	two, err := ir.NewRuleSet(write)
	if err != nil || one.ContentHash != two.ContentHash {
		t.Fatalf("OR set order/duplicates changed identity: %v", err)
	}
	write.Entries[0].CIDR = "198.51.100.0/24"
	if one.Entries[1].CIDR != "192.0.2.0/24" {
		t.Fatal("constructor leaked entries alias")
	}
	if _, err := ir.DecodeRuleSet(mustJSON(t, one)); err != nil {
		t.Fatal(err)
	}
	forged := one.Clone()
	forged.Entries[0].Domain = "other.invalid"
	hasDiagnostic(t, forged.Validate(), ir.InvalidValue, "/content_hash")
	write.Entries[1] = ir.RuleSetEntry{Kind: ir.RuleSetCIDR, CIDR: "192.0.2.1/24"}
	_, err = ir.DecodeRuleSetWrite(mustJSON(t, write))
	hasDiagnostic(t, err, ir.InvalidValue, "/entries/1/cidr")
	for _, invalid := range []string{
		`{"kind":"domain","domain":"example.invalid","match":"suffix","cidr":"192.0.2.0/24"}`,
		`{"kind":"cidr","cidr":"192.0.2.0/24","domain":""}`,
		`{"kind":"domain","domain":"*.invalid","match":"suffix"}`,
	} {
		var entry ir.RuleSetEntry
		if json.Unmarshal([]byte(invalid), &entry) == nil {
			t.Fatal("mixed or unnormalized entry accepted")
		}
	}
}

func TestDNSReferencesCyclesAndExplicitResolvers(t *testing.T) {
	valid := dnsFixture()
	if _, err := ir.DecodeDNSProfile(mustJSON(t, valid)); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ir.DNSProfile){
		"duplicate_id":              func(v *ir.DNSProfile) { v.Resolvers[1].ResolverID = "local" },
		"cross_namespace_duplicate": func(v *ir.DNSProfile) { v.Resolvers[0].ResolverID = "bootstrap" },
		"missing_final":             func(v *ir.DNSProfile) { v.FinalResolver = "absent" },
		"missing_rule_resolver":     func(v *ir.DNSProfile) { v.Rules[0].ResolverID = "absent" },
		"missing_bootstrap":         func(v *ir.DNSProfile) { v.Resolvers[2].BootstrapResolverID = "absent" },
		"missing_outbound":          func(v *ir.DNSProfile) { v.Resolvers[1].Outbound = nil },
		"recursive_udp":             func(v *ir.DNSProfile) { v.Bootstrap[1].Address = "dns.example.invalid" },
		"mixed_local":               func(v *ir.DNSProfile) { v.Resolvers[0].URL = "https://dns.example.invalid/dns-query" },
		"bad_url_port":              func(v *ir.DNSProfile) { v.Resolvers[2].URL = "https://dns.example.invalid:65536/dns-query" },
	} {
		t.Run(name, func(t *testing.T) {
			value := valid.Clone()
			mutate(&value)
			if value.Validate() == nil {
				t.Fatal("invalid DNS construction accepted")
			}
			if _, err := ir.DecodeDNSProfile(mustJSON(t, value)); err == nil {
				t.Fatal("invalid DNS JSON accepted")
			}
		})
	}
	self := valid.Clone()
	self.Resolvers[2].BootstrapResolverID = "https"
	hasDiagnostic(t, self.Validate(), ir.ReferenceCycle, "/resolvers/2/bootstrap_resolver_id")
	indirect := valid.Clone()
	next := indirect.Resolvers[2].Clone()
	next.ResolverID = "other"
	next.BootstrapResolverID = "https"
	indirect.Resolvers = append(indirect.Resolvers, next)
	indirect.Resolvers[2].BootstrapResolverID = "other"
	err := indirect.Validate()
	hasDiagnostic(t, err, ir.ReferenceCycle, "/resolvers/2/bootstrap_resolver_id")
	hasDiagnostic(t, err, ir.ReferenceCycle, "/resolvers/3/bootstrap_resolver_id")
	copy := valid.Clone()
	copy.Resolvers[2].Outbound.Builtin = ir.Reject
	copy.Rules[0].Match.DomainSuffix[0] = "mutated.invalid"
	if valid.Resolvers[2].Outbound.Builtin != ir.Direct || valid.Rules[0].Match.DomainSuffix[0] != "example.invalid" {
		t.Fatal("DNS clone leaked aliases")
	}
}

func TestPresetRestrictedAndTargetConsistent(t *testing.T) {
	for _, family := range []struct {
		family ir.CoreFamily
		format ir.OutputFormat
	}{{ir.Xray, ir.XrayJSON}, {ir.SingBox, ir.SingBoxJSON}, {ir.Mihomo, ir.MihomoYAML}} {
		value := presetFixture()
		value.CoreFamily, value.Format = family.family, family.format
		if _, err := ir.DecodeClientPreset(mustJSON(t, value)); err != nil {
			t.Fatal(err)
		}
	}
	for name, mutate := range map[string]func(*ir.ClientPreset){
		"nonlinux":      func(v *ir.ClientPreset) { v.Platform = "windows" },
		"remote_import": func(v *ir.ClientPreset) { v.ImportMethod = "subscription_url" },
		"listener":      func(v *ir.ClientPreset) { v.LocalListener.Listen = "0.0.0.0" },
		"control": func(v *ir.ClientPreset) {
			v.ControlAPI = ir.ControlAPIPreset{Enabled: true, Listen: "127.0.0.1", Port: 9090}
		},
		"format":           func(v *ir.ClientPreset) { v.Format = ir.MihomoYAML },
		"review_timestamp": func(v *ir.ClientPreset) { v.ReviewedAt = "today" },
	} {
		t.Run(name, func(t *testing.T) {
			value := presetFixture()
			mutate(&value)
			if value.Validate() == nil {
				t.Fatal("unreviewed preset accepted")
			}
		})
	}
}

func orchestrationSpec(t *testing.T) ir.FrozenInputSpec {
	spec := mustSpec(t)
	add := func(id ir.ID, kind ir.ResourceKind, payload ir.ResourcePayload) ir.FrozenRef {
		meta := spec.Resources[0].Metadata
		meta.ResourceID, meta.Kind = id, kind
		spec.Resources = append(spec.Resources, ir.Resource{Metadata: meta, Payload: payload})
		return ir.FrozenRef{ResourceID: id, Kind: kind, Revision: meta.Revision, SecurityEpoch: meta.SecurityEpoch}
	}
	set, err := ir.NewRuleSet(ir.RuleSetWrite{SchemaVersion: 1, Format: ir.DomainCIDRText, Entries: []ir.RuleSetEntry{{Kind: ir.RuleSetDomain, Domain: "example.invalid", Match: ir.DomainSuffix}}})
	if err != nil {
		t.Fatal(err)
	}
	setRef := add("44444444-4444-4444-8444-444444444444", ir.KindRuleSet, &set)
	route := routingFixture()
	route.Rules[0].Match.RuleSetIDs = []ir.ID{setRef.ResourceID}
	route.DomainResolutionMode = ir.ResolveForIPRules
	routeRef := add("55555555-5555-4555-8555-555555555555", ir.KindRoutingProfile, &route)
	spec.RoutingProfile = &routeRef
	dns := dnsFixture()
	dns.Rules[0].Match.RuleSetIDs = []ir.ID{setRef.ResourceID}
	dnsRef := add("66666666-6666-4666-8666-666666666666", ir.KindDNSProfile, &dns)
	spec.DNSProfile = &dnsRef
	for _, target := range spec.Targets {
		preset := presetFixture()
		preset.CoreFamily, preset.Format = target.CoreFamily, target.Format
		add(target.ClientPresetID, ir.KindClientPreset, &preset)
		spec.Resources[len(spec.Resources)-1].Metadata.Revision = target.ClientPresetRevision
	}
	return spec
}

func policyOverrideSpec(t *testing.T) ir.FrozenInputSpec {
	spec := mustSpec(t)
	group := policyFixture()
	metadata := spec.Resources[0].Metadata
	metadata.ResourceID, metadata.Kind = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ir.KindPolicyGroup
	spec.Resources = append(spec.Resources, ir.Resource{Metadata: metadata, Payload: &group})
	spec.Members = append(spec.Members, ir.FrozenRef{ResourceID: metadata.ResourceID, Kind: ir.KindPolicyGroup, Revision: metadata.Revision, SecurityEpoch: metadata.SecurityEpoch})
	strategy := ir.PolicyFixed
	health := policyHealth()
	member := group.DefaultMember
	spec.Targets[0].PolicyOverrides = map[ir.ID]ir.PolicyOverride{metadata.ResourceID: {PolicyGroupID: metadata.ResourceID, Strategy: &strategy, DefaultMember: &member, HealthCheck: &health}}
	return spec
}

func TestFrozenPolicyOverridesAreValidatedPinnedAndOwned(t *testing.T) {
	spec := policyOverrideSpec(t)
	frozen, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	before := mustJSON(t, frozen)
	if !bytes.Contains(before, []byte(`"policy_overrides":[{`)) {
		t.Fatal("override wire format is not the contractual array")
	}
	if _, err := ir.DecodeFrozenInput(before); err != nil {
		t.Fatal(err)
	}
	key := spec.Targets[0].Key
	target, ok := frozen.Target(key)
	if !ok || !target.Equal(spec.Targets[0]) {
		t.Fatal("equal target not recognized")
	}
	for _, override := range target.PolicyOverrides {
		*override.Strategy = ir.PolicyLatencyBest
		*override.HealthCheck.IntervalMS = 20000
		override.DefaultMember.ResourceID = "22222222-2222-4222-8222-222222222222"
	}
	pinned, _ := frozen.Target(key)
	if pinned.Equal(target) {
		t.Fatal("changed override compares equal")
	}
	copy := frozen.Spec()
	for _, value := range []ir.Target{copy.Targets[0], spec.Targets[0]} {
		for _, override := range value.PolicyOverrides {
			*override.Strategy = ir.PolicyRoundRobin
			*override.HealthCheck.TimeoutMS = 6000
		}
	}
	if !bytes.Equal(before, mustJSON(t, frozen)) || frozen.Validate() != nil {
		t.Fatal("override map or nested pointer aliases frozen input")
	}
	empty := mustSpec(t).Targets[0]
	other := empty.Clone()
	other.PolicyOverrides = map[ir.ID]ir.PolicyOverride{}
	if !empty.Equal(other) {
		t.Fatal("omitted/empty override arrays should have equal semantics")
	}
	for name, mutate := range map[string]func(*ir.FrozenInputSpec){
		"wrong_key": func(v *ir.FrozenInputSpec) {
			for id, override := range v.Targets[0].PolicyOverrides {
				delete(v.Targets[0].PolicyOverrides, id)
				v.Targets[0].PolicyOverrides["bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"] = override
			}
		},
		"missing_group": func(v *ir.FrozenInputSpec) {
			for id, override := range v.Targets[0].PolicyOverrides {
				delete(v.Targets[0].PolicyOverrides, id)
				override.PolicyGroupID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
				v.Targets[0].PolicyOverrides[override.PolicyGroupID] = override
			}
		},
		"nonmember_default": func(v *ir.FrozenInputSpec) {
			for _, override := range v.Targets[0].PolicyOverrides {
				override.DefaultMember.ResourceID = "22222222-2222-4222-8222-222222222222"
			}
		},
		"unused_group": func(v *ir.FrozenInputSpec) { v.Members = v.Members[:len(v.Members)-1] },
	} {
		t.Run(name, func(t *testing.T) {
			value := policyOverrideSpec(t)
			mutate(&value)
			if _, err := ir.NewFrozenInput(value); err == nil {
				t.Fatal("invalid override accepted")
			}
		})
	}
}

func TestTargetPolicyOverrideStrictArrayAndDeterminism(t *testing.T) {
	target := policyOverrideSpec(t).Targets[0]
	wire := mustJSON(t, target)
	var decoded ir.Target
	if err := json.Unmarshal(wire, &decoded); err != nil || !decoded.Equal(target) {
		t.Fatalf("target override roundtrip: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	for name, overrides := range map[string]string{
		"map":           `{"SYNTHETIC_SECRET":{}}`,
		"null":          `null`,
		"duplicate":     `[{"policy_group_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","strategy":"fixed"},{"policy_group_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","strategy":"manual_select"}]`,
		"unknown_field": `[{"policy_group_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","strategy":"fixed","SYNTHETIC_SECRET":"SYNTHETIC_SECRET"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			fields["policy_overrides"] = json.RawMessage(overrides)
			before := mustJSON(t, decoded)
			err := json.Unmarshal(mustJSON(t, fields), &decoded)
			if err == nil || strings.Contains(err.Error(), "SYNTHETIC_SECRET") || !bytes.Equal(before, mustJSON(t, decoded)) {
				t.Fatal("strict array validation, redaction or receiver isolation failed")
			}
		})
	}
	strategy := ir.PolicyFixed
	id := ir.ID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	target.PolicyOverrides[id] = ir.PolicyOverride{PolicyGroupID: id, Strategy: &strategy}
	want := mustJSON(t, target)
	for range 20 {
		if !bytes.Equal(want, mustJSON(t, target)) {
			t.Fatal("map iteration changed serialized override order")
		}
	}
}

func TestOrchestrationPayloadsKeepOpenAPIWireShape(t *testing.T) {
	write := ir.RuleSetWrite{SchemaVersion: 1, Format: ir.DomainCIDRText, Entries: []ir.RuleSetEntry{{Kind: ir.RuleSetDomain, Domain: "example.invalid", Match: ir.DomainExact}}}
	set, err := ir.NewRuleSet(write)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]any{"RoutingProfile": routingFixture(), "DNSProfile": dnsFixture(), "ClientPreset": presetFixture(), "RuleSetWrite": write, "RuleSet": set} {
		t.Run(name, func(t *testing.T) {
			if err := api.Validate(name, parseValue(t, mustJSON(t, value))); err != nil {
				t.Fatal("typed payload no longer matches OpenAPI")
			}
		})
	}
	// Frozen targets add build/revision pins around the existing subscription
	// descriptor; the shared override field must retain the same array shape.
	target := parseValue(t, mustJSON(t, policyOverrideSpec(t).Targets[0])).(map[string]any)
	for _, field := range []string{"core_family", "core_build_sha256", "adapter_version", "client_preset_revision"} {
		delete(target, field)
	}
	if err := api.Validate("SubscriptionTarget", target); err != nil {
		t.Fatal("frozen policy override wire shape differs from subscription API")
	}
}

func TestFrozenOrchestrationClosureDeepCopyAndOrdering(t *testing.T) {
	spec := orchestrationSpec(t)
	frozen, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	before := mustJSON(t, frozen)
	for range len(spec.Resources) {
		spec.Resources = append(spec.Resources[1:], spec.Resources[0])
		got, err := ir.NewFrozenInput(spec)
		if err != nil || !bytes.Equal(before, mustJSON(t, got)) {
			t.Fatalf("resource-order nondeterminism: %v", err)
		}
	}
	if _, err := ir.DecodeFrozenInput(before); err != nil {
		t.Fatal(err)
	}
	for _, resource := range spec.Resources {
		if _, err := ir.DecodeResource(mustJSON(t, resource)); err != nil {
			t.Fatal(err)
		}
	}
	mutate := func(copy *ir.FrozenInputSpec) {
		copy.RoutingProfile.Revision++
		copy.DNSProfile.Revision++
		for _, resource := range copy.Resources {
			switch v := resource.Payload.(type) {
			case *ir.RoutingProfile:
				v.Rules[0].Match.DomainExact[0] = "mutated.invalid"
				v.Rules[0].Match.RuleSetIDs[0] = "mutated"
				v.Rules[0].Match.DestinationPorts[0].To = 1
			case *ir.DNSProfile:
				v.Bootstrap[0].ResolverID = "mutated"
				v.Resolvers[2].Outbound.Builtin = ir.Reject
				v.Rules[0].Match.DomainSuffix[0] = "mutated.invalid"
			case *ir.RuleSet:
				v.Entries[0].Domain = "mutated.invalid"
			case *ir.ClientPreset:
				v.LocalListener.Port = 1234
			}
		}
	}
	mutate(&spec)
	copy := frozen.Spec()
	mutate(&copy)
	if frozen.Validate() != nil || !bytes.Equal(before, mustJSON(t, frozen)) {
		t.Fatal("frozen snapshot leaked mutable aliases")
	}
	copy = frozen.Spec()
	for _, resource := range copy.Resources {
		if route, ok := resource.Payload.(*ir.RoutingProfile); ok {
			slices.Reverse(route.Rules)
		}
	}
	reordered, err := ir.NewFrozenInput(copy)
	if err != nil || bytes.Equal(before, mustJSON(t, reordered)) {
		t.Fatal("semantic rule order was normalized away")
	}
}

func TestFrozenOrchestrationReferenceDiagnostics(t *testing.T) {
	for name, mutate := range map[string]func(*ir.FrozenInputSpec){
		"root_revision":   func(v *ir.FrozenInputSpec) { v.RoutingProfile.Revision++ },
		"root_epoch":      func(v *ir.FrozenInputSpec) { v.DNSProfile.SecurityEpoch++ },
		"root_wrong_kind": func(v *ir.FrozenInputSpec) { v.RoutingProfile.ResourceID = v.DNSProfile.ResourceID },
		"root_missing":    func(v *ir.FrozenInputSpec) { v.DNSProfile.ResourceID = "77777777-7777-4777-8777-777777777777" },
		"dns_missing":     func(v *ir.FrozenInputSpec) { v.DNSProfile = nil },
		"preset_revision": func(v *ir.FrozenInputSpec) { v.Targets[0].ClientPresetRevision++ },
		"ruleset_wrong_kind": func(v *ir.FrozenInputSpec) {
			for _, r := range v.Resources {
				if route, ok := r.Payload.(*ir.RoutingProfile); ok {
					route.Rules[0].Match.RuleSetIDs[0] = v.DNSProfile.ResourceID
				}
			}
		},
		"outbound_missing": func(v *ir.FrozenInputSpec) {
			for _, r := range v.Resources {
				if dns, ok := r.Payload.(*ir.DNSProfile); ok {
					dns.Resolvers[2].Outbound = &ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: "77777777-7777-4777-8777-777777777777"}
				}
			}
		},
		"dns_cidr_set": func(v *ir.FrozenInputSpec) {
			for i, r := range v.Resources {
				if _, ok := r.Payload.(*ir.RuleSet); ok {
					set, _ := ir.NewRuleSet(ir.RuleSetWrite{SchemaVersion: 1, Format: ir.DomainCIDRText, Entries: []ir.RuleSetEntry{{Kind: ir.RuleSetCIDR, CIDR: "192.0.2.0/24"}}})
					v.Resources[i].Payload = &set
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			spec := orchestrationSpec(t)
			mutate(&spec)
			if _, err := ir.NewFrozenInput(spec); err == nil {
				t.Fatal("bad reference accepted")
			}
		})
	}
}
