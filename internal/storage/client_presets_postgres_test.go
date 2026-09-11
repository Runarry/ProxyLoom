package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
)

func TestPostgresClientPresetsProvisionOnceReadOnlyAndImmutable(t *testing.T) {
	h := newNodeHTTPAcceptance(t)
	before, err := h.env.store.Scope(h.env.ctx, h.env.scope)
	if err != nil {
		t.Fatal("cannot read preset scope baseline")
	}
	var wg sync.WaitGroup
	results := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- h.env.store.EnsureBuiltinClientPresets(h.env.ctx, h.env.scope) }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("preset initialization failed: %v", err)
		}
	}
	after, err := h.env.store.Scope(h.env.ctx, h.env.scope)
	if err != nil || after.CatalogRevision != before.CatalogRevision+1 {
		t.Fatal("concurrent preset initialization did not advance catalog exactly once")
	}
	if err := h.env.store.EnsureBuiltinClientPresets(h.env.ctx, h.env.scope); err != nil {
		t.Fatal("repeat preset initialization failed")
	}
	again, _ := h.env.store.Scope(h.env.ctx, h.env.scope)
	if again.CatalogRevision != after.CatalogRevision {
		t.Fatal("repeated startup caused preset revision churn")
	}
	response := h.do(http.MethodGet, "/api/v1/client-presets", "", "", nil)
	requireNodeStatus(t, response, http.StatusOK)
	var page apicontract.ClientPresetListResponse
	if json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Data) != 5 || page.Page.NextCursor != "" {
		t.Fatal("five built-in client presets not listed")
	}
	expected, err := catalog.BuiltinClientPresets(h.env.scope)
	if err != nil {
		t.Fatal("cannot construct expected preset IDs")
	}
	want := map[ir.ID]ir.Resource{}
	for _, resource := range expected {
		want[resource.Metadata.ResourceID] = resource
	}
	disabledCount, controlledCount := 0, 0
	for _, item := range page.Data {
		preset := item.Preset
		if item.Metadata.ResourceID != want[item.Metadata.ResourceID].Metadata.ResourceID || item.Metadata.Revision != 1 || item.Metadata.Kind != ir.KindClientPreset || preset.Platform != "linux" || preset.ImportMethod != "file" || preset.DNSMode != "profile" || preset.LocalListener.Protocol != "socks5" || preset.LocalListener.Listen != "127.0.0.1" || preset.LocalListener.Port != 1080 {
			t.Fatal("preset constraints or stable identity changed")
		}
		if preset.ControlAPI.Enabled {
			controlledCount++
			if preset.ControlAPI.Listen != "127.0.0.1" || (preset.CoreFamily != ir.SingBox || preset.ControlAPI.Port != 17812) && (preset.CoreFamily != ir.Mihomo || preset.ControlAPI.Port != 17813) {
				t.Fatal("controlled preset used a non-approved family, address or port")
			}
		} else {
			disabledCount++
			if preset.ControlAPI.Listen != "" || preset.ControlAPI.Port != 0 {
				t.Fatal("original preset acquired an implicit control listener")
			}
		}
		resource, err := h.env.store.Head(h.env.ctx, h.env.scope, item.Metadata.ResourceID)
		if err != nil {
			t.Fatal("persisted preset was unreadable")
		}
		plain, err := catalog.Canonical(resource)
		if err != nil {
			t.Fatal("persisted preset was invalid")
		}
		var envelope []byte
		if h.env.runtime.QueryRow(h.env.ctx, "SELECT envelope FROM public.resource_revisions WHERE resource_id=$1 AND revision=1", dbID(item.Metadata.ResourceID)).Scan(&envelope) != nil || bytes.Contains(envelope, []byte("local_listener")) || bytes.Equal(plain, envelope) {
			t.Fatal("preset revision was stored as plaintext")
		}
		clear(plain)
		for _, mutation := range []func(catalog.Tx) error{
			func(tx catalog.Tx) error {
				_, err := tx.Create(h.env.ctx, catalog.CreateInput{Name: "unreviewed", Enabled: true, Payload: resource.Payload})
				return err
			},
			func(tx catalog.Tx) error {
				_, err := tx.Update(h.env.ctx, item.Metadata.ResourceID, 1, catalog.UpdateInput{Name: "changed", Enabled: true, Payload: resource.Payload})
				return err
			},
			func(tx catalog.Tx) error { _, err := tx.Delete(h.env.ctx, item.Metadata.ResourceID, 1); return err },
			func(tx catalog.Tx) error { _, err := tx.Revoke(h.env.ctx, item.Metadata.ResourceID, 1); return err },
		} {
			if err := h.env.store.Transact(h.env.ctx, h.env.scope, mutation); !errors.Is(err, catalog.ErrInvalidInput) {
				t.Fatal("public catalog mutation changed immutable preset")
			}
		}
		if _, err := h.env.admin.Exec(h.env.ctx, "UPDATE public.resources SET name='changed',head_revision=2 WHERE id=$1", dbID(item.Metadata.ResourceID)); err == nil {
			t.Fatal("database permitted preset metadata mutation")
		}
		// Exercise the complete encrypted append path without the public domain
		// guard. The database must still reject a valid new preset revision.
		if err := h.env.store.transact(h.env.ctx, h.env.scope, func(tx *catalogTx) error {
			_, err := tx.mutate(func() (ir.Resource, error) {
				next := resource
				next.Metadata.Revision++
				next.Metadata.Name = "changed"
				return next, tx.persist(h.env.ctx, next, nil, false)
			})
			return err
		}); !errors.Is(err, catalog.ErrInvalidInput) {
			t.Fatal("database allowed an encrypted preset revision append")
		}
		requireNodeStatus(t, h.do(http.MethodPatch, "/api/v1/client-presets/"+string(item.Metadata.ResourceID), `{"name":"changed"}`, revisionTag(1), nil), http.StatusNotFound)
	}
	if disabledCount != 3 || controlledCount != 2 {
		t.Fatal("original or controlled preset variant missing")
	}
	if bytes.Contains(response.Body.Bytes(), []byte("verified")) {
		t.Fatal("preset list implied kernel or client verification")
	}
	requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/client-presets", `{}`, "", nil), http.StatusNotFound)
	requireNodeStatus(t, h.do(http.MethodGet, "/api/v1/client-presets", "", "", func(r *http.Request) { r.Header.Del("Cookie") }), http.StatusUnauthorized)
	for _, query := range []string{"core_family=unknown", "platform=unknown", "platform=", "core_family="} {
		requireNodeStatus(t, h.do(http.MethodGet, "/api/v1/client-presets?"+query, "", "", nil), http.StatusBadRequest)
	}
	for _, family := range []ir.CoreFamily{ir.Xray, ir.SingBox, ir.Mihomo} {
		filtered := h.do(http.MethodGet, "/api/v1/client-presets?core_family="+string(family)+"&platform=linux", "", "", nil)
		requireNodeStatus(t, filtered, http.StatusOK)
		var matched apicontract.ClientPresetListResponse
		count := 2
		if family == ir.Xray {
			count = 1
		}
		if json.Unmarshal(filtered.Body.Bytes(), &matched) != nil || len(matched.Data) != count || matched.Page.NextCursor != "" {
			t.Fatal("preset family filter lost its result")
		}
		for _, item := range matched.Data {
			if item.Preset.CoreFamily != family {
				t.Fatal("preset family filter returned another family")
			}
		}
	}
	empty := h.do(http.MethodGet, "/api/v1/client-presets?platform=windows", "", "", nil)
	requireNodeStatus(t, empty, http.StatusOK)
	var emptyPage apicontract.ClientPresetListResponse
	if json.Unmarshal(empty.Body.Bytes(), &emptyPage) != nil || len(emptyPage.Data) != 0 {
		t.Fatal("Linux preset listed for unsupported platform")
	}
	seen := map[ir.ID]bool{}
	cursor := ""
	for i := 0; i < 5; i++ {
		path := "/api/v1/client-presets?limit=1"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		paged := h.do(http.MethodGet, path, "", "", nil)
		requireNodeStatus(t, paged, http.StatusOK)
		var result apicontract.ClientPresetListResponse
		if json.Unmarshal(paged.Body.Bytes(), &result) != nil || len(result.Data) != 1 || seen[result.Data[0].Metadata.ResourceID] {
			t.Fatal("preset pagination repeated or dropped item")
		}
		seen[result.Data[0].Metadata.ResourceID] = true
		cursor = result.Page.NextCursor
		if i < 4 && cursor == "" || i == 4 && cursor != "" {
			t.Fatal("preset pagination returned incorrect continuation")
		}
		if i == 0 {
			requireNodeStatus(t, h.do(http.MethodGet, "/api/v1/client-presets?limit=1&platform=linux&cursor="+url.QueryEscape(cursor), "", "", nil), http.StatusBadRequest)
		}
	}
	end, _ := h.env.store.Scope(h.env.ctx, h.env.scope)
	if end.CatalogRevision != after.CatalogRevision {
		t.Fatal("preset reads or failed edits advanced catalog revision")
	}
}

func TestPostgresClientPresetsUpgradePreservesOriginalThree(t *testing.T) {
	e := newPostgres(t, true)
	all, err := catalog.BuiltinClientPresets(e.scope)
	if err != nil {
		t.Fatal("cannot construct preset upgrade fixtures")
	}
	original := map[ir.ID][]byte{}
	// Reproduce the original catalog, which provisioned only disabled
	// control variants, then run simultaneous newer startup initialization.
	if err := e.store.transact(e.ctx, e.scope, func(tx *catalogTx) error {
		for _, resource := range all {
			if resource.Payload.(*ir.ClientPreset).ControlAPI.Enabled {
				continue
			}
			data, err := catalog.Canonical(resource)
			if err != nil {
				return err
			}
			original[resource.Metadata.ResourceID] = data
			_, err = tx.mutate(func() (ir.Resource, error) {
				m := resource.Metadata
				if err := tx.q.InsertResource(e.ctx, dbgen.InsertResourceParams{ID: dbID(m.ResourceID), ScopeID: dbID(e.scope), Kind: string(m.Kind), Name: m.Name, Enabled: m.Enabled}); err != nil {
					return ir.Resource{}, err
				}
				return resource, tx.persist(e.ctx, resource, nil, false)
			})
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal("cannot provision original preset catalog")
	}
	before, _ := e.store.Scope(e.ctx, e.scope)
	var wg sync.WaitGroup
	results := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- e.store.EnsureBuiltinClientPresets(e.ctx, e.scope) }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal("concurrent preset upgrade failed")
		}
	}
	after, err := e.store.Scope(e.ctx, e.scope)
	if err != nil || after.CatalogRevision != before.CatalogRevision+1 {
		t.Fatal("preset upgrade churned catalog revision")
	}
	page, err := e.store.ListClientPresets(e.ctx, e.scope, catalog.ClientPresetListOptions{Limit: 10})
	if err != nil || len(page.Items) != 5 {
		t.Fatal("preset upgrade did not add exactly two variants")
	}
	for id, expected := range original {
		resource, err := e.store.Head(e.ctx, e.scope, id)
		if err != nil {
			t.Fatal("original preset was lost during upgrade")
		}
		actual, err := catalog.Canonical(resource)
		if err != nil || !bytes.Equal(expected, actual) {
			t.Fatal("preset upgrade changed original immutable bytes or identity")
		}
		clear(actual)
		clear(expected)
	}
	var count int
	if e.runtime.QueryRow(e.ctx, "SELECT count(*) FROM public.resource_revisions WHERE scope_id=$1", dbID(e.scope)).Scan(&count) != nil || count != 5 {
		t.Fatal("preset upgrade appended duplicate revisions")
	}
}
