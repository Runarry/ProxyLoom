package ir_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/schemas"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "ir", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func parseValue(t *testing.T, data []byte) any {
	t.Helper()
	var value any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func mustNode(t *testing.T, name string) ir.Node {
	t.Helper()
	node, err := ir.DecodeNode(fixture(t, "positive/"+name))
	if err != nil {
		t.Fatal(err)
	}
	return node
}
func mustSpec(t *testing.T) ir.FrozenInputSpec {
	t.Helper()
	var spec ir.FrozenInputSpec
	if err := json.Unmarshal(fixture(t, "positive/frozen-a-b.json"), &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}
func boolPtr(value bool) *bool       { return &value }
func stringPtr(value string) *string { return &value }
func hasDiagnostic(t *testing.T, err error, code ir.DiagnosticCode, path string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s at %s, accepted input", code, path)
	}
	var diagnostics ir.Diagnostics
	if !errors.As(err, &diagnostics) {
		t.Fatalf("non-contract error type %T", err)
	}
	for _, d := range diagnostics {
		if d.Code == code && (path == "*" || d.FieldPath == path) {
			return
		}
	}
	t.Fatalf("missing diagnostic %s at %s; got %v", code, path, err)
}

func TestSchemaFixturesMatchTypedGo(t *testing.T) {
	// Compile the actual standalone schema independently from ir's embedded
	// validator. Format assertion is part of the documented v1 consumer contract.
	onDisk, err := os.ReadFile(filepath.Join("..", "..", "schemas", "ir-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(onDisk, schemas.V1()) {
		t.Fatal("embedded and standalone schemas differ")
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	if err := compiler.AddResource(schemas.V1ID, parseValue(t, onDisk)); err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Cases []struct {
			Name        string `json:"name"`
			Definition  string `json:"definition"`
			SchemaValid bool   `json:"schema_valid"`
			GoValid     bool   `json:"go_valid"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(fixture(t, "manifest.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	for _, test := range manifest.Cases {
		t.Run(test.Name, func(t *testing.T) {
			data := fixture(t, test.Name)
			schema, err := compiler.Compile(schemas.V1ID + "#/$defs/" + test.Definition)
			if err != nil {
				t.Fatal(err)
			}
			if accepted := schema.Validate(parseValue(t, data)) == nil; accepted != test.SchemaValid {
				t.Fatalf("schema accepted=%v, want %v", accepted, test.SchemaValid)
			}
			var value interface{ Validate() error }
			switch test.Definition {
			case "subscription_profile":
				var next ir.SubscriptionProfile
				err = json.Unmarshal(data, &next)
				value = next
			case "node":
				next, decodeErr := ir.DecodeNode(data)
				value, err = next, decodeErr
			case "chain":
				next, decodeErr := ir.DecodeChain(data)
				value, err = next, decodeErr
			case "target_ref":
				next, decodeErr := ir.DecodeTargetRef(data)
				value, err = next, decodeErr
			case "resource":
				next, decodeErr := ir.DecodeResource(data)
				value, err = next, decodeErr
			case "frozen_input":
				next, decodeErr := ir.DecodeFrozenInput(data)
				value, err = next, decodeErr
			default:
				t.Fatal("fixture definition has no Go decoder")
			}
			if (err == nil) != test.GoValid {
				t.Fatalf("Go accepted=%v, want %v; diagnostic=%v", err == nil, test.GoValid, err)
			}
			if err == nil {
				if err := value.Validate(); err != nil {
					t.Fatalf("decoded value fails Validate: %v", err)
				}
				if err := schema.Validate(parseValue(t, mustJSON(t, value))); err != nil {
					t.Fatal("typed round trip no longer matches schema")
				}
			}
		})
	}
}

func TestStrictJSONDiagnostics(t *testing.T) {
	base := string(fixture(t, "positive/trojan-a.json"))
	tests := []struct {
		name, data string
		code       ir.DiagnosticCode
		path       string
	}{
		{"unknown nested", strings.Replace(base, `"password": "EXAMPLE_ONLY_A"`, `"password": "EXAMPLE_ONLY_A", "unexpected/key~": true`, 1), ir.UnknownField, "/auth"},
		{"duplicate secret", strings.Replace(base, `"password": "EXAMPLE_ONLY_A"`, `"password": "EXAMPLE_ONLY_A", "password": "NEVER_PRINT_DUPLICATE"`, 1), ir.DuplicateField, "/auth/password"},
		{"escaped duplicate", strings.Replace(base, `"password": "EXAMPLE_ONLY_A"`, `"password": "EXAMPLE_ONLY_A", "\u0070assword": "NEVER_PRINT_DUPLICATE"`, 1), ir.DuplicateField, "/auth/password"},
		{"case sensitive", strings.Replace(base, `"schema_version"`, `"Schema_Version"`, 1), ir.UnknownField, ""},
		{"future version", strings.Replace(base, `"schema_version": 1`, `"schema_version": 2`, 1), ir.UnsupportedVersion, "/schema_version"},
		{"null optional", strings.Replace(base, `"udp": false`, `"udp": null`, 1), ir.InvalidType, "/features/udp"},
		{"null root", "null", ir.InvalidType, ""},
		{"trailing document", base + " {}", ir.InvalidJSON, ""},
		{"unpaired surrogate", strings.Replace(base, "EXAMPLE_ONLY_A", `\ud800`, 1), ir.InvalidJSON, ""},
		{"escaped quote before unpaired surrogate", strings.Replace(base, "EXAMPLE_ONLY_A", `x\"\ud800`, 1), ir.InvalidJSON, ""},
		{"invalid utf8", strings.Replace(base, "EXAMPLE_ONLY_A", string([]byte{0xff}), 1), ir.InvalidJSON, ""},
		{"endpoint URI", strings.Replace(base, `"host": "a.example.invalid"`, `"host": "https://a.example.invalid:443/path"`, 1), ir.InvalidValue, "/endpoint/host"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ir.DecodeNode([]byte(test.data))
			hasDiagnostic(t, err, test.code, test.path)
			if strings.Contains(err.Error(), "EXAMPLE_ONLY_A") || strings.Contains(err.Error(), "NEVER_PRINT_DUPLICATE") {
				t.Fatal("diagnostic error leaked a credential")
			}
			var diagnostics ir.Diagnostics
			errors.As(err, &diagnostics)
			if strings.Contains(string(mustJSON(t, diagnostics)), "EXAMPLE_ONLY_A") || strings.Contains(string(mustJSON(t, diagnostics)), "NEVER_PRINT_DUPLICATE") {
				t.Fatal("diagnostic JSON leaked a credential")
			}
		})
	}
}

func TestSchemaIntegerSyntaxAndPrecision(t *testing.T) {
	base := string(fixture(t, "positive/resource-trojan-a.json"))
	for _, literal := range []string{"1.0", "1e0", "9007199254740993", "9223372036854775807"} {
		t.Run(literal, func(t *testing.T) {
			data := []byte(strings.Replace(base, `"revision": 1`, `"revision": `+literal, 1))
			if err := schemas.Validate("resource", parseValue(t, data)); err != nil {
				t.Fatal("schema rejected integer boundary")
			}
			resource, err := ir.DecodeResource(data)
			if err != nil {
				t.Fatal(err)
			}
			want := literal
			if literal == "1.0" || literal == "1e0" {
				want = "1"
			}
			if fmt.Sprint(resource.Metadata.Revision) != want {
				t.Fatal("revision precision changed")
			}
		})
	}
	for _, literal := range []string{"1.5", "9223372036854775808"} {
		data := []byte(strings.Replace(base, `"revision": 1`, `"revision": `+literal, 1))
		if schemas.Validate("resource", parseValue(t, data)) == nil {
			t.Fatal("schema accepted invalid revision")
		}
		if _, err := ir.DecodeResource(data); err == nil {
			t.Fatal("Go accepted invalid revision")
		}
	}
}

func TestEscapedQuoteAndPairedSurrogatePreserveCredential(t *testing.T) {
	base := string(fixture(t, "positive/trojan-a.json"))
	for _, test := range []struct{ encoded, want string }{
		{`x\"\ud83d\ude00`, "x\"😀"},
		{`x\\ud800`, `x\ud800`},
	} {
		node, err := ir.DecodeNode([]byte(strings.Replace(base, "EXAMPLE_ONLY_A", test.encoded, 1)))
		if err != nil {
			t.Fatal(err)
		}
		if string(node.Auth.(*ir.PasswordAuth).Password) != test.want {
			t.Fatal("credential changed while decoding escaped Unicode")
		}
	}
}

func TestDiagnosticsNeverEchoUnknownKeysOrInvalidResourceIDs(t *testing.T) {
	base := string(fixture(t, "positive/trojan-a.json"))
	secretKey := "trojan://NEVER_ECHO_KEY@example.invalid"
	for _, fragment := range []string{
		`"password":"EXAMPLE_ONLY_A","` + secretKey + `":true`,
		`"password":"EXAMPLE_ONLY_A","` + secretKey + `":true,"` + secretKey + `":false`,
		`"password":"EXAMPLE_ONLY_A","` + secretKey + `":{"password":"one","password":"two"}`,
	} {
		_, err := ir.DecodeNode([]byte(strings.Replace(base, `"password": "EXAMPLE_ONLY_A"`, fragment, 1)))
		if err == nil {
			t.Fatal("invalid object accepted")
		}
		if strings.Contains(err.Error(), "NEVER_ECHO_KEY") {
			t.Fatal("error leaked an unknown key")
		}
		var diagnostics ir.Diagnostics
		errors.As(err, &diagnostics)
		if bytes.Contains(mustJSON(t, diagnostics), []byte("NEVER_ECHO_KEY")) {
			t.Fatal("diagnostic JSON leaked an unknown key")
		}
	}
	spec := mustSpec(t)
	spec.Resources[0].Metadata.ResourceID = "NEVER_ECHO_INVALID_ID"
	spec.Resources[0].Payload.(*ir.Node).Auth.(*ir.PasswordAuth).Password = ""
	err := spec.Validate()
	if err == nil {
		t.Fatal("invalid resource accepted")
	}
	var diagnostics ir.Diagnostics
	errors.As(err, &diagnostics)
	if bytes.Contains(mustJSON(t, diagnostics), []byte("NEVER_ECHO_INVALID_ID")) {
		t.Fatal("diagnostic echoed an invalid resource identity")
	}
}

func TestTypedVariantsAndOptionalBooleans(t *testing.T) {
	a := mustNode(t, "trojan-a.json")
	b := mustNode(t, "trojan-b.json")
	if a.Features.UDP == nil || *a.Features.UDP || b.Features.UDP != nil {
		t.Fatal("false and absence were collapsed")
	}
	for _, node := range []ir.Node{a, b} {
		decoded, err := ir.DecodeNode(mustJSON(t, node))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(node.Features, decoded.Features) {
			t.Fatal("feature presence changed on round trip")
		}
	}
	vmess := mustNode(t, "vmess-websocket.json")
	ws := vmess.Transport.(*ir.WebSocketTransport)
	tls := vmess.Security.(*ir.TLSSecurity)
	if vmess.Endpoint.Host == *ws.Host || *ws.Host == tls.ServerName {
		t.Fatal("independent endpoint/Host/SNI fixture was collapsed")
	}
	if _, ok := vmess.Auth.(*ir.VMessAuth); !ok {
		t.Fatal("VMess lost its typed auth variant")
	}
	if _, ok := mustNode(t, "vless-reality.json").Security.(*ir.RealitySecurity); !ok {
		t.Fatal("REALITY lost its typed security variant")
	}
}

type forgedAuth struct {
	*ir.PasswordAuth
	Extra string `json:"extra"`
}
type forgedPayload struct{ *ir.Node }

func TestDirectConstructionCannotBypassValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ir.Node)
	}{
		{"bad protocol", func(n *ir.Node) { n.Protocol = "invented" }},
		{"auth mismatch", func(n *ir.Node) {
			n.Auth = &ir.UUIDAuth{Kind: ir.AuthUUID, UUID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd"}
		}},
		{"nil auth", func(n *ir.Node) { n.Auth = (*ir.PasswordAuth)(nil) }},
		{"forged auth", func(n *ir.Node) {
			n.Auth = &forgedAuth{PasswordAuth: n.Auth.(*ir.PasswordAuth), Extra: "not an IR variant"}
		}},
		{"nil transport", func(n *ir.Node) { n.Transport = (*ir.NativeTCPTransport)(nil) }},
		{"missing certificate decision", func(n *ir.Node) { n.Security.(*ir.TLSSecurity).VerifyCertificate = nil }},
		{"bad port", func(n *ir.Node) { n.Endpoint.Port = 65536 }},
		{"invalid schema", func(n *ir.Node) { n.SchemaVersion = 0 }},
		{"wrong auth discriminator", func(n *ir.Node) { n.Auth.(*ir.PasswordAuth).Kind = ir.AuthUUID }},
		{"invalid utf8 secret", func(n *ir.Node) { n.Auth.(*ir.PasswordAuth).Password = ir.Secret(string([]byte{0xff})) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			node := mustNode(t, "trojan-a.json")
			test.mutate(&node)
			if node.Validate() == nil {
				t.Fatal("constructed invalid node accepted")
			}
		})
	}
	spec := mustSpec(t)
	node := spec.Resources[0].Payload.(*ir.Node)
	spec.Resources[0].Payload = &forgedPayload{Node: node}
	if spec.Resources[0].Validate() == nil {
		t.Fatal("forged payload accepted")
	}
	if _, err := ir.NewFrozenInput(spec); err == nil {
		t.Fatal("forged payload frozen")
	}
	var zero ir.FrozenInput
	if zero.Validate() == nil {
		t.Fatal("zero frozen handle accepted")
	}
	if _, err := json.Marshal(zero); err == nil {
		t.Fatal("zero frozen handle serialized")
	}
}

func TestLeafJSONBoundariesRejectUnknownFields(t *testing.T) {
	tests := []struct {
		name, data  string
		destination any
	}{
		{"auth", `{"kind":"password","password":"EXAMPLE_ONLY","uuid":"EXAMPLE_ONLY"}`, &ir.PasswordAuth{}},
		{"transport", `{"kind":"native_tcp","path":"/hidden"}`, &ir.NativeTCPTransport{}},
		{"security", `{"mode":"none","verify_certificate":false}`, &ir.NoSecurity{}},
		{"features", `{"udp":false,"detour":"hidden"}`, &ir.Features{}},
		{"extensions", `{"mihomo":{}}`, &ir.Extensions{}},
		{"endpoint", `{"host":"a.example.invalid","port":443,"sni":"hidden"}`, &ir.Endpoint{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(test.data), test.destination); err == nil {
				t.Fatal("leaf decoder dropped an unknown field")
			}
		})
	}
}

func TestTargetRefContextAndMixedBranches(t *testing.T) {
	resource, err := ir.DecodeTargetRef(fixture(t, "positive/resource-ref.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := resource.ValidateFor(false, ir.KindNode, ir.KindChain); err != nil {
		t.Fatal(err)
	}
	if resource.ValidateFor(false, ir.KindChain) == nil {
		t.Fatal("wrong kind accepted in containing field")
	}
	builtin := ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Direct}
	if builtin.ValidateFor(false, ir.KindNode) == nil {
		t.Fatal("builtin accepted as proxy member")
	}
	if err := builtin.ValidateFor(true, ir.KindNode); err != nil {
		t.Fatal(err)
	}
	builtin.ResourceID = resource.ResourceID
	if builtin.Validate() == nil {
		t.Fatal("mixed target reference accepted")
	}
}

func TestCredentialAggregatesRedactDefaultFormattingAndSlog(t *testing.T) {
	spec := mustSpec(t)
	frozen, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	node := spec.Resources[0].Payload.(*ir.Node)
	values := []any{node, *node, node.Auth, node.Endpoint, spec.Resources[0], spec, frozen, ir.Secret("EXAMPLE_ONLY_A"), mustNode(t, "vless-reality.json").Security}
	for i, value := range values {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
				formatted := fmt.Sprintf(format, value)
				if strings.Contains(formatted, "EXAMPLE_ONLY") || strings.Contains(formatted, "a.example.invalid") || strings.Contains(formatted, strings.Repeat("A", 43)) {
					t.Fatal("fmt leaked credential material")
				}
			}
			var output bytes.Buffer
			slog.New(slog.NewJSONHandler(&output, nil)).Info("test", "value", value)
			if strings.Contains(output.String(), "EXAMPLE_ONLY") || strings.Contains(output.String(), "a.example.invalid") || strings.Contains(output.String(), strings.Repeat("A", 43)) {
				t.Fatal("structured log leaked credential material")
			}
		})
	}
	if !bytes.Contains(mustJSON(t, node), []byte("EXAMPLE_ONLY_A")) {
		t.Fatal("compile IR JSON must retain the actual credential")
	}
}
