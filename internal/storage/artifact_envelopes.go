package storage

import (
	"container/list"
	"crypto/sha256"
	"sync"

	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
)

// Only decoded ciphertext is retained. Every request still reads the database,
// authorizes the current publication and authenticates/decrypts the payload.
// Exact envelope hashes also fence corruption and key rewrapping immediately.
const artifactEnvelopeCacheBytes = 32 << 20
const artifactEnvelopeCacheEntries = 32

type envelopeKey struct {
	scope, artifact   ir.ID
	payload, wrapping [32]byte
}

type decodedEnvelope struct {
	key      envelopeKey
	payload  secretbox.Payload
	wrapping secretbox.Wrapping
	bytes    int
}

type artifactEnvelopeCache struct {
	mu    sync.Mutex
	items map[envelopeKey]*list.Element
	order list.List
	bytes int
}

func makeEnvelopeKey(scope, artifact ir.ID, payload, wrapping []byte) envelopeKey {
	return envelopeKey{scope, artifact, sha256.Sum256(payload), sha256.Sum256(wrapping)}
}

func (c *artifactEnvelopeCache) get(key envelopeKey) (secretbox.Payload, secretbox.Wrapping, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry := c.items[key]; entry != nil {
		c.order.MoveToFront(entry)
		value := entry.Value.(decodedEnvelope)
		return value.payload, value.wrapping, true
	}
	return secretbox.Payload{}, secretbox.Wrapping{}, false
}

func (c *artifactEnvelopeCache) put(key envelopeKey, payload secretbox.Payload, wrapping secretbox.Wrapping) {
	size := len(payload.Ciphertext) + len(payload.Nonce) + len(wrapping.Ciphertext) + len(wrapping.Nonce) + len(wrapping.KeyID)
	if size > artifactEnvelopeCacheBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = make(map[envelopeKey]*list.Element)
	}
	if entry := c.items[key]; entry != nil {
		c.order.MoveToFront(entry)
		return
	}
	for c.bytes+size > artifactEnvelopeCacheBytes || len(c.items) >= artifactEnvelopeCacheEntries {
		old := c.order.Back()
		value := old.Value.(decodedEnvelope)
		delete(c.items, value.key)
		c.order.Remove(old)
		c.bytes -= value.bytes
	}
	c.items[key] = c.order.PushFront(decodedEnvelope{key, payload, wrapping, size})
	c.bytes += size
}
