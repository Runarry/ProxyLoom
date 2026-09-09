package origin

import (
	"encoding/json"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestMatchOrderSeparatesIdentityFromDuplicatesAndSuggestions(t *testing.T) {
	item := Item{ExternalKey: "prov-1", SourceItemID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Fingerprint: "fp-new",
		Name: "Home", Protocol: ir.Trojan, Host: "edge.example.invalid", Port: 443}
	records := []Record{
		{NodeID: "11111111-1111-4111-8111-111111111111", Revision: 3, ExternalKey: "prov-1", Fingerprint: "fp-old",
			Name: "Renamed", Protocol: ir.Trojan, Host: "edge.example.invalid", Port: 443},
		{NodeID: "22222222-2222-4222-8222-222222222222", Revision: 1, Fingerprint: "fp-new",
			Name: "Home", Protocol: ir.Trojan, Host: "other.example.invalid", Port: 443},
	}
	decision, ok := Match(item, records)
	if !ok || decision.Kind != Identity || decision.Method != StableExternalKey || decision.NodeID != records[0].NodeID || decision.Ambiguous {
		t.Fatalf("external key lost to fingerprint: %#v", decision)
	}
	item.ExternalKey = ""
	records[0].BoundSourceItemID = item.SourceItemID
	decision, ok = Match(item, records)
	if !ok || decision.Kind != Identity || decision.Method != ManualBinding || decision.NodeID != records[0].NodeID {
		t.Fatalf("manual binding lost: %#v", decision)
	}
	item.SourceItemID = ""
	decision, ok = Match(item, records)
	if !ok || decision.Kind != Duplicate || decision.Method != ExactFingerprint || decision.NodeID != records[1].NodeID {
		t.Fatalf("fingerprint was treated as identity: %#v", decision)
	}
}

func TestRenameDoesNotBreakFingerprintDuplicate(t *testing.T) {
	item := Item{Fingerprint: "fp-same", Name: "New label", Protocol: ir.Trojan, Host: "edge.example.invalid", Port: 443}
	records := []Record{{NodeID: "11111111-1111-4111-8111-111111111111", Revision: 4, Fingerprint: "fp-same",
		Name: "Old label", Protocol: ir.Trojan, Host: "edge.example.invalid", Port: 443}}
	decision, ok := Match(item, records)
	if !ok || decision.Kind != Duplicate || decision.NodeID != records[0].NodeID {
		t.Fatal("rename was treated as a new identity")
	}
}

func TestSameNameDifferentFingerprintIsSuggestionNotMerge(t *testing.T) {
	item := Item{Fingerprint: "fp-a", Name: "Home", Protocol: ir.Trojan, Host: "a.example.invalid", Port: 443}
	records := []Record{{NodeID: "11111111-1111-4111-8111-111111111111", Revision: 1, Fingerprint: "fp-b",
		Name: "Home", Protocol: ir.Trojan, Host: "b.example.invalid", Port: 8443}}
	decision, ok := Match(item, records)
	if !ok || decision.Kind != Suggest || decision.Method != Suggestion || decision.Ambiguous {
		t.Fatalf("same-name nodes were merged: %#v ok=%v", decision, ok)
	}
}

func TestAuthChangeWithoutKeyIsSuggestion(t *testing.T) {
	item := Item{Fingerprint: "fp-rotated", Name: "Edge", Protocol: ir.Trojan, Host: "edge.example.invalid", Port: 443}
	records := []Record{{NodeID: "11111111-1111-4111-8111-111111111111", Revision: 2, Fingerprint: "fp-old",
		Name: "Edge", Protocol: ir.Trojan, Host: "edge.example.invalid", Port: 443}}
	decision, ok := Match(item, records)
	if !ok || decision.Kind != Suggest || decision.Method != Suggestion {
		t.Fatalf("credential rotation silently matched: %#v", decision)
	}
}

func TestAmbiguousFingerprintIsConflict(t *testing.T) {
	item := Item{Fingerprint: "fp-shared"}
	records := []Record{
		{NodeID: "11111111-1111-4111-8111-111111111111", Revision: 1, Fingerprint: "fp-shared"},
		{NodeID: "22222222-2222-4222-8222-222222222222", Revision: 1, Fingerprint: "fp-shared"},
	}
	decision, ok := Match(item, records)
	if !ok || !decision.Ambiguous || decision.Kind != Duplicate || decision.NodeID != "" {
		t.Fatalf("duplicate catalog rows were auto-merged: %#v", decision)
	}
}

func TestExternalKeyDoesNotUseNameOrUUID(t *testing.T) {
	if ExternalKeyFromMetadata(map[string]json.RawMessage{"name": json.RawMessage(`"Home"`), "id": json.RawMessage(`"11111111-1111-4111-8111-111111111111"`)}) != "" {
		t.Fatal("name or UUID became an identity key")
	}
	if ExternalKeyFromMetadata(map[string]json.RawMessage{"external_key": json.RawMessage(`"ok_node-1"`)}) != "ok_node-1" {
		t.Fatal("documented external_key rejected")
	}
	if ExternalKeyFromMetadata(map[string]json.RawMessage{"external_key": json.RawMessage(`"has space"`)}) != "" {
		t.Fatal("invalid external_key accepted")
	}
	if _, ok := ParseExternalKey(""); ok {
		t.Fatal("empty key accepted")
	}
}

func TestNoMatch(t *testing.T) {
	if _, ok := Match(Item{Fingerprint: "fp-a", Name: "A", Protocol: ir.HTTP, Host: "a.example.invalid", Port: 80},
		[]Record{{NodeID: "11111111-1111-4111-8111-111111111111", Revision: 1, Fingerprint: "fp-b", Name: "B", Protocol: ir.Trojan, Host: "b.example.invalid", Port: 443}}); ok {
		t.Fatal("unrelated node matched")
	}
}
