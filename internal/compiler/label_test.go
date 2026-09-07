package compiler

import (
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestLabelsIgnoreDisplayNames(t *testing.T) {
	itemA := nodeLabelItem("11111111-1111-4111-8111-111111111111", 1)
	itemB := nodeLabelItem("22222222-2222-4222-8222-222222222222", 1)
	labels, err := defaultLabeler().assign([]labelItem{itemA, itemB})
	if err != nil {
		t.Fatal(err)
	}
	if labels[itemA.key][0] == labels[itemB.key][0] {
		t.Fatal("distinct nodes received the same tag")
	}
	if !strings.HasPrefix(labels[itemA.key][0], "n_") || len(labels[itemA.key][0]) != 10 {
		t.Fatalf("independent tag %s", labels[itemA.key][0])
	}
	sameNameOtherID := nodeLabelItem("11111111-1111-4111-8111-111111111111", 2)
	other, err := defaultLabeler().assign([]labelItem{sameNameOtherID})
	if err != nil {
		t.Fatal(err)
	}
	if other[sameNameOtherID.key][0] == labels[itemA.key][0] {
		t.Fatal("revision change must change the tag")
	}
}

func TestLabelCollisionExtendsOnlyCollidingItems(t *testing.T) {
	l := labeler{digest: func(material string) string {
		switch material {
		case "node|aaaa|1":
			return "aaaaaaaa" + strings.Repeat("1", 56)
		case "node|bbbb|1":
			return "aaaaaaaa" + strings.Repeat("2", 56)
		case "node|cccc|1":
			return "cccccccc" + strings.Repeat("3", 56)
		default:
			t.Fatalf("unexpected material %s", material)
			return ""
		}
	}}
	labels, err := l.assign([]labelItem{
		nodeLabelItem("aaaa", 1),
		nodeLabelItem("bbbb", 1),
		nodeLabelItem("cccc", 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if labels[nodeMaterial("cccc", 1)][0] != "n_cccccccc" {
		t.Fatalf("non-colliding tag was extended: %s", labels[nodeMaterial("cccc", 1)][0])
	}
	if labels[nodeMaterial("aaaa", 1)][0] != "n_aaaaaaaa11" || labels[nodeMaterial("bbbb", 1)][0] != "n_aaaaaaaa22" {
		t.Fatalf("colliding tags not extended deterministically: %v", labels)
	}
}

func TestIdenticalFullHashesFailClosed(t *testing.T) {
	l := labeler{digest: func(string) string { return strings.Repeat("ab", 32) }}
	_, err := l.assign([]labelItem{nodeLabelItem("aaaa", 1), nodeLabelItem("bbbb", 1)})
	if err == nil {
		t.Fatal("full hash collision accepted")
	}
	var diags ir.Diagnostics
	if !asDiagnosticsOK(err, &diags) || diags[0].Code != ir.CompileLabelCollision {
		t.Fatalf("got %v", err)
	}
}

func asDiagnosticsOK(err error, diags *ir.Diagnostics) bool {
	d := asDiagnostics(err)
	if len(d) == 0 {
		return false
	}
	*diags = d
	return true
}
