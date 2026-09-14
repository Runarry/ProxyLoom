package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
)

func TestCatalogContentDigestBindsExactPersistedBytes(t *testing.T) {
	box, err := secretbox.New("fixture", map[string][]byte{"fixture": bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	resource, err := catalog.New(jobs.NewID(), catalog.CreateInput{Name: "integrity", Tags: []string{}, Enabled: true, Payload: syntheticNode()})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := catalog.Canonical(resource)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plain)
	m := resource.Metadata
	row := dbgen.GetResourceRevisionRow{ScopeID: dbID(m.ScopeID), ResourceID: dbID(m.ResourceID), Revision: m.Revision, SchemaVersion: int32(m.SchemaVersion), SecurityEpoch: m.SecurityEpoch}
	row.ContentHmac, err = box.Digest(secretbox.PurposeResourceContent, plain)
	if err != nil {
		t.Fatal(err)
	}
	seal := func(content []byte) {
		payload, wrapping, e := box.Seal(recordContext(row), content)
		if e != nil {
			t.Fatal(e)
		}
		row.Envelope, e = json.Marshal(payload)
		if e != nil {
			t.Fatal(e)
		}
		row.Wrapping, e = json.Marshal(wrapping)
		if e != nil {
			t.Fatal(e)
		}
	}
	store := &Catalog{box: box}
	seal(plain)
	if restored, e := store.open(row); e != nil || restored.Metadata.ResourceID != m.ResourceID {
		t.Fatal("canonical stored revision rejected", e)
	}
	// Valid equivalent JSON with a different ciphertext must not be accepted
	// against the prior content digest by normalizing away the byte change.
	changed := append(bytes.Clone(plain), '\n')
	defer clear(changed)
	if _, e := ir.DecodeResource(changed); e != nil {
		t.Fatal(e)
	}
	seal(changed)
	if _, e := store.open(row); !errors.Is(e, catalog.ErrCrypto) {
		t.Fatal("content digest did not bind persisted bytes", e)
	}
}
