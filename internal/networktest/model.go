// Package networktest prepares bounded tests from typed, immutable resources.
// It is shared by API preparation and Runner probes and owns no database or keys.
package networktest

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/adapter/mihomo"
	"github.com/Runarry/ProxyLoom/internal/adapter/singbox"
	"github.com/Runarry/ProxyLoom/internal/adapter/xray"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"github.com/Runarry/ProxyLoom/internal/safefetch"
)

var ErrTarget = errors.New("test_target_invalid")
var ErrAddress = errors.New("test_address_rejected")

type TargetConfig struct {
	URL               string                         `json:"url"`
	AllowedTypes      []string                       `json:"allowed_types"`
	ExpectedResponse  runnerprotocol.HTTPExpectation `json:"expected_response"`
	Limits            runnerprotocol.Limits          `json:"limits"`
	PermissionBasis   string                         `json:"permission_basis"`
	RedirectPolicy    string                         `json:"redirect_policy"`
	VerifyCertificate bool                           `json:"verify_certificate"`
	Compression       string                         `json:"compression"`
}

func (c TargetConfig) Validate() error {
	if runnerprotocol.ValidateTestURL(c.URL) != nil || c.ExpectedResponse.Validate() != nil || len(c.AllowedTypes) < 1 || len(c.AllowedTypes) > 2 || c.Limits.DurationMS < 1 || c.Limits.DurationMS > 60000 || c.Limits.MaxBytes < 1 || c.Limits.MaxBytes > 1<<30 || c.PermissionBasis != "self_owned" && c.PermissionBasis != "explicitly_authorized" || c.RedirectPolicy != "deny" || !c.VerifyCertificate || c.Compression != "disabled" {
		return ErrTarget
	}
	for i, v := range c.AllowedTypes {
		if v != "connectivity" && v != "download_throughput" || slices.Contains(c.AllowedTypes[:i], v) {
			return ErrTarget
		}
	}
	return nil
}

type Target struct {
	ID          ir.ID                   `json:"test_target_id"`
	Revision    runnerprotocol.Sequence `json:"revision"`
	Name        string                  `json:"name"`
	Enabled     bool                    `json:"enabled"`
	Config      TargetConfig            `json:"target"`
	SafetyState string                  `json:"safety_state"`
	CheckedAt   *time.Time              `json:"safety_checked_at,omitempty"`
	Diagnostics []ir.Diagnostic         `json:"diagnostics"`
	CreatedAt   time.Time               `json:"created_at"`
}
type TargetRequest struct {
	Name    string       `json:"name"`
	Enabled *bool        `json:"enabled,omitempty"`
	Config  TargetConfig `json:"target"`
}

func (r TargetRequest) Validate() error {
	if !utf8.ValidString(r.Name) || utf8.RuneCountInString(r.Name) < 1 || utf8.RuneCountInString(r.Name) > 256 {
		return ErrTarget
	}
	return r.Config.Validate()
}

type Resolver struct {
	DNS safefetch.Resolver
	// Only controlled test harnesses populate this field. Production
	// constructors never copy network exceptions from requests or settings.
	AllowNets []netip.Prefix
	// ProxyAllowNets is derived only from the administrator-owned system setting.
	// It is intentionally not used to resolve HTTP test targets.
	ProxyAllowNets []netip.Prefix
}

func (r Resolver) Approved(ip netip.Addr) bool {
	if ip.Zone() != "" {
		return false
	}
	for _, prefix := range r.AllowNets {
		if prefix.Contains(ip.Unmap()) {
			return true
		}
	}
	return !safefetch.BlockedAddress(ip)
}
func (r Resolver) Resolve(ctx context.Context, host string) ([]string, error) {
	return r.resolve(ctx, host, r.Approved)
}

// ResolveEndpoint permits a configured private proxy endpoint while retaining
// the public-only rule for every other caller of Resolve.
func (r Resolver) ResolveEndpoint(ctx context.Context, host string) ([]string, error) {
	return r.resolve(ctx, host, r.ApprovedEndpoint)
}

func (r Resolver) ApprovedEndpoint(ip netip.Addr) bool {
	if r.Approved(ip) {
		return true
	}
	for _, prefix := range r.ProxyAllowNets {
		if prefix.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}

func (r Resolver) PrivateEndpointAuthorized(ip netip.Addr) bool {
	for _, prefix := range r.ProxyAllowNets {
		if prefix.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}

func PrivateProxyAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	for _, prefix := range []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
	} {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func (r Resolver) resolve(ctx context.Context, host string, approved func(netip.Addr) bool) ([]string, error) {
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else {
		dns := r.DNS
		if dns == nil {
			dns = net.DefaultResolver
		}
		var err error
		ips, err = dns.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, ErrAddress
		}
	}
	if len(ips) < 1 || len(ips) > 16 {
		return nil, ErrAddress
	}
	values := []string{}
	for _, ip := range ips {
		if !approved(ip) {
			return nil, ErrAddress
		}
		value := ip.Unmap().String()
		if !slices.Contains(values, value) {
			values = append(values, value)
		}
	}
	sort.Strings(values)
	return values, nil
}

// Compile copies nodes before binding approved IPs. It never mutates catalog
// objects and never resolves DNS. No user routing or direct fallback is added.
func Compile(family ir.CoreFamily, snapshot ir.ID, subject ir.Resource, resources map[ir.ID]ir.Resource, addresses map[ir.ID]string) (adapter.Artifact, []runnerprotocol.ApprovedEndpoint, error) {
	input := adapter.EmitInput{SnapshotID: snapshot, TargetKey: "network-test", FinalTag: "test_out"}
	var endpoints []runnerprotocol.ApprovedEndpoint
	node := func(id ir.ID) (ir.Resource, error) {
		r, ok := resources[id]
		if !ok || r.Metadata.Kind != ir.KindNode {
			return ir.Resource{}, ErrTarget
		}
		data, err := json.Marshal(r)
		if err != nil {
			return ir.Resource{}, ErrTarget
		}
		defer clear(data)
		copy, err := ir.DecodeResource(data)
		if err != nil {
			return ir.Resource{}, err
		}
		n := copy.Payload.(*ir.Node)
		if addresses == nil {
			return copy, nil
		} // Offline validation has no DNS preparation.
		original := n.Endpoint.Host
		ip, err := netip.ParseAddr(addresses[id])
		if err != nil {
			return ir.Resource{}, ErrAddress
		}
		n.Endpoint.Host = ip.String()
		if tls, ok := n.Security.(*ir.TLSSecurity); ok && tls.ServerName == "" {
			tls.ServerName = original
		}
		if ws, ok := n.Transport.(*ir.WebSocketTransport); ok && ws.Host == nil {
			host := original
			if n.Endpoint.Port != 80 && n.Endpoint.Port != 443 {
				host = net.JoinHostPort(original, strconv.Itoa(n.Endpoint.Port))
			}
			ws.Host = &host
		}
		endpoints = append(endpoints, runnerprotocol.ApprovedEndpoint{IP: ip.String(), Port: n.Endpoint.Port})
		return copy, nil
	}
	switch subject.Metadata.Kind {
	case ir.KindNode:
		r, err := node(subject.Metadata.ResourceID)
		if err != nil {
			return adapter.Artifact{}, nil, err
		}
		input.Independents = []adapter.IndependentOutbound{{Tag: "test_out", Resource: r}}
	case ir.KindChain:
		chain, ok := subject.Payload.(*ir.Chain)
		if !ok || chain.Validate() != nil || len(chain.Hops) != 2 {
			return adapter.Artifact{}, nil, ErrTarget
		}
		h1, err := node(chain.Hops[0].NodeID)
		if err != nil {
			return adapter.Artifact{}, nil, err
		}
		h2, err := node(chain.Hops[1].NodeID)
		if err != nil {
			return adapter.Artifact{}, nil, err
		}
		input.Chains = []adapter.ChainInstance{{ResourceID: subject.Metadata.ResourceID, Revision: subject.Metadata.Revision, TagH1: "test_h1", TagH2: "test_out", Hop1: h1, Hop2: h2, FailurePolicy: ir.FailClosed}}
	default:
		return adapter.Artifact{}, nil, ErrTarget
	}
	var artifact adapter.Artifact
	var err error
	switch family {
	case ir.Xray:
		artifact, _, err = xray.Emit(input)
	case ir.SingBox:
		artifact, _, err = singbox.Emit(input)
	case ir.Mihomo:
		artifact, _, err = mihomo.Emit(input)
	default:
		err = ErrTarget
	}
	return artifact, endpoints, err
}
