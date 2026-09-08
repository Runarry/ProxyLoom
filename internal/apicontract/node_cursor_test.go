package apicontract

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/catalog"
)

func TestNodeHistoryCursorsBindResourceAndTuple(t *testing.T) {
	mac, err := NewCursorHMAC(bytes.Repeat([]byte{0x23}, 32))
	if err != nil {
		t.Fatal("cursor key construction failed")
	}
	codec, err := NewCursorCodec(mac)
	if err != nil {
		t.Fatal("cursor construction failed")
	}
	binding := CursorBinding{ScopeID: scopeID, Collection: "nodes/revisions", Sort: NodeRevisionSort, FilterHash: strings.Repeat("1", 64)}
	token, err := codec.EncodeNodeRevision(binding, math.MaxInt64)
	if err != nil {
		t.Fatal("revision cursor encode failed")
	}
	if value, err := codec.DecodeNodeRevision(token, binding); err != nil || value != math.MaxInt64 {
		t.Fatal("revision precision was lost")
	}
	other := binding
	other.FilterHash = strings.Repeat("2", 64)
	expectStatus(t, func() error { _, err := codec.DecodeNodeRevision(token, other); return err }(), 400)
	other = binding
	other.ScopeID = nodeID
	expectStatus(t, func() error { _, err := codec.DecodeNodeRevision(token, other); return err }(), 400)
	expectStatus(t, func() error { _, err := codec.DecodeNodeRevision("X"+token[1:], binding); return err }(), 400)
	refs := CursorBinding{ScopeID: scopeID, Collection: "nodes/references", Sort: NodeReferenceSort, FilterHash: strings.Repeat("3", 64)}
	position := catalog.ReferencePosition{SourceID: nodeID, SourceRevision: math.MaxInt64, Path: "/payload/hops/1/node_id"}
	token, err = codec.EncodeNodeReference(refs, position)
	if err != nil {
		t.Fatal("reference cursor encode failed")
	}
	if decoded, err := codec.DecodeNodeReference(token, refs); err != nil || decoded != position {
		t.Fatal("reference cursor changed its ordering tuple")
	}
	if _, err := codec.Decode(token, refs); err == nil {
		t.Fatal("reference cursor crossed into resource-list decoding")
	}
}
