package runnerprotocol

import (
	"math"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

type HTTPExpectation struct {
	StatusCodes      []int  `json:"status_codes"`
	BodySHA256       string `json:"body_sha256,omitempty"`
	MaxResponseBytes int64  `json:"max_response_bytes"`
	EgressIPResponse bool   `json:"egress_ip_response"`
}
type FrozenTestTarget struct {
	TestTargetID      ir.ID           `json:"test_target_id"`
	Revision          Sequence        `json:"revision"`
	URL               string          `json:"url"`
	ValidatedIPs      []string        `json:"validated_ips"`
	ExpectedResponse  HTTPExpectation `json:"expected_response"`
	RedirectPolicy    string          `json:"redirect_policy"`
	VerifyCertificate bool            `json:"verify_certificate"`
	Compression       string          `json:"compression"`
}
type ApprovedEndpoint struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

type NetworkObservation struct {
	Location        string `json:"location"`
	ExecutionSHA256 string `json:"execution_sha256"`
	TruncatedBy     string `json:"truncated_by"`
	CPUThrottled    *bool  `json:"cpu_throttled,omitempty"`
}

func (o *NetworkObservation) Validate() error {
	if o == nil {
		return nil
	}
	if len(o.Location) < 1 || len(o.Location) > 128 || strings.ContainsAny(o.Location, "\r\n\x00") || !ValidDigest(o.ExecutionSHA256) || o.TruncatedBy != "none" && o.TruncatedBy != "duration" && o.TruncatedBy != "bytes" {
		return ErrInvalid
	}
	return nil
}

func ValidateTestURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 4096 || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(raw, "\r\n\x00") {
		return ErrInvalid
	}
	if u.Port() != "" {
		if _, err := netip.ParseAddrPort("127.0.0.1:" + u.Port()); err != nil {
			return ErrInvalid
		}
	}
	return nil
}
func (e HTTPExpectation) Validate() error {
	if len(e.StatusCodes) < 1 || len(e.StatusCodes) > 10 || e.MaxResponseBytes < 1 || e.MaxResponseBytes > 1<<30 || e.BodySHA256 != "" && !ValidDigest(e.BodySHA256) {
		return ErrInvalid
	}
	for i, code := range e.StatusCodes {
		if code < 100 || code > 599 || slices.Contains(e.StatusCodes[:i], code) {
			return ErrInvalid
		}
	}
	return nil
}
func ValidatePayload(p FrozenPayload) error {
	if p.Type == "config_validate" {
		return ValidateConfigPayload(p)
	}
	if p.Type != "connectivity" && p.Type != "download_throughput" {
		return ErrInvalid
	}
	if p.Subject == nil || p.QuotaReservationID.Validate() != nil || p.TestTarget == nil || p.ExecutionPolicy.Network != "controlled_target_only" || p.ExecutionPolicy.ProcessLimit < 1 || p.ExecutionPolicy.ProcessLimit > 128 || p.Limits.MaxBytes < 1 || p.Limits.MaxBytes > 1<<30 || p.Limits.DurationMS > 60000 || p.MinimumSampleBytes < 1 || p.MinimumSampleBytes > 1<<30 || len(p.ApprovedEndpoints) < 1 || len(p.ApprovedEndpoints) > 2 || len(p.Dependencies) < 1 || len(p.Dependencies) > 3 {
		return ErrInvalid
	}
	check := p
	check.Type = "config_validate"
	check.ExecutionPolicy.Network = "none"
	check.ExecutionPolicy.ProcessLimit = min(p.ExecutionPolicy.ProcessLimit, 32)
	check.Limits.MaxBytes = 0
	check.TestTarget = nil
	check.ApprovedEndpoints = nil
	check.Dependencies = nil
	check.MinimumSampleBytes = 0
	if ValidateConfigPayload(check) != nil {
		return ErrInvalid
	}
	t := p.TestTarget
	if t.TestTargetID.Validate() != nil || t.Revision < 1 || ValidateTestURL(t.URL) != nil || t.ExpectedResponse.Validate() != nil || t.RedirectPolicy != "deny" || !t.VerifyCertificate || t.Compression != "disabled" || len(t.ValidatedIPs) < 1 || len(t.ValidatedIPs) > 16 {
		return ErrInvalid
	}
	for _, v := range t.ValidatedIPs {
		ip, err := netip.ParseAddr(v)
		if err != nil || ip.Zone() != "" {
			return ErrInvalid
		}
	}
	for _, v := range p.ApprovedEndpoints {
		ip, err := netip.ParseAddr(v.IP)
		if err != nil || ip.Zone() != "" || v.Port < 1 || v.Port > 65535 {
			return ErrInvalid
		}
	}
	seen := map[ir.ID]bool{}
	for _, s := range p.Dependencies {
		if s.ID.Validate() != nil || seen[s.ID] || s.Revision < 1 || s.SecurityEpoch < 1 || s.Kind != ir.KindNode && s.Kind != ir.KindChain {
			return ErrInvalid
		}
		seen[s.ID] = true
	}
	if !seen[p.Subject.ID] || p.Subject.Kind == ir.KindNode && (len(p.Dependencies) != 1 || len(p.ApprovedEndpoints) != 1) || p.Subject.Kind == ir.KindChain && (len(p.Dependencies) != 3 || len(p.ApprovedEndpoints) != 2) {
		return ErrInvalid
	}
	for _, d := range p.Dependencies {
		if d.ID == p.Subject.ID && d != *p.Subject || d.ID != p.Subject.ID && d.Kind != ir.KindNode {
			return ErrInvalid
		}
	}
	return nil
}

func ValidateMetrics(m Metrics) error {
	for _, v := range []*float64{m.ConfigCheckMS, m.CoreStartMS, m.ProxyDialMS, m.TargetTLSMS, m.HTTPTTFBMS, m.HTTPTotalMS, m.BodyDurationMS} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > 300000) {
			return ErrInvalid
		}
	}
	if m.BodyBytes != nil && (*m.BodyBytes < 0 || *m.BodyBytes > 1<<30) {
		return ErrInvalid
	}
	if m.SampleCount != nil && (*m.SampleCount < 0 || *m.SampleCount > 3) || m.FailureCount != nil && (*m.FailureCount < 0 || *m.FailureCount > 3) {
		return ErrInvalid
	}
	if m.ThroughputMbps != nil {
		if math.IsNaN(*m.ThroughputMbps) || math.IsInf(*m.ThroughputMbps, 0) || *m.ThroughputMbps < 0 || m.BodyBytes == nil || m.BodyDurationMS == nil || *m.BodyDurationMS < 1000 {
			return ErrInvalid
		}
		expected := float64(*m.BodyBytes) * 8 / (*m.BodyDurationMS * 1000)
		if math.Abs(*m.ThroughputMbps-expected) > max(0.000001, expected*0.000001) {
			return ErrInvalid
		}
	}
	if m.FailureCount != nil && (m.SampleCount == nil || *m.FailureCount > *m.SampleCount) {
		return ErrInvalid
	}
	if m.EgressIP != nil {
		if _, err := netip.ParseAddr(*m.EgressIP); err != nil {
			return ErrInvalid
		}
	}
	return nil
}
