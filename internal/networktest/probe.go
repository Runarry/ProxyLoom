package networktest

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"golang.org/x/net/proxy"
)

type ProbeOptions struct {
	SOCKSAddress string
	RootCAs      *x509.CertPool
}
type ProbeResult struct {
	Metrics     runnerprotocol.Metrics
	Verdict     string
	Error       *runnerprotocol.SafeError
	TruncatedBy string
}
type sample struct {
	dial, tls, ttfb, total, body float64
	bytes                        int64
	code                         string
	truncated                    string
	egress                       string
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
func median(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	slices.Sort(values)
	v := values[len(values)/2]
	if len(values)%2 == 0 {
		v = (values[len(values)/2-1] + v) / 2
	}
	return &v
}

// Probe never falls back from SOCKS to a direct request. The separate HEAD
// health check can only downgrade a failed verdict to inconclusive.
func Probe(ctx context.Context, p runnerprotocol.FrozenPayload, options ProbeOptions) ProbeResult {
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.Limits.DurationMS)*time.Millisecond)
	defer cancel()
	r := ProbeResult{Verdict: "fail", TruncatedBy: "none"}
	count := 1
	if p.Type == "connectivity" {
		count = 3
	}
	var samples, failures int32
	var bytes int64
	var body float64
	var dialTimes, tlsTimes, ttfbTimes, totalTimes []float64
	var egress *string
	for i := 0; i < count && ctx.Err() == nil; i++ {
		remaining := p.Limits.MaxBytes - bytes
		if remaining <= 0 {
			r.TruncatedBy = "bytes"
			break
		}
		limit := remaining
		if p.Type == "connectivity" {
			limit = min(remaining, p.TestTarget.ExpectedResponse.MaxResponseBytes, 64<<10)
		}
		s := probeSample(ctx, p, options, limit)
		bytes += s.bytes
		body += s.body
		samples++
		if s.truncated != "none" {
			r.TruncatedBy = s.truncated
		}
		if s.code != "" {
			failures++
			r.Error = runnerprotocol.Safe(s.code)
		} else {
			if s.egress != "" {
				value := s.egress
				egress = &value
			}
			dialTimes = append(dialTimes, s.dial)
			if s.tls > 0 {
				tlsTimes = append(tlsTimes, s.tls)
			}
			ttfbTimes = append(ttfbTimes, s.ttfb)
			totalTimes = append(totalTimes, s.total)
		}
	}
	r.Metrics = runnerprotocol.Metrics{ProxyDialMS: median(dialTimes), TargetTLSMS: median(tlsTimes), HTTPTTFBMS: median(ttfbTimes), HTTPTotalMS: median(totalTimes), BodyBytes: &bytes, BodyDurationMS: &body, SampleCount: &samples, FailureCount: &failures}
	r.Metrics.EgressIP = egress
	if samples == int32(count) && failures == 0 {
		r.Verdict = "pass"
		r.Error = nil
	}
	if p.Type == "download_throughput" && r.Verdict == "pass" {
		if body < 1000 || bytes < p.MinimumSampleBytes {
			r.Verdict = "inconclusive"
			r.Error = runnerprotocol.Safe("INSUFFICIENT_SAMPLE")
		} else {
			speed := float64(bytes) * 8 / (body * 1000)
			r.Metrics.ThroughputMbps = &speed
		}
	}
	if r.Verdict == "fail" && parent.Err() == nil && !healthyTarget(parent, p.TestTarget, options.RootCAs) {
		r.Verdict = "inconclusive"
		r.Error = runnerprotocol.Safe("TEST_TARGET_UNAVAILABLE")
	}
	return r
}

func targetAddress(t *runnerprotocol.FrozenTestTarget) (string, *url.URL) {
	u, _ := url.Parse(t.URL)
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(t.ValidatedIPs[0], port), u
}
func probeSample(ctx context.Context, p runnerprotocol.FrozenPayload, options ProbeOptions, limit int64) (s sample) {
	s = sample{truncated: "none"}
	var timingMu sync.Mutex
	var timings sample
	defer func() {
		timingMu.Lock()
		defer timingMu.Unlock()
		s.dial = timings.dial
		s.tls = timings.tls
		s.ttfb = timings.ttfb
		if timings.code != "" {
			s.code = timings.code
		}
	}()
	address, u := targetAddress(p.TestTarget)
	forward := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: -1}
	socks, err := proxy.SOCKS5("tcp", options.SOCKSAddress, nil, forward)
	if err != nil {
		s.code = "PROXY_CONNECT_FAILED"
		return s
	}
	dialer, ok := socks.(proxy.ContextDialer)
	if !ok {
		s.code = "PROXY_CONNECT_FAILED"
		return s
	}
	var tlsStart time.Time
	transport := &http.Transport{Proxy: nil, DisableCompression: true, DisableKeepAlives: true, ForceAttemptHTTP2: false, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 64 << 10, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname(), RootCAs: options.RootCAs}, DialContext: func(c context.Context, network, ignored string) (net.Conn, error) {
		start := time.Now()
		conn, err := dialer.DialContext(c, "tcp", address)
		timingMu.Lock()
		timings.dial = ms(time.Since(start))
		timingMu.Unlock()
		return conn, err
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Duration(p.Limits.DurationMS) * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	start := time.Now()
	trace := &httptrace.ClientTrace{TLSHandshakeStart: func() { timingMu.Lock(); tlsStart = time.Now(); timingMu.Unlock() }, TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
		timingMu.Lock()
		defer timingMu.Unlock()
		timings.tls = ms(time.Since(tlsStart))
		if err != nil {
			timings.code = "TARGET_TLS_FAILED"
		}
	}, GotFirstResponseByte: func() { timingMu.Lock(); timings.ttfb = ms(time.Since(start)); timingMu.Unlock() }}
	request, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, p.TestTarget.URL, nil)
	if err != nil {
		s.code = "INVALID_CONFIG"
		return s
	}
	request.Header.Set("Cache-Control", "no-store, no-cache, max-age=0")
	request.Header.Set("Pragma", "no-cache")
	request.Header.Set("Accept-Encoding", "identity")
	response, err := client.Do(request)
	if err != nil {
		if s.code == "" {
			s.code = "PROXY_CONNECT_FAILED"
		}
		s.total = ms(time.Since(start))
		return s
	}
	defer response.Body.Close()
	if !slices.Contains(p.TestTarget.ExpectedResponse.StatusCodes, response.StatusCode) || response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity" {
		s.code = "HTTP_EXPECTATION_FAILED"
		return s
	}
	if age, err := strconv.ParseInt(response.Header.Get("Age"), 10, 64); err == nil && age > 0 {
		s.code = "HTTP_EXPECTATION_FAILED"
		return s
	}
	readStart := time.Now()
	hash := sha256.New()
	buffer := make([]byte, 32<<10)
	var egressBody []byte
	complete := false
	for s.bytes < limit {
		n, readErr := response.Body.Read(buffer[:min(int64(len(buffer)), limit-s.bytes)])
		if n > 0 {
			s.bytes += int64(n)
			_, _ = hash.Write(buffer[:n])
			if p.TestTarget.ExpectedResponse.EgressIPResponse && len(egressBody) < 65 {
				egressBody = append(egressBody, buffer[:min(n, 65-len(egressBody))]...)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				complete = true
			} else if p.Type == "download_throughput" && (errors.Is(readErr, context.DeadlineExceeded) || isTimeout(readErr)) {
				s.truncated = "duration"
			} else {
				s.code = "HTTP_EXPECTATION_FAILED"
			}
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	s.body = ms(time.Since(readStart))
	s.total = ms(time.Since(start))
	if s.bytes == limit {
		s.truncated = "bytes"
		if response.ContentLength == s.bytes {
			complete = true
		}
	}
	if p.Type == "connectivity" && !complete {
		s.code = "HTTP_EXPECTATION_FAILED"
	}
	if expected := p.TestTarget.ExpectedResponse.BodySHA256; expected != "" && (!complete || hex.EncodeToString(hash.Sum(nil)) != expected) {
		s.code = "HTTP_EXPECTATION_FAILED"
	}
	if p.TestTarget.ExpectedResponse.EgressIPResponse {
		ip, err := netip.ParseAddr(strings.TrimSpace(string(egressBody)))
		if err != nil || !complete || s.bytes > 64 || ip.Zone() != "" {
			s.code = "HTTP_EXPECTATION_FAILED"
		} else {
			s.egress = ip.Unmap().String()
		}
	}
	return s
}
func isTimeout(err error) bool { var e net.Error; return errors.As(err, &e) && e.Timeout() }
func healthyTarget(ctx context.Context, target *runnerprotocol.FrozenTestTarget, roots *x509.CertPool) bool {
	address, u := targetAddress(target)
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname(), RootCAs: roots}, DialContext: func(c context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(c, "tcp", address)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, target.URL, nil)
	if err != nil {
		return false
	}
	request.Header.Set("Cache-Control", "no-cache")
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	_ = response.Body.Close()
	return slices.Contains(target.ExpectedResponse.StatusCodes, response.StatusCode) && !strings.Contains(response.Header.Get("Warning"), "stale")
}
