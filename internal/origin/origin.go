// Package origin matches imported candidates to existing nodes. It does not
// fetch URLs, persist bindings, or treat a connection fingerprint as identity.
package origin

import (
	"encoding/json"
	"regexp"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

type Method string
type Kind string

const (
	StableExternalKey Method = "stable_external_key"
	ManualBinding     Method = "manual_binding"
	ExactFingerprint  Method = "exact_fingerprint"
	Suggestion        Method = "suggestion"

	Identity  Kind = "identity"
	Duplicate Kind = "duplicate"
	Suggest   Kind = "suggestion"
)

var externalKey = regexp.MustCompile(`^[A-Za-z0-9._:~-]{1,128}$`)

// Item is one parsed candidate. Fingerprint is an opaque, purpose-separated
// digest supplied by the caller. Empty identity fields are skipped.
type Item struct {
	SourceItemID ir.ID
	ExternalKey  string
	Fingerprint  string
	Name         string
	Protocol     ir.Protocol
	Host         string
	Port         int
}

// Record is an existing node projection. BoundSourceItemID is only set for a
// confirmed manual binding. ExternalKey is source-scoped when present.
type Record struct {
	NodeID            ir.ID
	Revision          int64
	BoundSourceItemID ir.ID
	ExternalKey       string
	Fingerprint       string
	Name              string
	Protocol          ir.Protocol
	Host              string
	Port              int
}

// Decision is empty when Match returns false. Suggestion never authorizes an
// automatic update; callers must not copy NodeID into a confirmed binding.
type Decision struct {
	Kind      Kind
	Method    Method
	NodeID    ir.ID
	Revision  int64
	Ambiguous bool
}

func ParseExternalKey(value string) (string, bool) {
	if !externalKey.MatchString(value) {
		return "", false
	}
	return value, true
}

// ExternalKeyFromMetadata reads only an explicit external_key JSON string.
// Names, UUIDs, passwords and other URI fields are ignored.
func ExternalKeyFromMetadata(metadata map[string]json.RawMessage) string {
	raw, ok := metadata["external_key"]
	if !ok {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	key, ok := ParseExternalKey(value)
	if !ok {
		return ""
	}
	return key
}

func Match(item Item, records []Record) (Decision, bool) {
	if item.ExternalKey != "" {
		if decision, ok := collect(records, func(record Record) bool {
			return record.ExternalKey == item.ExternalKey
		}, Identity, StableExternalKey); ok {
			return decision, true
		}
	}
	if item.SourceItemID != "" {
		if decision, ok := collect(records, func(record Record) bool {
			return record.BoundSourceItemID == item.SourceItemID
		}, Identity, ManualBinding); ok {
			return decision, true
		}
	}
	if item.Fingerprint != "" {
		if decision, ok := collect(records, func(record Record) bool {
			return record.Fingerprint == item.Fingerprint
		}, Duplicate, ExactFingerprint); ok {
			return decision, true
		}
	}
	return collect(records, func(record Record) bool {
		if item.Fingerprint != "" && record.Fingerprint == item.Fingerprint {
			return false
		}
		sameEndpoint := item.Protocol != "" && record.Protocol == item.Protocol && record.Host == item.Host && record.Port == item.Port && item.Host != "" && item.Port > 0
		sameName := item.Name != "" && record.Name == item.Name && record.Protocol == item.Protocol
		return sameEndpoint || sameName
	}, Suggest, Suggestion)
}

func collect(records []Record, keep func(Record) bool, kind Kind, method Method) (Decision, bool) {
	var hits []Record
	for _, record := range records {
		if keep(record) {
			hits = append(hits, record)
		}
	}
	if len(hits) == 0 {
		return Decision{}, false
	}
	decision := Decision{Kind: kind, Method: method, NodeID: hits[0].NodeID, Revision: hits[0].Revision}
	if len(hits) > 1 {
		ids := map[ir.ID]bool{}
		for _, hit := range hits {
			ids[hit.NodeID] = true
		}
		if len(ids) > 1 {
			decision.Ambiguous = true
			decision.NodeID = ""
			decision.Revision = 0
		}
	}
	return decision, true
}
