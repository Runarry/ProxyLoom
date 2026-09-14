package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
)

func TestArtifactEnvelopeCacheStillAuthenticatesEveryRead(t *testing.T) {
	box, err := secretbox.New("fixture", map[string][]byte{"fixture": bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := &Subscriptions{catalog: &Catalog{box: box}}
	scope, id := jobs.NewID(), jobs.NewID()
	plain := []byte("synthetic cached artifact")
	p, w, err := s.seal(scope, id, secretbox.TableCompileOutputs, plain)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 20 {
		group.Go(func() {
			got, e := s.open(scope, id, secretbox.TableCompileOutputs, p, w)
			if e != nil || !bytes.Equal(got, plain) {
				t.Error("cached read failed", e)
			}
			clear(got) // Callers own plaintext; clearing it cannot corrupt a later read.
		})
	}
	group.Wait()
	if len(s.artifactEnvelopes.items) != 1 {
		t.Fatal("identical envelopes were not reused")
	}
	var payload secretbox.Payload
	if err := json.Unmarshal(p, &payload); err != nil {
		t.Fatal(err)
	}
	payload.Ciphertext[0] ^= 1
	changed, _ := json.Marshal(payload)
	for _, tc := range []struct {
		name              string
		payload, wrapping []byte
	}{
		{"changed_ciphertext", changed, w},
		{"malformed_payload", []byte("{}"), w},
		{"malformed_wrapping", p, []byte("{}")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := s.open(scope, id, secretbox.TableCompileOutputs, tc.payload, tc.wrapping); !errors.Is(err, catalog.ErrCrypto) || got != nil {
				t.Fatal("cached envelope bypassed authentication", err)
			}
		})
	}
	if got, err := s.open(jobs.NewID(), id, secretbox.TableCompileOutputs, p, w); !errors.Is(err, catalog.ErrCrypto) || got != nil {
		t.Fatal("cached envelope crossed scopes", err)
	}
	if got, err := s.open(scope, jobs.NewID(), secretbox.TableCompileOutputs, p, w); !errors.Is(err, catalog.ErrCrypto) || got != nil {
		t.Fatal("cached envelope crossed artifacts", err)
	}
}

func TestArtifactEnvelopeCacheBoundsRetainedCiphertext(t *testing.T) {
	var cache artifactEnvelopeCache
	for i := range artifactEnvelopeCacheEntries + 10 {
		key := envelopeKey{artifact: jobs.NewID()}
		cache.put(key, secretbox.Payload{Ciphertext: make([]byte, (2<<20)+i)}, secretbox.Wrapping{})
	}
	if cache.bytes > artifactEnvelopeCacheBytes || len(cache.items) >= artifactEnvelopeCacheEntries {
		t.Fatal("ciphertext byte budget exceeded")
	}
	var small artifactEnvelopeCache
	for range artifactEnvelopeCacheEntries + 10 {
		small.put(envelopeKey{artifact: jobs.NewID()}, secretbox.Payload{}, secretbox.Wrapping{})
	}
	if len(small.items) != artifactEnvelopeCacheEntries {
		t.Fatal("entry budget exceeded")
	}
}
