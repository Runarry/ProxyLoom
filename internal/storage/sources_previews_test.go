package storage

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/importparse"
	"github.com/Runarry/ProxyLoom/internal/imports"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestSourcePreviewSecretDiffAndTypedPaths(t *testing.T) {
	fixture := url.URL{Scheme: "trojan", User: url.User("SYNTHETIC_OLD"), Host: "diff.example.invalid:443", Fragment: "Name"}
	parsed := importparse.ParseURI(fixture.String())
	if !parsed.Valid() {
		t.Fatal("invalid synthetic fixture")
	}
	before := *parsed.Node
	after := before
	after.Auth = &ir.PasswordAuth{Kind: ir.AuthPassword, Password: "SYNTHETIC_NEW"}
	after.Endpoint.Host = "changed.example.invalid"
	changes := sourceFieldChanges("Name", &before, "New name", &after)
	if len(changes) != 3 {
		t.Fatalf("expected name, endpoint and secret changes, got %d", len(changes))
	}
	for _, change := range changes {
		if change.FieldPath == "/node/auth/password" && (!change.SecretChanged || len(change.Before) != 0 || len(change.After) != 0) {
			t.Fatal("secret change contains a value or lacks its marker")
		}
	}
	encoded, err := json.Marshal(changes)
	if err != nil || strings.Contains(string(encoded), "SYNTHETIC_") {
		t.Fatal("source diff disclosed a synthetic secret")
	}
	after.Origin = &ir.Origin{SourceResourceID: "10000000-0000-4000-8000-000000000001", SourceItemID: "10000000-0000-4000-8000-000000000002", MatchMethod: ir.ManualBinding}
	if len(sourceFieldChanges("Name", &after, "Name", &after)) != 0 {
		t.Fatal("unchanged input produced a diff")
	}
	plain, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	read, err := (importCandidate{State: "new", Name: "Name", Node: plain, SourceItemID: after.Origin.SourceItemID,
		ChangeKind: "modified", UpstreamChanges: changes}).read(after.Origin.SourceResourceID, after.Origin.SourceItemID, 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(read)
	if err != nil || strings.Contains(string(encoded), "SYNTHETIC_") {
		t.Fatal("preview read disclosed a synthetic secret")
	}
}

func TestSourcePreviewBoundIncludesMissingWithoutTruncation(t *testing.T) {
	existing := make([]storedSourceItem, imports.MaxCandidates)
	parsed := make([]parsedCandidate, imports.MaxCandidates)
	for i := range existing {
		existing[i] = storedSourceItem{ID: ir.ID(fmt.Sprint(i)), Fingerprint: fmt.Sprintf("old-%d", i)}
		parsed[i] = parsedCandidate{Fingerprint: existing[i].Fingerprint}
	}
	if got := sourcePreviewCount(existing, parsed); got != imports.MaxCandidates {
		t.Fatalf("unchanged limit: %d", got)
	}
	for i := range parsed {
		parsed[i].Fingerprint = fmt.Sprintf("new-%d", i)
	}
	if got := sourcePreviewCount(existing, parsed); got != 2*imports.MaxCandidates {
		t.Fatalf("missing entries were truncated: %d", got)
	}
	if !strings.Contains(sourceRefreshError(sourcePreviewLimit).Message, "5000-candidate") {
		t.Fatal("limit failure lacks its fixed explanation")
	}
}
