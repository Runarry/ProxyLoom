// Package secretbox encrypts sensitive records with independent random data keys.
// It has no database, network, configuration-file, or logging side effects.
package secretbox

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"math"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

const (
	KeySize           = 32
	MaxKeys           = 16
	MaxKeyIDBytes     = 64
	MaxPlaintextBytes = 16 << 20
	MaxDigestBytes    = MaxPlaintextBytes
	PayloadVersion    = 1
	WrappingVersion   = 1

	PurposeResourceContent  = "resource_content"
	PurposeIdempotency      = "idempotency"
	PurposeCursor           = "cursor"
	PurposeNodeIdentity     = "node_identity"
	PurposeImportConnection = "import_connection"
	PurposeImportCommit     = "import_commit"

	TableResourceRevisions     = "resource_revisions"
	TableSourceSnapshots       = "source_snapshots"
	TableSourceItems           = "source_items"
	TableNodeBindings          = "node_bindings"
	TableImportBatches         = "import_batches"
	TableImportItems           = "import_items"
	TableImportCandidates      = "import_candidates"
	TableSystemSettings        = "system_settings"
	TableTestTargets           = "test_targets"
	TableCompatibilityEvidence = "compatibility_evidence"
	TableCompileBatches        = "compile_batches"
	TableCompileOutputs        = "compile_outputs"
	TableJobs                  = "jobs"

	nonceSize = 12
	tagSize   = 16
)

// Error values intentionally omit key identifiers, contexts, data, and the
// underlying authentication or parsing failure. Open and Rewrap share one error.
var (
	ErrKeyring = errors.New("secretbox: invalid_keyring")
	ErrSeal    = errors.New("secretbox: encryption_failed")
	ErrOpen    = errors.New("secretbox: authentication_failed")
	ErrDigest  = errors.New("secretbox: digest_failed")
)

// Context identifies one immutable record. Callers derive it from trusted
// storage metadata, never from metadata supplied alongside an encrypted value.
type Context struct {
	ScopeID       ir.ID
	Table         string
	ObjectID      ir.ID
	Revision      int64
	SchemaVersion int
}

// Payload is immutable after storage. JSON is its explicit persistence format;
// ordinary fmt and slog formatting is redacted. Each operation owns its slices.
type Payload struct {
	Version    int    `json:"version"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

// Wrapping is stored separately from Payload and can be replaced during master
// key rotation. Its Version is a format version, not a concurrency/CAS counter.
type Wrapping struct {
	Version    int    `json:"version"`
	KeyID      string `json:"key_id"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

// Box retains private copies of key bytes and is safe for concurrent use.
// Construct a new Box for a new active key; existing instances are immutable.
type Box struct {
	activeKeyID string
	keys        map[string][KeySize]byte
	contentKey  [KeySize]byte
	random      io.Reader
}

// New requires an explicit active key and an independent content HMAC key.
// Removing an old key is an explicit caller decision; rewrapping never retires it.
func New(activeKeyID string, keys map[string][]byte, contentHMACKey []byte) (*Box, error) {
	if !validKeyID(activeKeyID) || len(keys) == 0 || len(keys) > MaxKeys || len(contentHMACKey) != KeySize {
		return nil, ErrKeyring
	}
	if _, ok := keys[activeKeyID]; !ok {
		return nil, ErrKeyring
	}
	for id, key := range keys {
		if !validKeyID(id) || len(key) != KeySize || bytes.Equal(key, contentHMACKey) {
			return nil, ErrKeyring
		}
		for otherID, other := range keys {
			if id != otherID && bytes.Equal(key, other) {
				return nil, ErrKeyring
			}
		}
	}
	b := &Box{activeKeyID: activeKeyID, keys: make(map[string][KeySize]byte, len(keys)), random: rand.Reader}
	copy(b.contentKey[:], contentHMACKey)
	for id, key := range keys {
		b.keys[id] = [KeySize]byte(key)
	}
	return b, nil
}

// Seal generates a fresh DEK and independent payload/wrapping nonces. Neither
// random encryption nor subsequent rewrapping is an input to a content Digest.
func (b *Box) Seal(ctx Context, plaintext []byte) (Payload, Wrapping, error) {
	if !b.ready() || !validContext(ctx) || len(plaintext) > MaxPlaintextBytes {
		return Payload{}, Wrapping{}, ErrSeal
	}
	var dek [KeySize]byte
	defer clear(dek[:])
	if _, err := io.ReadFull(b.random, dek[:]); err != nil {
		return Payload{}, Wrapping{}, ErrSeal
	}
	gcm, err := newGCM(dek[:])
	if err != nil {
		return Payload{}, Wrapping{}, ErrSeal
	}
	p := Payload{Version: PayloadVersion, Nonce: make([]byte, nonceSize)}
	if _, err := io.ReadFull(b.random, p.Nonce); err != nil {
		return Payload{}, Wrapping{}, ErrSeal
	}
	p.Ciphertext = gcm.Seal(nil, p.Nonce, plaintext, contextAAD(ctx, "payload", PayloadVersion))
	w, err := b.wrap(ctx, p, dek[:])
	if err != nil {
		return Payload{}, Wrapping{}, ErrSeal
	}
	return p, w, nil
}

// Open returns new caller-owned plaintext only after both layers authenticate.
// Invalid versions, contexts, missing/wrong keys, and corruption all fail alike.
func (b *Box) Open(ctx Context, p Payload, w Wrapping) ([]byte, error) {
	dek, err := b.unwrap(ctx, p, w)
	if err != nil {
		return nil, ErrOpen
	}
	defer clear(dek)
	return openPayload(ctx, p, dek)
}

// Rewrap authenticates the complete record, then encrypts the existing DEK with
// the active master key and a new nonce. It never mutates its arguments or writes
// storage; callers must atomically compare and replace the old wrapping record.
func (b *Box) Rewrap(ctx Context, p Payload, w Wrapping) (Wrapping, error) {
	dek, err := b.unwrap(ctx, p, w)
	if err != nil {
		return Wrapping{}, ErrOpen
	}
	defer clear(dek)
	plaintext, err := openPayload(ctx, p, dek)
	if err != nil {
		return Wrapping{}, ErrOpen
	}
	clear(plaintext)
	next, err := b.wrap(ctx, p, dek)
	if err != nil {
		return Wrapping{}, ErrOpen
	}
	return next, nil
}

// Digest authenticates caller-canonicalized content in a fixed purpose domain.
// It does not normalize JSON, sort collections, or expose a plain secret hash.
func (b *Box) Digest(purpose string, data []byte) ([]byte, error) {
	if !b.ready() || !validPurpose(purpose) || len(data) > MaxDigestBytes {
		return nil, ErrDigest
	}
	mac := hmac.New(sha256.New, b.contentKey[:])
	_, _ = mac.Write(appendField([]byte("proxyloom.secretbox.hmac\x00v1\x00"), purpose))
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(data)))
	_, _ = mac.Write(size[:])
	_, _ = mac.Write(data)
	return mac.Sum(nil), nil
}

func (b *Box) ready() bool {
	if b == nil || b.random == nil {
		return false
	}
	_, ok := b.keys[b.activeKeyID]
	return ok
}

func (b *Box) wrap(ctx Context, p Payload, dek []byte) (Wrapping, error) {
	key := b.keys[b.activeKeyID]
	defer clear(key[:])
	gcm, err := newGCM(key[:])
	if err != nil {
		return Wrapping{}, err
	}
	w := Wrapping{Version: WrappingVersion, KeyID: b.activeKeyID, Nonce: make([]byte, nonceSize)}
	if _, err := io.ReadFull(b.random, w.Nonce); err != nil {
		return Wrapping{}, err
	}
	w.Ciphertext = gcm.Seal(nil, w.Nonce, dek, wrappingAAD(ctx, p, w))
	return w, nil
}

func (b *Box) unwrap(ctx Context, p Payload, w Wrapping) ([]byte, error) {
	if !b.ready() || !validContext(ctx) || p.Version != PayloadVersion || w.Version != WrappingVersion ||
		len(p.Nonce) != nonceSize || len(p.Ciphertext) < tagSize || len(p.Ciphertext) > MaxPlaintextBytes+tagSize ||
		!validKeyID(w.KeyID) || len(w.Nonce) != nonceSize || len(w.Ciphertext) != KeySize+tagSize {
		return nil, ErrOpen
	}
	key, ok := b.keys[w.KeyID]
	if !ok {
		return nil, ErrOpen
	}
	defer clear(key[:])
	gcm, err := newGCM(key[:])
	if err != nil {
		return nil, ErrOpen
	}
	dek, err := gcm.Open(nil, w.Nonce, w.Ciphertext, wrappingAAD(ctx, p, w))
	if err != nil {
		clear(dek)
		return nil, ErrOpen
	}
	return dek, nil
}

func openPayload(ctx Context, p Payload, dek []byte) ([]byte, error) {
	gcm, err := newGCM(dek)
	if err != nil {
		return nil, ErrOpen
	}
	plaintext, err := gcm.Open(nil, p.Nonce, p.Ciphertext, contextAAD(ctx, "payload", PayloadVersion))
	if err != nil {
		clear(plaintext)
		return nil, ErrOpen
	}
	return plaintext, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func validKeyID(id string) bool {
	if len(id) == 0 || len(id) > MaxKeyIDBytes {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validContext(ctx Context) bool {
	if len(ctx.ScopeID) != 36 || len(ctx.ObjectID) != 36 || ctx.Revision <= 0 || ctx.SchemaVersion <= 0 || ctx.SchemaVersion > math.MaxInt32 {
		return false
	}
	switch ctx.Table {
	case TableResourceRevisions, TableSourceSnapshots, TableSourceItems, TableNodeBindings,
		TableImportBatches, TableImportItems, TableImportCandidates, TableSystemSettings, TableTestTargets,
		TableCompatibilityEvidence, TableCompileBatches, TableCompileOutputs, TableJobs:
	default:
		return false
	}
	return ctx.ScopeID.Validate() == nil && ctx.ObjectID.Validate() == nil
}

func validPurpose(purpose string) bool {
	switch purpose {
	case PurposeResourceContent, PurposeIdempotency, PurposeCursor, PurposeNodeIdentity, PurposeImportConnection, PurposeImportCommit:
		return true
	default:
		return false
	}
}

// All variable-width fields are length prefixed and all integers are fixed-width
// big endian. This encoding is part of format v1 and must not change in place.
func contextAAD(ctx Context, purpose string, version int) []byte {
	aad := appendField([]byte("proxyloom.secretbox.aad\x00"), purpose)
	aad = binary.BigEndian.AppendUint32(aad, uint32(version))
	aad = appendField(aad, string(ctx.ScopeID))
	aad = appendField(aad, ctx.Table)
	aad = appendField(aad, string(ctx.ObjectID))
	aad = binary.BigEndian.AppendUint64(aad, uint64(ctx.Revision))
	return binary.BigEndian.AppendUint32(aad, uint32(ctx.SchemaVersion))
}

func wrappingAAD(ctx Context, p Payload, w Wrapping) []byte {
	aad := appendField(contextAAD(ctx, "dek", w.Version), w.KeyID)
	// Bind the wrapper to the exact immutable payload as well as its context.
	// This unkeyed hash is internal AAD and is never a public content digest.
	hash := sha256.New()
	_, _ = hash.Write(binary.BigEndian.AppendUint32(nil, uint32(p.Version)))
	_, _ = hash.Write(p.Nonce) // nonceSize is fixed and checked before opening.
	_, _ = hash.Write(p.Ciphertext)
	return hash.Sum(aad)
}

func appendField(dst []byte, field string) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(field)))
	return append(dst, field...)
}
