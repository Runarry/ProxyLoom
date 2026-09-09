package catalog_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

const scope ir.ID = "10000000-0000-4000-8000-000000000001"

func TestInputLoggingRedactsAggregates(t *testing.T) {
	const secret = "synthetic-secret-sentinel"
	for _, value := range []any{
		catalog.CreateInput{Name: secret, Payload: node(t, "trojan-a")},
		catalog.UpdateInput{Name: secret, Payload: node(t, "trojan-a")},
		catalog.IdempotencyRequest{RouteKey: secret, Key: secret, CanonicalRequest: []byte(secret)},
	} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if strings.Contains(fmt.Sprintf(format, value), secret) {
				t.Fatal("format leaked input")
			}
		}
		var output bytes.Buffer
		slog.New(slog.NewJSONHandler(&output, nil)).Info("input", "value", value)
		if strings.Contains(output.String(), secret) || !strings.Contains(output.String(), "[REDACTED]") {
			t.Fatal("structured log did not redact input")
		}
	}
}

func node(t *testing.T, fixture string) *ir.Node {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "ir", "positive", fixture+".json"))
	if err != nil {
		t.Fatal(err)
	}
	n, err := ir.DecodeNode(data)
	if err != nil {
		t.Fatal(err)
	}
	return &n
}

func resource(t *testing.T, n *ir.Node) ir.Resource {
	t.Helper()
	r, err := catalog.New(scope, catalog.CreateInput{Name: "synthetic", Tags: []string{"z", "a"}, Enabled: true, Payload: n})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func update(r ir.Resource) catalog.UpdateInput {
	return catalog.UpdateInput{Name: r.Metadata.Name, Tags: r.Metadata.Tags, Enabled: r.Metadata.Enabled, Payload: r.Payload}
}

func TestCreateAndHistoryIsolation(t *testing.T) {
	n := node(t, "trojan-a")
	r := resource(t, n)
	second := resource(t, n)
	if r.Metadata.ResourceID.Validate() != nil || r.Metadata.ResourceID[14] != '4' || r.Metadata.ResourceID == second.Metadata.ResourceID || r.Metadata.Revision != 1 || r.Metadata.SecurityEpoch != 1 {
		t.Fatal("invalid server metadata")
	}
	before, _ := catalog.Canonical(r)
	n.Endpoint.Host = "changed.example"
	if r.Payload.(*ir.Node).Endpoint.Host == n.Endpoint.Host {
		t.Fatal("caller payload alias")
	}
	i := update(r)
	i.Name = "renamed"
	i.Tags = []string{"new"}
	next, err := catalog.Apply(r, i)
	if err != nil {
		t.Fatal(err)
	}
	if next.Metadata.Revision != 2 || next.Metadata.SecurityEpoch != 1 || next.Metadata.ResourceID != r.Metadata.ResourceID || next.Metadata.ScopeID != scope {
		t.Fatal("metadata transition")
	}
	next.Payload.(*ir.Node).Endpoint.Host = "mutated.example"
	next.Metadata.Tags[0] = "mutated"
	after, _ := catalog.Canonical(r)
	if !bytes.Equal(before, after) {
		t.Fatal("historical snapshot mutated")
	}
}

func TestCompleteTypedAuthenticationChanges(t *testing.T) {
	tests := []struct {
		name, fixture string
		mutate        func(*ir.Node)
	}{
		{"password", "trojan-a", func(n *ir.Node) { n.Auth.(*ir.PasswordAuth).Password = "synthetic-rotated" }},
		{"method", "shadowsocks-aead", func(n *ir.Node) { n.Auth.(*ir.MethodPasswordAuth).Method = ir.AES256GCM }},
		{"method-password", "shadowsocks-aead", func(n *ir.Node) { n.Auth.(*ir.MethodPasswordAuth).Password = "synthetic-rotated" }},
		{"vmess-cipher", "vmess-websocket", func(n *ir.Node) { n.Auth.(*ir.VMessAuth).Cipher = ir.VMessNone }},
		{"vmess-uuid", "vmess-websocket", func(n *ir.Node) { n.Auth.(*ir.VMessAuth).UUID = "20000000-0000-4000-8000-000000000002" }},
		{"uuid", "vless-reality", func(n *ir.Node) { n.Auth.(*ir.UUIDAuth).UUID = "20000000-0000-4000-8000-000000000002" }},
		{"username", "http-connect", func(n *ir.Node) { n.Auth.(*ir.UsernamePasswordAuth).Username = "synthetic-new-user" }},
		{"username-password", "http-connect", func(n *ir.Node) { n.Auth.(*ir.UsernamePasswordAuth).Password = "synthetic-rotated" }},
		{"kind", "socks5-anonymous", func(n *ir.Node) {
			n.Auth = &ir.UsernamePasswordAuth{Kind: ir.AuthUsernamePassword, Username: "synthetic", Password: "synthetic"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := resource(t, node(t, tt.fixture))
			n := node(t, tt.fixture)
			tt.mutate(n)
			i := update(r)
			i.Payload = n
			i.Enabled = false
			next, err := catalog.Apply(r, i)
			if err != nil {
				t.Fatal(err)
			}
			if next.Metadata.SecurityEpoch != 2 || next.Metadata.Revision != 2 {
				t.Fatal("auth change and disable must advance once")
			}
		})
	}
}

func TestEpochLifecycleAndOverflow(t *testing.T) {
	r := resource(t, node(t, "socks5-anonymous"))
	i := update(r)
	i.Enabled = false
	disabled, err := catalog.Apply(r, i)
	if err != nil {
		t.Fatal(err)
	}
	i = update(disabled)
	i.Enabled = true
	enabled, err := catalog.Apply(disabled, i)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Metadata.SecurityEpoch != 2 || enabled.Metadata.SecurityEpoch != 2 {
		t.Fatal("reenable restored epoch")
	}
	revoked, err := catalog.Revoke(enabled)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := catalog.Delete(revoked)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.Metadata.SecurityEpoch != 3 || deleted.Metadata.SecurityEpoch != 4 || deleted.Metadata.Enabled || !reflect.DeepEqual(r.Payload, deleted.Payload) {
		t.Fatal("revocation lifecycle")
	}
	for _, field := range []string{"revision", "epoch"} {
		full := r
		if field == "revision" {
			full.Metadata.Revision = math.MaxInt64
		} else {
			full.Metadata.SecurityEpoch = math.MaxInt64
		}
		if _, err := catalog.Revoke(full); err != catalog.ErrInvalidInput {
			t.Fatal("revoke overflow accepted")
		}
		if _, err := catalog.Delete(full); err != catalog.ErrInvalidInput {
			t.Fatal("delete overflow accepted")
		}
		i := update(full)
		i.Enabled = false
		if _, err := catalog.Apply(full, i); err != catalog.ErrInvalidInput {
			t.Fatal("update overflow accepted")
		}
	}
}

func TestRealityCredentialRotationRevokesPriorEpoch(t *testing.T) {
	old := resource(t, node(t, "vless-reality"))
	changed := node(t, "vless-reality")
	security := changed.Security.(*ir.RealitySecurity)
	if security.ShortID == "abcd" {
		security.ShortID = "dcba"
	} else {
		security.ShortID = "abcd"
	}
	input := update(old)
	input.Payload = changed
	next, err := catalog.Apply(old, input)
	if err != nil {
		t.Fatal(err)
	}
	if next.Metadata.SecurityEpoch != old.Metadata.SecurityEpoch+1 || !next.Metadata.Enabled {
		t.Fatal("REALITY credential rotation must invalidate prior epoch without disabling the new revision")
	}
	if old.Payload.(*ir.Node).Security.(*ir.RealitySecurity).ShortID == security.ShortID {
		t.Fatal("credential rotation changed historical input")
	}
}

func TestCanonicalSetsAndOrderedFields(t *testing.T) {
	r := resource(t, node(t, "trojan-a"))
	r.Metadata.Tags = []string{"z", "a"}
	first, err := catalog.Canonical(r)
	if err != nil {
		t.Fatal(err)
	}
	if r.Metadata.Tags[0] != "z" {
		t.Fatal("canonical mutated tags")
	}
	r.Metadata.Tags = []string{"a", "z"}
	second, _ := catalog.Canonical(r)
	if !bytes.Equal(first, second) {
		t.Fatal("set order changed canonical bytes")
	}
	tls := r.Payload.(*ir.Node).Security.(*ir.TLSSecurity)
	tls.ALPN = []string{"h2", "http/1.1"}
	first, _ = catalog.Canonical(r)
	tls.ALPN = []string{"http/1.1", "h2"}
	second, _ = catalog.Canonical(r)
	if bytes.Equal(first, second) {
		t.Fatal("ALPN order discarded")
	}
	r.Metadata.Tags = []string{"a", "a"}
	if _, err := catalog.Canonical(r); err != catalog.ErrInvalidInput {
		t.Fatal("duplicate tags accepted")
	}
}

func TestReferencesAndRejectedInput(t *testing.T) {
	a := resource(t, node(t, "trojan-a"))
	b := resource(t, node(t, "trojan-b"))
	chain := &ir.Chain{SchemaVersion: 1, Hops: []ir.NodeRef{{NodeID: a.Metadata.ResourceID}, {NodeID: b.Metadata.ResourceID}}, FailurePolicy: ir.FailClosed}
	r, err := catalog.New(scope, catalog.CreateInput{Name: "chain", Enabled: true, Payload: chain})
	if err != nil {
		t.Fatal(err)
	}
	refs, err := catalog.ExtractReferences(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].TargetID != a.Metadata.ResourceID || refs[1].TargetID != b.Metadata.ResourceID || refs[0].Path != "/payload/hops/0/node_id" || refs[1].Path != "/payload/hops/1/node_id" || refs[0].TargetRevision != nil || refs[0].ExpectedKind != ir.KindNode || refs[0].SourceRevision != 1 {
		t.Fatal("invalid derived references")
	}
	first, _ := catalog.Canonical(r)
	r.Payload.(*ir.Chain).Hops[0], r.Payload.(*ir.Chain).Hops[1] = r.Payload.(*ir.Chain).Hops[1], r.Payload.(*ir.Chain).Hops[0]
	second, _ := catalog.Canonical(r)
	if bytes.Equal(first, second) {
		t.Fatal("hop order discarded")
	}
	i := update(a)
	i.Payload = chain
	if _, err := catalog.Apply(a, i); err != catalog.ErrInvalidInput {
		t.Fatal("resource kind mutation accepted")
	}
	n := node(t, "trojan-a")
	n.Origin = &ir.Origin{SourceResourceID: scope, SourceItemID: scope, MatchMethod: ir.ManualBinding}
	if _, err := catalog.New(scope, catalog.CreateInput{Name: "origin", Payload: n}); err != nil {
		t.Fatal("confirmed source origin was rejected")
	}
	if _, err := catalog.New("secret-invalid-id", catalog.CreateInput{Name: "invalid", Payload: node(t, "trojan-a")}); err != catalog.ErrInvalidInput {
		t.Fatal("unsafe invalid scope")
	}
	var nilNode *ir.Node
	if _, err := catalog.New(scope, catalog.CreateInput{Name: "invalid", Payload: nilNode}); err != catalog.ErrInvalidInput {
		t.Fatal("typed nil accepted")
	}
}

func TestSafeReceiptValidation(t *testing.T) {
	for _, tc := range []struct {
		status catalog.ReceiptStatus
		code   int
	}{{catalog.ReceiptCreated, 201}, {catalog.ReceiptUpdated, 200}, {catalog.ReceiptDeleted, 204}, {catalog.ReceiptRevoked, 200}, {catalog.ReceiptAccepted, 202}} {
		r := catalog.Receipt{Status: tc.status, HTTPStatus: tc.code, ResourceID: scope, Revision: 1, OperationID: scope}
		if !r.Valid() {
			t.Fatal("valid receipt rejected")
		}
	}
	for _, r := range []catalog.Receipt{
		{Status: "synthetic-secret", HTTPStatus: 200, ResourceID: scope, Revision: 1},
		{Status: catalog.ReceiptCreated, HTTPStatus: 201, ResourceID: "synthetic-secret", Revision: 1},
		{Status: catalog.ReceiptCreated, HTTPStatus: 500, ResourceID: scope, Revision: 1},
		{Status: catalog.ReceiptCreated, HTTPStatus: 201, ResourceID: scope},
		{Status: catalog.ReceiptAccepted, HTTPStatus: 202},
	} {
		if r.Validate() != catalog.ErrInvalidInput {
			t.Fatal("unsafe receipt accepted")
		}
	}
}
