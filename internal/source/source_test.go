package source

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestDisplayURLOmitsPathQueryAndUserinfo(t *testing.T) {
	raw := url.URL{Scheme: "https", Host: "feed.example.invalid:8443", Path: "/sub", RawQuery: "token=1", Fragment: "frag"}
	raw.User = url.UserPassword("user", "pass")
	display, err := DisplayURL(raw.String())
	if err != nil || display != "https://feed.example.invalid:8443" {
		t.Fatalf("display URL %q %v", display, err)
	}
	if _, err := DisplayURL("https://feed.example.invalid/path"); err != nil {
		t.Fatal(err)
	}
	if _, err := DisplayURL("ftp://feed.example.invalid"); err == nil {
		t.Fatal("non-http URL was accepted")
	}
}

func TestCanonicalRejectsInvalidDocumentsAndRedacts(t *testing.T) {
	cfg := Config{SchemaVersion: 1, URL: ir.Secret("https://feed.example.invalid/sub"), Format: FormatAuto,
		Auth: sourceAuthNone(), RefreshPolicy: RefreshPolicy{IntervalSeconds: 60, CommitMode: Manual, MissingPolicy: Retain},
		FetchLimits: FetchLimits{TimeoutMS: 5000, MaxCompressedBytes: 1024, MaxDecodedBytes: 2048, MaxRedirects: 2}, BindingRevision: 1}
	doc := Document{Metadata: ir.Metadata{ResourceID: "11111111-1111-4111-8111-111111111111", ScopeID: "10000000-0000-4000-8000-000000000001",
		Kind: ir.KindSource, Revision: 1, SchemaVersion: 1, Name: "feed", Tags: []string{"b", "a"}, Enabled: true, SecurityEpoch: 1}, Source: cfg}
	plain, err := Canonical(doc)
	if err != nil || !strings.Contains(string(plain), `"a"`) {
		t.Fatalf("canonical source failed: %v %s", err, plain)
	}
	if text := fmt.Sprintf("%v %#v", doc, cfg); strings.Contains(text, "feed.example.invalid") || text != "[REDACTED] [REDACTED]" {
		t.Fatalf("source document leaked: %s", text)
	}
	doc.Source.URL = "not-a-url"
	if _, err := Canonical(doc); err == nil {
		t.Fatal("invalid source URL was stored")
	}
}

func sourceAuthNone() Auth { return Auth{Kind: AuthNone} }
