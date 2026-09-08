package apicontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

const scopeID ir.ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const nodeID ir.ID = "11111111-1111-4111-8111-111111111111"

func fixtureNode(t *testing.T, name string) ir.Node {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "ir", "positive", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	node, err := ir.DecodeNode(data)
	if err != nil {
		t.Fatal(err)
	}
	return node
}
func resource(node ir.Node) ir.Resource {
	return ir.Resource{Metadata: ir.Metadata{ResourceID: nodeID, ScopeID: scopeID, Kind: ir.KindNode, Revision: 1, SchemaVersion: 1, Name: "Synthetic node", Tags: []string{}, Enabled: true, SecurityEpoch: 1}, Payload: &node}
}
func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func expectStatus(t *testing.T, err error, status int) {
	t.Helper()
	if err == nil || AsError(err).HTTPStatus() != status {
		t.Fatalf("status: got %v, want %d", err, status)
	}
}

func TestStrictJSONBoundary(t *testing.T) {
	valid := `{"name":"Synthetic chain","hops":[{"node_id":"11111111-1111-4111-8111-111111111111"},{"node_id":"22222222-2222-4222-8222-222222222222"}],"failure_policy":"fail_closed"}`
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"valid", valid, 0},
		{"unknown", strings.Replace(valid, `"name":`, `"credential-key-must-not-echo":"sensitive-value","name":`, 1), 400},
		{"case-sensitive", strings.Replace(valid, `"name"`, `"Name"`, 1), 400},
		{"duplicate", strings.Replace(valid, `"name":`, `"name":"sensitive-value","name":`, 1), 400},
		{"duplicate-unknown", strings.Replace(valid, `"name":`, `"credential-key-must-not-echo":0,"credential-key-must-not-echo":1,"name":`, 1), 400},
		{"trailing", valid + ` {}`, 400},
		{"null-name", strings.Replace(valid, `"Synthetic chain"`, `null`, 1), 400},
		{"null-array", strings.Replace(valid, `"hops":[`, `"tags":null,"hops":[`, 1), 400},
		{"surrogate", strings.Replace(valid, `Synthetic chain`, `\ud800`, 1), 400},
		{"utf8", strings.Replace(valid, `Synthetic chain`, string([]byte{0xff}), 1), 400},
		{"root-null", "null", 400},
		{"hostile-exponent", `{"value":1e1000000000}`, 400},
		{"hostile-negative-exponent", `{"value":1e-1000000000}`, 400},
		{"huge-number-literal", `{"value":` + strings.Repeat("1", 129) + `}`, 400},
		{"depth", strings.Repeat("[", 130) + strings.Repeat("]", 130), 413},
		{"size", strings.Repeat(" ", MaxJSONBytes+1), 413},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := ChainCreateRequest{Name: "unchanged"}
			destination := original
			err := Decode([]byte(test.body), "ChainCreateRequest", &destination)
			if test.status == 0 {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = destination.Input(); err != nil {
					t.Fatal(err)
				}
				return
			}
			expectStatus(t, err, test.status)
			if !reflect.DeepEqual(original, destination) {
				t.Fatal("failed decode modified destination")
			}
			encoded := string(mustJSON(t, AsError(err).Response("request-test")))
			if strings.Contains(encoded, "sensitive-value") || strings.Contains(encoded, "credential-key-must-not-echo") {
				t.Fatal("unsafe error detail")
			}
		})
	}
}

func TestExactDecimalCanonicalization(t *testing.T) {
	for _, group := range [][]string{
		{"1", "1.0", "1e0", "10e-1"},
		{"0.5", "0.50", "5e-1", "5000e-4"},
		{"-12.5", "-12.500", "-125e-1"},
		{"0", "-0.0", "0e10"},
		{"9007199254740993", "9007199254740993.00", "90071992547409930e-1"},
	} {
		for _, spelling := range group {
			if !boundedNumber(spelling) || canonicalNumber(spelling) != group[0] {
				t.Fatalf("decimal normalization failed for %s", spelling)
			}
		}
	}
	for _, spelling := range []string{"1e309", "1e-309", "1e1000000000", strings.Repeat("9", 129)} {
		if boundedNumber(spelling) {
			t.Fatal("unbounded number accepted")
		}
	}
	var previous []byte
	for _, number := range []string{"0.5", "0.50", "5e-1"} {
		canonical, err := CanonicalRequest([]byte(`{"config_check_ms":`+number+`}`), "TestMetrics")
		if err != nil {
			t.Fatal(err)
		}
		if previous != nil && !bytes.Equal(previous, canonical) {
			t.Fatal("equal decimal requests have different idempotency bytes")
		}
		previous = canonical
	}
}

func TestNodeCreateDTOAndServerOwnedMetadata(t *testing.T) {
	for _, name := range []string{"trojan-a", "shadowsocks-aead", "vmess-websocket", "vless-reality", "http-connect", "socks5-anonymous"} {
		t.Run(name, func(t *testing.T) {
			body := `{"name":"Synthetic node","node":` + string(mustJSON(t, fixtureNode(t, name))) + `}`
			var request NodeCreateRequest
			if err := Decode([]byte(body), "NodeCreateRequest", &request); err != nil {
				t.Fatal(err)
			}
			input, err := request.Input()
			if err != nil {
				t.Fatal(err)
			}
			if !input.Enabled || input.Tags == nil || input.Payload.Validate() != nil {
				t.Fatal("create defaults/typed payload invalid")
			}
			for _, field := range []string{"resource_id", "scope_id", "revision", "security_epoch", "catalog_revision"} {
				var invalid NodeCreateRequest
				expectStatus(t, Decode([]byte(strings.Replace(body, `{"name":`, `{"`+field+`":"1","name":`, 1)), "NodeCreateRequest", &invalid), 400)
			}
		})
	}
}

func TestSecretPatchAllAuthenticationTypes(t *testing.T) {
	for _, test := range []struct{ fixture, kind, field, replacement string }{
		{"trojan-a", "password", "password", "synthetic-new-password"},
		{"shadowsocks-aead", "method_password", "password", "synthetic-new-password"},
		{"vmess-websocket", "vmess_aead", "uuid", "33333333-3333-4333-8333-333333333333"},
		{"vless-reality", "uuid", "uuid", "33333333-3333-4333-8333-333333333333"},
		{"http-connect", "username_password", "username", "synthetic-new-user"},
		{"http-connect", "username_password", "password", "synthetic-new-password"},
	} {
		t.Run(test.kind+"/"+test.field, func(t *testing.T) {
			old := fixtureNode(t, test.fixture)
			original := mustJSON(t, old)
			decode := func(secret string) NodePatchRequest {
				t.Helper()
				var request NodePatchRequest
				body := `{"node":{"auth":{"kind":"` + test.kind + `"` + secret + `}}}`
				if err := Decode([]byte(body), "NodePatchRequest", &request); err != nil {
					t.Fatal(err)
				}
				return request
			}
			kept, err := decode("").Merge(resource(old))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original, mustJSON(t, kept.Payload)) {
				t.Fatal("absent secret changed authentication")
			}
			changed, err := decode(`,"` + test.field + `":"` + test.replacement + `"`).Merge(resource(old))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(mustJSON(t, changed.Payload)), test.replacement) {
				t.Fatal("replacement was not applied")
			}
			_, err = decode(`,"` + test.field + `":null`).Merge(resource(old))
			expectStatus(t, err, 422)
			_, err = decode(`,"` + test.field + `":""`).Merge(resource(old))
			expectStatus(t, err, 422)
			_, err = decode(`,"` + test.field + `":"********"`).Merge(resource(old))
			expectStatus(t, err, 422)
			if !bytes.Equal(original, mustJSON(t, old)) {
				t.Fatal("merge modified original node")
			}
		})
	}
}

func TestAuthKindTransitionAndNestedSecrets(t *testing.T) {
	old := fixtureNode(t, "http-connect")
	var anonymous NodePatchRequest
	if err := Decode([]byte(`{"node":{"auth":{"kind":"none"}}}`), "NodePatchRequest", &anonymous); err != nil {
		t.Fatal(err)
	}
	next, err := anonymous.Merge(resource(old))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := next.Payload.(*ir.Node).Auth.(*ir.NoAuth); !ok {
		t.Fatal("typed auth transition failed")
	}
	var missing NodePatchRequest
	if err := Decode([]byte(`{"node":{"auth":{"kind":"username_password"}}}`), "NodePatchRequest", &missing); err != nil {
		t.Fatal(err)
	}
	_, err = missing.Merge(resource(*next.Payload.(*ir.Node)))
	expectStatus(t, err, 422)
	var mixed NodePatchRequest
	expectStatus(t, Decode([]byte(`{"node":{"auth":{"kind":"password","uuid":"secret"}}}`), "NodePatchRequest", &mixed), 400)
	reality := fixtureNode(t, "vless-reality")
	var clear NodePatchRequest
	if err := Decode([]byte(`{"node":{"security":{"mode":"reality","public_key":null}}}`), "NodePatchRequest", &clear); err != nil {
		t.Fatal(err)
	}
	_, err = clear.Merge(resource(reality))
	expectStatus(t, err, 422)
	if err := Decode([]byte(`{"node":{"security":{"mode":"reality","short_id":null}}}`), "NodePatchRequest", &clear); err != nil {
		t.Fatal(err)
	}
	_, err = clear.Merge(resource(reality))
	expectStatus(t, err, 422)
	if err := Decode([]byte(`{"node":{"security":{"mode":"reality","short_id":""}}}`), "NodePatchRequest", &clear); err != nil {
		t.Fatal(err)
	}
	explicitEmpty, err := clear.Merge(resource(reality))
	if err != nil {
		t.Fatal(err)
	}
	if explicitEmpty.Payload.(*ir.Node).Security.(*ir.RealitySecurity).ShortID != "" {
		t.Fatal("explicit valid empty short ID changed")
	}
	var keep NodePatchRequest
	if err := Decode([]byte(`{"node":{"security":{"mode":"reality","server_name":"changed.invalid"}}}`), "NodePatchRequest", &keep); err != nil {
		t.Fatal(err)
	}
	updated, err := keep.Merge(resource(reality))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Payload.(*ir.Node).Security.(*ir.RealitySecurity).PublicKey != reality.Security.(*ir.RealitySecurity).PublicKey {
		t.Fatal("absent nested secret changed")
	}
}

func TestReadDTOContainsNoSecretsAndPreservesCounters(t *testing.T) {
	for _, name := range []string{"trojan-a", "shadowsocks-aead", "vmess-websocket", "vless-reality", "http-connect", "socks5-anonymous"} {
		t.Run(name, func(t *testing.T) {
			old := resource(fixtureNode(t, name))
			old.Metadata.Revision = math.MaxInt64
			old.Metadata.SecurityEpoch = 9007199254740993
			read, err := NewNodeReadResponse("request-test", old)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateDTO("NodeReadResponse", read); err != nil {
				t.Fatal(err)
			}
			encoded := mustJSON(t, read)
			var value map[string]any
			if err := json.Unmarshal(encoded, &value); err != nil {
				t.Fatal(err)
			}
			var check func(any)
			check = func(value any) {
				switch value := value.(type) {
				case map[string]any:
					for key, child := range value {
						switch key {
						case "password", "uuid", "username", "public_key", "short_id", "payload":
							t.Fatalf("secret/raw field %s in read DTO", key)
						}
						check(child)
					}
				case []any:
					for _, child := range value {
						check(child)
					}
				}
			}
			check(value)
			if !bytes.Contains(encoded, []byte(`"revision":"9223372036854775807"`)) || !bytes.Contains(encoded, []byte(`"security_epoch":"9007199254740993"`)) {
				t.Fatal("counter precision lost")
			}
		})
	}
}

func TestETagAndErrorStatusSeparation(t *testing.T) {
	for _, test := range []struct {
		values []string
		status int
	}{
		{nil, 428}, {[]string{""}, 400}, {[]string{`W/"r2"`}, 400}, {[]string{`"r02"`}, 400}, {[]string{`*`}, 400},
		{[]string{`"r1", "r2"`}, 400}, {[]string{`"r2"`, `"r2"`}, 400}, {[]string{`"r9223372036854775808"`}, 400},
		{[]string{`"r1"`}, 412}, {[]string{`"r2"`}, 0},
	} {
		header := http.Header{}
		for _, value := range test.values {
			header.Add("If-Match", value)
		}
		_, err := RequireRevision(header, 2)
		if test.status == 0 {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			expectStatus(t, err, test.status)
		}
	}
	if value, err := ETag(math.MaxInt64); err != nil || value != `"r9223372036854775807"` {
		t.Fatal("strong ETag counter precision")
	}
	for _, test := range []struct {
		err    error
		status int
	}{{catalog.ErrRevisionConflict, 412}, {catalog.ErrIdempotencyConflict, 409}, {catalog.ErrInvalidInput, 422}, {catalog.ErrCrypto, 503}, {errors.New("secret sql or credential"), 500}} {
		expectStatus(t, test.err, test.status)
		recorder := httptest.NewRecorder()
		WriteError(recorder, "bad\r\ncredential", test.err)
		if recorder.Code != test.status || strings.Contains(recorder.Body.String(), "credential") {
			t.Fatal("unsafe error mapping")
		}
		var response ErrorResponse
		if err := Decode(recorder.Body.Bytes(), "ErrorResponse", &response); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCursorScopeAndFilterBinding(t *testing.T) {
	mac, err := NewCursorHMAC(bytes.Repeat([]byte{37}, 32))
	if err != nil {
		t.Fatal(err)
	}
	codec, err := NewCursorCodec(mac)
	if err != nil {
		t.Fatal(err)
	}
	filters, err := FilterHash(url.Values{"tag": {"b", "a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	same, _ := FilterHash(url.Values{"tag": {"a", "b"}})
	if filters != same {
		t.Fatal("set filter canonicalization differs")
	}
	binding := CursorBinding{ScopeID: scopeID, Collection: "nodes", Sort: CreatedAtIDSort, FilterHash: filters}
	position := CursorPosition{CreatedAt: time.Date(2026, 9, 8, 1, 2, 3, 456789000, time.UTC), ID: nodeID}
	token, err := codec.Encode(binding, position)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(token, binding)
	if err != nil || decoded != position {
		t.Fatal("cursor round trip failed")
	}
	for _, wrong := range []CursorBinding{
		{ScopeID: nodeID, Collection: "nodes", Sort: CreatedAtIDSort, FilterHash: filters},
		{ScopeID: scopeID, Collection: "chains", Sort: CreatedAtIDSort, FilterHash: filters},
		{ScopeID: scopeID, Collection: "nodes", Sort: CreatedAtIDSort, FilterHash: strings.Repeat("0", 64)},
	} {
		_, err := codec.Decode(token, wrong)
		expectStatus(t, err, 400)
	}
	_, err = codec.Decode(strings.Repeat("x", MaxCursorBytes+1), binding)
	expectStatus(t, err, 400)
	_, err = codec.Decode(token[:len(token)-2]+"AA", binding)
	expectStatus(t, err, 400)
	page, err := ParsePagination(url.Values{})
	if err != nil || page.Limit != 50 {
		t.Fatal("default pagination")
	}
	for _, raw := range []string{"0", "201", "-1", "1.0", "01"} {
		_, err := ParsePagination(url.Values{"limit": {raw}})
		expectStatus(t, err, 400)
	}
	_, err = ParsePagination(url.Values{"limit": {"1", "2"}})
	expectStatus(t, err, 400)
	page, err = ParsePagination(url.Values{"limit": {"200"}, "cursor": {token}})
	if err != nil || page.Limit != 200 {
		t.Fatal("maximum pagination")
	}
	for _, value := range []any{mac, codec, ReplaceSecret("SYNTHETIC-NO-LOG"), NodePatchRequest{Node: &NodePatch{Auth: &AuthPatch{Kind: ir.AuthPassword, Password: ReplaceSecret("SYNTHETIC-NO-LOG")}}}} {
		var output bytes.Buffer
		slog.New(slog.NewJSONHandler(&output, nil)).Info("safe", "value", value)
		formatted := fmt.Sprintf("%v %+v %#v", value, value, value)
		if strings.Contains(formatted, "SYNTHETIC-NO-LOG") || strings.Contains(output.String(), "SYNTHETIC-NO-LOG") || strings.Contains(formatted, "37 37") || strings.Contains(formatted, "0x25") || !strings.Contains(output.String(), "REDACTED") {
			t.Fatal("logging exposed secret-bearing aggregate")
		}
	}
}

func TestRequestIDAndIdempotencyHTTPMetadata(t *testing.T) {
	request := httptest.NewRequest("POST", "/test", nil)
	request.Header.Set("X-Request-ID", "client-secret")
	recorder := httptest.NewRecorder()
	RequestIDs(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := RequestID(r.Context())
		if len(id) != 32 || id == "client-secret" {
			t.Error("request ID not server owned")
		}
		WriteError(w, id, NewError(StateConflict))
	})).ServeHTTP(recorder, request)
	if recorder.Code != 409 || strings.Contains(recorder.Body.String(), "client-secret") {
		t.Fatal("request ID boundary")
	}
	header := http.Header{"Idempotency-Key": []string{"synthetic-key"}}
	metadata, err := ReadIdempotency(header, scopeID, nodeID, "nodes.create")
	if err != nil {
		t.Fatal(err)
	}
	first, err := metadata.Request([]byte(`{"node":{"auth":{"kind":"password","password":"new"}}}`), "NodePatchRequest")
	if err != nil {
		t.Fatal(err)
	}
	second, err := metadata.Request([]byte(`{ "node": { "auth": {"password":"new", "kind":"password" } } }`), "NodePatchRequest")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.CanonicalRequest, second.CanonicalRequest) {
		t.Fatal("idempotency canonical request order differs")
	}
	result, err := NewIdempotencyReceipt("request-test", catalog.IdempotencyResult{Replayed: true, Receipt: catalog.Receipt{HTTPStatus: 201, ResourceID: nodeID, Revision: math.MaxInt64, Status: catalog.ReceiptCreated}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateDTO("IdempotencyReceiptResponse", result); err != nil {
		t.Fatal(err)
	}
	header.Add("Idempotency-Key", "other")
	_, err = ReadIdempotency(header, scopeID, nodeID, "nodes.create")
	expectStatus(t, err, 400)
}
