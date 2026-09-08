package importparse

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

func share(scheme, body string) string { return scheme + "://" + body }

type synthetic struct {
	password, username, uuid, publicKey string
}

func syntheticValues(t testing.TB) synthetic {
	t.Helper()
	data := make([]byte, 64)
	if _, err := rand.Read(data); err != nil {
		t.Fatal("could not generate synthetic test values")
	}
	id := append([]byte(nil), data[:16]...)
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return synthetic{
		password:  "test:" + hex.EncodeToString(data[16:32]) + "+/@?%25#中文",
		username:  "user+" + hex.EncodeToString(data[32:40]) + "%40",
		uuid:      fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]),
		publicKey: base64.RawURLEncoding.EncodeToString(data[32:64]),
	}
}

func (s synthetic) vmess() map[string]any {
	return map[string]any{"v": "2", "ps": "", "add": "vmess.invalid", "port": "443", "id": s.uuid, "aid": "0", "scy": "auto", "net": "tcp", "type": "none", "host": "", "path": "", "tls": ""}
}

func encodeVMess(t testing.TB, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal("could not construct synthetic VMess JSON")
	}
	return base64.StdEncoding.EncodeToString(data)
}

func hasCode(candidate Candidate, code ir.DiagnosticCode) bool {
	for _, d := range candidate.Diagnostics {
		if d.Code == code {
			return true
		}
	}
	return false
}

func requireValid(t testing.TB, candidate Candidate) ir.Node {
	t.Helper()
	if !candidate.Valid() {
		t.Fatalf("candidate was not valid: status=%s diagnostics=%v", candidate.Status, candidate.Diagnostics)
	}
	if err := candidate.Node.Validate(); err != nil {
		t.Fatalf("candidate IR is invalid: %v", err)
	}
	return *candidate.Node
}

func TestFixtureMatrix(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/imports/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		SchemaVersion int `json:"schema_version"`
		Cases         []struct{ ID, Scheme, Body, Status, Protocol, Host, Transport, Security, Name, Code string }
	}
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.SchemaVersion != 1 || len(manifest.Cases) < 20 {
		t.Fatal("invalid import fixture manifest")
	}
	s := syntheticValues(t)
	ws := s.vmess()
	ws["ps"], ws["add"], ws["net"], ws["tls"] = "东京 + 节点", "2001:db8::2", "ws", "tls"
	ws["host"], ws["path"], ws["sni"] = "WS.Invalid", "/ws%2Fraw", "TLS.Invalid"
	missingID := s.vmess()
	delete(missingID, "id")
	replacer := strings.NewReplacer(
		"{{password_encoded}}", url.QueryEscape(s.password), "{{username_encoded}}", url.QueryEscape(s.username),
		"{{uuid}}", s.uuid, "{{public_key}}", s.publicKey,
		"{{ss64}}", base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:"+s.password)),
		"{{ss_empty64}}", base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:")),
		"{{vmess_ws}}", encodeVMess(t, ws), "{{vmess_tcp}}", encodeVMess(t, s.vmess()),
		"{{vmess_missing_id}}", encodeVMess(t, missingID),
	)
	for _, fixture := range manifest.Cases {
		t.Run(fixture.ID, func(t *testing.T) {
			candidate := ParseURI(share(fixture.Scheme, replacer.Replace(fixture.Body)))
			if candidate.Status != fixture.Status {
				t.Fatalf("status=%s want=%s diagnostics=%v", candidate.Status, fixture.Status, candidate.Diagnostics)
			}
			if fixture.Code != "" && !hasCode(candidate, ir.DiagnosticCode(fixture.Code)) {
				t.Fatalf("missing expected diagnostic %s", fixture.Code)
			}
			if fixture.Status != StatusValid {
				if candidate.Node != nil {
					t.Fatal("invalid candidate retained executable Node")
				}
				return
			}
			node := requireValid(t, candidate)
			if node.Protocol != ir.Protocol(fixture.Protocol) || node.Endpoint.Host != fixture.Host || candidate.Name != fixture.Name {
				t.Fatal("fixture connection identity or decoded name differs")
			}
			encoded, _ := json.Marshal(node)
			var fields struct {
				Transport struct{ Kind string }
				Security  struct{ Mode string }
			}
			if json.Unmarshal(encoded, &fields) != nil || fields.Transport.Kind != fixture.Transport || fields.Security.Mode != fixture.Security {
				t.Fatal("fixture transport/security mapping differs")
			}
		})
	}
}

func TestDecodingDoesNotCrossURIComponents(t *testing.T) {
	s := syntheticValues(t)
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		raw := share("ss", encoding.EncodeToString([]byte("aes-256-gcm:"+s.password))+"@[2001:0DB8:0:0::1]:8388#name%2520+中")
		candidate := ParseURI(raw)
		node := requireValid(t, candidate)
		if string(node.Auth.(*ir.MethodPasswordAuth).Password) != s.password || candidate.Name != "name%20+中" || node.Endpoint.Host != "2001:db8::1" {
			t.Fatal("SIP002 component was decoded or normalized incorrectly")
		}
	}
	for _, scheme := range []string{"socks5", "http", "https"} {
		raw := share(scheme, url.UserPassword(s.username, s.password).String()+"@edge.invalid:1080")
		node := requireValid(t, ParseURI(raw))
		auth := node.Auth.(*ir.UsernamePasswordAuth)
		if string(auth.Password) != s.password || string(auth.Username) != s.username {
			t.Fatal("userinfo decoding changed authentication")
		}
	}
	node := requireValid(t, ParseURI(share("vless", s.uuid+"@endpoint.invalid:443?security=tls&type=ws&host=socket.invalid&sni=tls.invalid&path=%2Fa%252Fb%3Fx%3D1")))
	ws := node.Transport.(*ir.WebSocketTransport)
	tls := node.Security.(*ir.TLSSecurity)
	if ws.Path != "/a%2Fb?x=1" || *ws.Host != "socket.invalid" || tls.ServerName != "tls.invalid" || !*tls.VerifyCertificate || node.Endpoint.Host != "endpoint.invalid" {
		t.Fatal("endpoint, WebSocket Host/path, and TLS SNI were conflated")
	}
	vmess := s.vmess()
	vmess["net"], vmess["path"], vmess["tls"] = "ws", "/literal%2Fsegment", "tls"
	vmessNode := requireValid(t, ParseURI(share("vmess", encodeVMess(t, vmess))))
	if vmessNode.Transport.(*ir.WebSocketTransport).Path != "/literal%2Fsegment" {
		t.Fatal("VMess JSON field was URL-decoded")
	}
}

func TestRejectAmbiguousOrUnrepresentableConnections(t *testing.T) {
	s := syntheticValues(t)
	for _, query := range []string{
		"type=tcp&type=tcp", "x-note=a&x-note=a", "sni=a.invalid&%73ni=b.invalid", "security=tls&SNI=a.invalid",
		"security=tls&allowInsecure=false&insecure=0", "security=tls&allowInsecure=true", "security=tls&insecure=1",
		"security=none&sni=a.invalid", "security=tls&pbk=unused", "security=reality&sni=a.invalid&fp=chrome&sid=", "security=tls&flow=unknown",
		"type=grpc", "type=tcp&path=%2F", "type=ws&path=", "type=ws&host=host.invalid%3A443", "type=ws&path=%2F%00",
		"security=tls&alpn=h2%2Ch2", "security=tls&alpn=", "security=tls&fp=", "encryption=secret", "encryption=",
		"security=tls&alpn=h2;http/1.1", "security=tls&&sni=a.invalid", "security=tls&x-note=%FF", "security=tls&serviceName=rpc",
	} {
		candidate := ParseURI(share("vless", s.uuid+"@edge.invalid:443?"+query))
		if candidate.Valid() || candidate.Node != nil || len(candidate.Diagnostics) == 0 {
			t.Fatal("ambiguous, malformed, or unrepresentable connection was accepted")
		}
	}
	for _, body := range []string{
		s.uuid + "@2001:db8::1:443", s.uuid + "@[fe80::1%25eth0]:443", s.uuid + "@edge.invalid:", s.uuid + "@edge.invalid:0", s.uuid + "@edge.invalid:65536",
		s.uuid + "@edge.invalid:443/config", s.uuid + "@edge.invalid:443#bad%FF", s.uuid + ":extra@edge.invalid:443", s.uuid + "@edge.invalid:443#bad%00",
	} {
		if ParseURI(share("vless", body)).Valid() {
			t.Fatal("invalid authority, path, or encoding was accepted")
		}
	}
	for _, body := range []string{"@edge.invalid:1080", ":@edge.invalid:1080", "user:@edge.invalid:1080", ":pass@edge.invalid:1080"} {
		if ParseURI(share("socks5", body)).Valid() {
			t.Fatal("partial authentication was converted to no-auth")
		}
	}
	if ParseURI(share("https", "edge.invalid?security=none")).Valid() || ParseURI(share("http", "edge.invalid?security=tls")).Valid() {
		t.Fatal("CONNECT scheme security was silently overridden")
	}
}

func TestVMessStrictJSONAndExplicitAEAD(t *testing.T) {
	s := syntheticValues(t)
	for _, key := range []string{"v", "add", "port", "id", "aid", "scy", "net", "tls"} {
		value := s.vmess()
		delete(value, key)
		if ParseURI(share("vmess", encodeVMess(t, value))).Valid() {
			t.Fatalf("missing required field %s was defaulted", key)
		}
	}
	for _, mutation := range []struct {
		key   string
		value any
	}{
		{"aid", "1"}, {"aid", nil}, {"scy", "legacy"}, {"net", "grpc"}, {"type", "http"}, {"port", 443.5}, {"port", true}, {"v", 2.1},
		{"ps", nil}, {"tls", nil}, {"allowInsecure", true}, {"id", ""}, {"path", "/ignored"}, {"host", "ignored.invalid"}, {"SNI", "ignored.invalid"},
	} {
		value := s.vmess()
		value[mutation.key] = mutation.value
		if ParseURI(share("vmess", encodeVMess(t, value))).Valid() {
			t.Fatalf("invalid VMess field %s was accepted", mutation.key)
		}
	}
	validJSON, _ := json.Marshal(s.vmess())
	for _, suffix := range []string{
		`,"id":"duplicate"}`, `,"\u0069d":"duplicate"}`, `,"x-note":{"same":1,"same":2}}`, `,"ps":"\ud800"}`, `,"x-note":"\udc00"}`,
	} {
		data := append(append([]byte(nil), validJSON[:len(validJSON)-1]...), suffix...)
		candidate := ParseURI(share("vmess", base64.StdEncoding.EncodeToString(data)))
		if candidate.Valid() || candidate.Node != nil {
			t.Fatal("duplicate JSON key or unpaired surrogate was accepted")
		}
	}
	value := s.vmess()
	value["x-note"] = map[string]any{"safe": []any{true, nil, "isolated"}}
	value["v"], value["port"], value["aid"] = 2, 443, 0
	candidate := ParseURI(share("vmess", encodeVMess(t, value)))
	requireValid(t, candidate)
	if len(candidate.Metadata) != 1 || !hasCode(candidate, UnknownMetadata) {
		t.Fatal("unknown metadata was lost")
	}
	for _, raw := range []string{"[]", "null", string(validJSON) + " {}", `{"x-note":` + strings.Repeat("[", 40) + "0" + strings.Repeat("]", 40) + "}"} {
		if ParseURI(share("vmess", base64.StdEncoding.EncodeToString([]byte(raw)))).Valid() {
			t.Fatal("invalid VMess JSON document was accepted")
		}
	}
}

func TestMetadataAndLoggingBoundaries(t *testing.T) {
	s := syntheticValues(t)
	raw := share("trojan", url.User(s.password).String()+"@edge.invalid:443?x-note="+url.QueryEscape(s.password)+"#"+url.PathEscape(s.password))
	candidate := ParseURI(raw)
	requireValid(t, candidate)
	var isolated string
	if json.Unmarshal(candidate.Metadata["x-note"], &isolated) != nil || isolated != s.password {
		t.Fatal("isolated metadata did not retain its exact value")
	}
	result := Result{Format: FormatText, Candidates: []Candidate{candidate}}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	for _, value := range []any{candidate, candidate.Metadata, result, candidate.Node} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d"} {
			logs.WriteString(fmt.Sprintf(verb, value))
		}
		logger.Info("test", "value", value)
	}
	if strings.Contains(logs.String(), s.password) || strings.Contains(logs.String(), raw) {
		t.Fatal("default formatting or structured logging exposed credentials")
	}
	malicious := ParseURI(share("trojan", url.User(s.password).String()+"@edge.invalid:443?"+url.QueryEscape(s.password)+"=x"))
	diagnostics, _ := json.Marshal(malicious.Diagnostics)
	if malicious.Valid() || bytes.Contains(diagnostics, []byte(s.password)) || bytes.Contains(diagnostics, []byte(url.QueryEscape(s.password))) {
		t.Fatal("unknown key was executable or echoed in diagnostics")
	}
	encoded, _ := json.Marshal(candidate)
	if !bytes.Contains(encoded, []byte(s.password)) {
		t.Fatal("explicit candidate JSON must retain credentials for encrypted persistence")
	}
	metadataJSON, _ := json.Marshal(candidate.Node.Extensions)
	if string(metadataJSON) != "{}" {
		t.Fatal("metadata leaked into executable extensions")
	}
}

func requireBatchError(t testing.TB, input []byte, format string, code ir.DiagnosticCode) {
	t.Helper()
	result, err := Parse(context.Background(), format, input)
	var diagnostics ir.Diagnostics
	if err == nil || !errors.As(err, &diagnostics) || len(diagnostics) == 0 || diagnostics[0].Code != code || len(result.Candidates) != 0 {
		t.Fatalf("batch error mismatch: expected=%s actual=%v candidates=%d", code, err, len(result.Candidates))
	}
}

func TestBatchNormalizationAndFormatOverride(t *testing.T) {
	body := []byte("\xef\xbb\xbf\r\n" + share("http", "edge.invalid") + "\r\n\r\nbroken line\r" + share("unknown", "edge.invalid:443") + "\n")
	result, err := Parse(context.Background(), FormatAuto, body)
	if err != nil || len(result.Candidates) != 3 || result.Candidates[0].Line != 2 || result.Candidates[1].Line != 4 || result.Candidates[2].Line != 5 {
		t.Fatalf("physical source positions were not retained: %v", err)
	}
	for index, candidate := range result.Candidates {
		if candidate.Index != index {
			t.Fatal("source indexes were not stable")
		}
	}
	if !result.Candidates[0].Valid() || result.Candidates[1].Status != StatusInvalid || result.Candidates[2].Status != StatusUnsupported {
		t.Fatal("mixed invalid entries prevented valid preview")
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		for layers := 1; layers <= 2; layers++ {
			encoded := body
			for range layers {
				encoded = []byte(encoding.EncodeToString(encoded))
			}
			for _, format := range []string{FormatAuto, FormatBase64} {
				decoded, err := Parse(context.Background(), format, encoded)
				if err != nil || decoded.Base64Layers != layers || decoded.Format != FormatBase64 || len(decoded.Candidates) != 3 || decoded.Candidates[1].Line != 4 {
					t.Fatalf("Base64 envelope normalization failed: %v", err)
				}
			}
		}
	}
	encoded := []byte(base64.StdEncoding.EncodeToString(body))
	textResult, err := Parse(context.Background(), FormatText, encoded)
	if err != nil || textResult.Base64Layers != 0 || textResult.Candidates[0].Valid() {
		t.Fatal("explicit text format did not disable Base64 detection")
	}
	requireBatchError(t, body, FormatBase64, InvalidBase64)
	for range 2 {
		encoded = []byte(base64.StdEncoding.EncodeToString(encoded))
	}
	requireBatchError(t, encoded, FormatAuto, TooManyLayers)
	requireBatchError(t, []byte("%%%"), FormatBase64, InvalidBase64)
	requireBatchError(t, []byte("Zh=="), FormatBase64, InvalidBase64)
	requireBatchError(t, []byte{0xff}, FormatText, InvalidUTF8)
	requireBatchError(t, nil, FormatAuto, EmptyInput)
	requireBatchError(t, body, "json", InvalidFormat)
	for _, native := range [][]byte{[]byte(`{"outbounds":[]}`), []byte("proxies:\n  - name: demo"), []byte("---\nproxies: []"), {0x1f, 0x8b, 0x08}, []byte("PK\x03\x04")} {
		for _, format := range []string{FormatAuto, FormatText} {
			requireBatchError(t, native, format, NativeConfig)
		}
		requireBatchError(t, []byte(base64.StdEncoding.EncodeToString(native)), FormatAuto, NativeConfig)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Parse(ctx, FormatText, body); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled request was parsed")
	}
}

func TestBatchBudgetsFailWithoutTruncation(t *testing.T) {
	entry := share("unknown", "edge.invalid") + "\n"
	exact := []byte(strings.Repeat(entry, MaxEntries))
	result, err := Parse(context.Background(), FormatText, exact)
	if err != nil || len(result.Candidates) != MaxEntries {
		t.Fatalf("entry limit rejected its boundary: %v", err)
	}
	requireBatchError(t, append(exact, []byte(entry)...), FormatText, TooManyEntries)
	prefix := share("http", "edge.invalid#")
	raw := prefix + strings.Repeat("n", MaxURIBytes-len(prefix))
	requireValid(t, ParseURI(raw))
	tooLong := ParseURI(raw + "n")
	if tooLong.Valid() || !hasCode(tooLong, URITooLarge) {
		t.Fatal("URI byte budget was not enforced")
	}
	decoded := []byte(prefix + strings.Repeat("n", MaxDecodedBytes-len(prefix)))
	result, err = Parse(context.Background(), FormatText, decoded)
	if err != nil || len(result.Candidates) != 1 || !hasCode(result.Candidates[0], URITooLarge) {
		t.Fatalf("decoded budget boundary was not retained as per-line error: %v", err)
	}
	requireBatchError(t, append(decoded, 'n'), FormatText, DecodedTooLarge)
	requireBatchError(t, append([]byte{0xef, 0xbb, 0xbf}, decoded...), FormatText, DecodedTooLarge)
	requireBatchError(t, make([]byte, MaxInputBytes+1), FormatAuto, InputTooLarge)
	for range 2 {
		decoded = []byte(base64.StdEncoding.EncodeToString(decoded))
	}
	result, err = Parse(context.Background(), FormatBase64, decoded)
	if err != nil || result.Base64Layers != 2 || len(result.Candidates) != 1 {
		t.Fatalf("maximum decoded list could not traverse two bounded envelopes: %v", err)
	}
}

func TestCanonicalConnectionExcludesOriginAndName(t *testing.T) {
	s := syntheticValues(t)
	raw := share("vless", s.uuid+"@EDGE.invalid:443?security=tls&type=ws&host=ws.invalid&path=%2F#")
	first := requireValid(t, ParseURI(raw+"one"))
	second := requireValid(t, ParseURI(raw+"two"))
	second.Origin = &ir.Origin{SourceResourceID: ir.ID(s.uuid), SourceItemID: ir.ID(s.uuid), MatchMethod: ir.ExactFingerprint}
	a, err := CanonicalConnection(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalConnection(second)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatal("display name or source identity affected connection fingerprint")
	}
	second.Endpoint.Port++
	b, _ = CanonicalConnection(second)
	if bytes.Equal(a, b) {
		t.Fatal("different endpoint was collapsed in connection fingerprint")
	}
	if _, err := CanonicalConnection(ir.Node{}); err == nil {
		t.Fatal("invalid Node received fingerprint material")
	}
}
