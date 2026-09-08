package secretbox

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

func testContext() Context {
	return Context{
		ScopeID: "11111111-1111-4111-8111-111111111111", Table: TableResourceRevisions,
		ObjectID: "22222222-2222-4222-8222-222222222222", Revision: 7, SchemaVersion: 1,
	}
}

func testKey(seed byte) []byte { return bytes.Repeat([]byte{seed}, KeySize) }

func testBox(t *testing.T) *Box {
	t.Helper()
	b, err := New("master-a", map[string][]byte{"master-a": testKey(1)}, testKey(3))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sealFixture(t *testing.T, b *Box) (Payload, Wrapping) {
	t.Helper()
	p, w, err := b.Seal(testContext(), []byte("synthetic-credential-never-log"))
	if err != nil {
		t.Fatal(err)
	}
	return p, w
}

func clonePayload(p Payload) Payload {
	p.Nonce, p.Ciphertext = bytes.Clone(p.Nonce), bytes.Clone(p.Ciphertext)
	return p
}

func cloneWrapping(w Wrapping) Wrapping {
	w.Nonce, w.Ciphertext = bytes.Clone(w.Nonce), bytes.Clone(w.Ciphertext)
	return w
}

func assertRejected(t *testing.T, b *Box, ctx Context, p Payload, w Wrapping) {
	t.Helper()
	beforeP, beforeW := clonePayload(p), cloneWrapping(w)
	got, err := b.Open(ctx, p, w)
	if err != ErrOpen || got != nil {
		t.Fatal("Open must return the fixed authentication error and no plaintext")
	}
	next, err := b.Rewrap(ctx, p, w)
	if err != ErrOpen || !reflect.DeepEqual(next, Wrapping{}) {
		t.Fatal("Rewrap must return the same fixed error and no replacement")
	}
	if !reflect.DeepEqual(p, beforeP) || !reflect.DeepEqual(w, beforeW) {
		t.Fatal("failed authentication mutated caller-owned ciphertext")
	}
}

func TestRoundTripRandomnessAndStableContentDigest(t *testing.T) {
	b := testBox(t)
	plaintext := []byte("synthetic-credential-never-log")
	original := bytes.Clone(plaintext)
	p1, w1 := sealFixture(t, b)
	p2, w2 := sealFixture(t, b)
	if bytes.Equal(p1.Nonce, p2.Nonce) || bytes.Equal(p1.Ciphertext, p2.Ciphertext) ||
		bytes.Equal(w1.Nonce, w2.Nonce) || bytes.Equal(w1.Ciphertext, w2.Ciphertext) || bytes.Equal(p1.Nonce, w1.Nonce) {
		t.Fatal("each record and encryption layer needs independent randomness")
	}
	dek1, err := b.unwrap(testContext(), p1, w1)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(dek1)
	dek2, err := b.unwrap(testContext(), p2, w2)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(dek2)
	if bytes.Equal(dek1, dek2) || bytes.Equal(dek1, testKey(1)) {
		t.Fatal("every record needs a fresh random data key")
	}
	for _, pair := range []struct {
		p Payload
		w Wrapping
	}{{p1, w1}, {p2, w2}} {
		got, err := b.Open(testContext(), pair.p, pair.w)
		if err != nil || !bytes.Equal(got, plaintext) {
			t.Fatal("authenticated round trip changed the plaintext")
		}
		clear(got)
	}
	d1, err := b.Digest(PurposeResourceContent, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := b.Digest(PurposeResourceContent, original)
	if err != nil || len(d1) != 32 || !bytes.Equal(d1, d2) {
		t.Fatal("random encryption must not affect the content HMAC")
	}
	if !bytes.Equal(plaintext, original) {
		t.Fatal("Seal or Digest changed caller-owned plaintext")
	}
	assertRejected(t, b, testContext(), p1, w2)
}

func TestEveryContextFieldIsAuthenticated(t *testing.T) {
	b := testBox(t)
	p, w := sealFixture(t, b)
	for _, tc := range []struct {
		name   string
		change func(*Context)
	}{
		{"scope", func(c *Context) { c.ScopeID = "33333333-3333-4333-8333-333333333333" }},
		{"table", func(c *Context) { c.Table = TableSourceSnapshots }},
		{"object", func(c *Context) { c.ObjectID = "33333333-3333-4333-8333-333333333333" }},
		{"revision", func(c *Context) { c.Revision++ }},
		{"schema", func(c *Context) { c.SchemaVersion++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := testContext()
			tc.change(&ctx)
			assertRejected(t, b, ctx, p, w)
		})
	}
}

func TestInvalidContextsFailClosed(t *testing.T) {
	b := testBox(t)
	p, w := sealFixture(t, b)
	for _, tc := range []struct {
		name   string
		change func(*Context)
	}{
		{"empty_scope", func(c *Context) { c.ScopeID = "" }},
		{"bad_scope", func(c *Context) { c.ScopeID = "secret-invalid-scope" }},
		{"noncanonical_scope", func(c *Context) { c.ScopeID = "ABCDEFAB-1234-4234-8234-123456789abc" }},
		{"empty_object", func(c *Context) { c.ObjectID = "" }},
		{"nil_uuid", func(c *Context) { c.ObjectID = "00000000-0000-0000-0000-000000000000" }},
		{"unknown_table", func(c *Context) { c.Table = "secret-arbitrary-table" }},
		{"unencrypted_table", func(c *Context) { c.Table = "users" }},
		{"zero_revision", func(c *Context) { c.Revision = 0 }},
		{"negative_revision", func(c *Context) { c.Revision = -1 }},
		{"zero_schema", func(c *Context) { c.SchemaVersion = 0 }},
		{"negative_schema", func(c *Context) { c.SchemaVersion = -1 }},
		{"large_schema", func(c *Context) { c.SchemaVersion = math.MaxInt32 + 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := testContext()
			tc.change(&ctx)
			nextP, nextW, err := b.Seal(ctx, []byte("synthetic"))
			if err != ErrSeal || !reflect.DeepEqual(nextP, Payload{}) || !reflect.DeepEqual(nextW, Wrapping{}) {
				t.Fatal("invalid context was accepted for encryption")
			}
			assertRejected(t, b, ctx, p, w)
		})
	}
}

func TestCorruptionTruncationAndVersions(t *testing.T) {
	b := testBox(t)
	p, w := sealFixture(t, b)
	for n := range len(p.Ciphertext) {
		changed := clonePayload(p)
		changed.Ciphertext[n] ^= 1
		assertRejected(t, b, testContext(), changed, w)
		changed.Ciphertext = bytes.Clone(p.Ciphertext[:n])
		assertRejected(t, b, testContext(), changed, w)
	}
	for n := range len(w.Ciphertext) {
		changed := cloneWrapping(w)
		changed.Ciphertext[n] ^= 1
		assertRejected(t, b, testContext(), p, changed)
		changed.Ciphertext = bytes.Clone(w.Ciphertext[:n])
		assertRejected(t, b, testContext(), p, changed)
	}
	for n := range nonceSize {
		changedP, changedW := clonePayload(p), cloneWrapping(w)
		changedP.Nonce[n] ^= 1
		changedW.Nonce[n] ^= 1
		assertRejected(t, b, testContext(), changedP, w)
		assertRejected(t, b, testContext(), p, changedW)
	}
	for _, size := range []int{0, nonceSize - 1, nonceSize + 1, 64} {
		changedP, changedW := clonePayload(p), cloneWrapping(w)
		changedP.Nonce, changedW.Nonce = make([]byte, size), make([]byte, size)
		assertRejected(t, b, testContext(), changedP, w)
		assertRejected(t, b, testContext(), p, changedW)
	}
	for _, version := range []int{-1, 0, 2, math.MaxInt} {
		changedP, changedW := clonePayload(p), cloneWrapping(w)
		changedP.Version, changedW.Version = version, version
		assertRejected(t, b, testContext(), changedP, w)
		assertRejected(t, b, testContext(), p, changedW)
	}
	for _, id := range []string{"", "missing", "secret-invalid/key-id", strings.Repeat("x", MaxKeyIDBytes+1)} {
		changed := cloneWrapping(w)
		changed.KeyID = id
		assertRejected(t, b, testContext(), p, changed)
	}
	changedP, changedW := clonePayload(p), cloneWrapping(w)
	changedP.Ciphertext = append(changedP.Ciphertext, 0)
	changedW.Ciphertext = append(changedW.Ciphertext, 0)
	assertRejected(t, b, testContext(), changedP, w)
	assertRejected(t, b, testContext(), p, changedW)
}

func TestPayloadMustAuthenticateEvenWithAValidWrapping(t *testing.T) {
	b := testBox(t)
	p, w := sealFixture(t, b)
	dek, err := b.unwrap(testContext(), p, w)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(dek)
	p.Ciphertext[0] ^= 1
	w, err = b.wrap(testContext(), p, dek)
	if err != nil {
		t.Fatal(err)
	}
	assertRejected(t, b, testContext(), p, w)
}

func TestKeyRotationRewrapPreservesPayloadAndDigest(t *testing.T) {
	old := testBox(t)
	rotated, err := New("master-b", map[string][]byte{"master-a": testKey(1), "master-b": testKey(2)}, testKey(3))
	if err != nil {
		t.Fatal(err)
	}
	p, w := sealFixture(t, old)
	beforeP, beforeW := clonePayload(p), cloneWrapping(w)
	digestBefore, err := old.Digest(PurposeResourceContent, []byte("synthetic-credential-never-log"))
	if err != nil {
		t.Fatal(err)
	}
	next, err := rotated.Rewrap(testContext(), p, w)
	if err != nil || next.KeyID != "master-b" || next.Version != WrappingVersion || bytes.Equal(next.Nonce, w.Nonce) {
		t.Fatal("rewrapping did not use the active key and fresh nonce")
	}
	if !reflect.DeepEqual(p, beforeP) || !reflect.DeepEqual(w, beforeW) {
		t.Fatal("rewrapping mutated the immutable payload or old wrapping")
	}
	plaintext, err := rotated.Open(testContext(), p, next)
	if err != nil || !bytes.Equal(plaintext, []byte("synthetic-credential-never-log")) {
		t.Fatal("new wrapping could not decrypt the unchanged payload")
	}
	digestAfter, err := rotated.Digest(PurposeResourceContent, plaintext)
	if err != nil || !bytes.Equal(digestBefore, digestAfter) {
		t.Fatal("master key rotation changed the content HMAC")
	}
	if _, err := rotated.Open(testContext(), p, w); err != nil {
		t.Fatal("rewrapping implicitly retired an old key")
	}
	assertRejected(t, old, testContext(), p, next)
	retired, err := New("master-b", map[string][]byte{"master-b": testKey(2)}, testKey(3))
	if err != nil {
		t.Fatal(err)
	}
	assertRejected(t, retired, testContext(), p, w)
	if _, err := retired.Open(testContext(), p, next); err != nil {
		t.Fatal("completed rewrap still depended on the retired master key")
	}
	nextAgain, err := rotated.Rewrap(testContext(), p, next)
	if err != nil || bytes.Equal(next.Nonce, nextAgain.Nonce) || bytes.Equal(next.Ciphertext, nextAgain.Ciphertext) {
		t.Fatal("rewrapping with the same active key must still use a fresh nonce")
	}
}

func TestWrongKeyAndAuthenticatedKeyID(t *testing.T) {
	b := testBox(t)
	p, w := sealFixture(t, b)
	wrong, err := New("master-a", map[string][]byte{"master-a": testKey(2)}, testKey(3))
	if err != nil {
		t.Fatal(err)
	}
	assertRejected(t, wrong, testContext(), p, w)
	// Even giving the correct raw key a different name cannot relabel a wrapper.
	relabelled, err := New("master-b", map[string][]byte{"master-b": testKey(1)}, testKey(3))
	if err != nil {
		t.Fatal(err)
	}
	w.KeyID = "master-b"
	assertRejected(t, relabelled, testContext(), p, w)
}

func TestKeyringInputOwnershipAndValidation(t *testing.T) {
	key, contentKey := testKey(1), testKey(3)
	keys := map[string][]byte{"master-a": key}
	b, err := New("master-a", keys, contentKey)
	if err != nil {
		t.Fatal(err)
	}
	p, w := sealFixture(t, b)
	before, _ := b.Digest(PurposeResourceContent, []byte("synthetic"))
	clear(key)
	clear(contentKey)
	delete(keys, "master-a")
	keys["master-b"] = testKey(9)
	if _, err := b.Open(testContext(), p, w); err != nil {
		t.Fatal("Box retained an alias to caller-owned master keys")
	}
	after, _ := b.Digest(PurposeResourceContent, []byte("synthetic"))
	if !bytes.Equal(before, after) {
		t.Fatal("Box retained an alias to caller-owned content key")
	}
	for _, tc := range []struct {
		name   string
		active string
		keys   map[string][]byte
		hmac   []byte
	}{
		{"missing", "a", nil, testKey(3)},
		{"empty_id", "", map[string][]byte{"": testKey(1)}, testKey(3)},
		{"bad_id", "secret/id", map[string][]byte{"secret/id": testKey(1)}, testKey(3)},
		{"long_id", strings.Repeat("a", 65), map[string][]byte{strings.Repeat("a", 65): testKey(1)}, testKey(3)},
		{"unknown_active", "b", map[string][]byte{"a": testKey(1)}, testKey(3)},
		{"short_master", "a", map[string][]byte{"a": make([]byte, 31)}, testKey(3)},
		{"long_master", "a", map[string][]byte{"a": make([]byte, 33)}, testKey(3)},
		{"bad_old_key", "a", map[string][]byte{"a": testKey(1), "secret/id": testKey(2)}, testKey(3)},
		{"missing_hmac", "a", map[string][]byte{"a": testKey(1)}, nil},
		{"short_hmac", "a", map[string][]byte{"a": testKey(1)}, make([]byte, 31)},
		{"long_hmac", "a", map[string][]byte{"a": testKey(1)}, make([]byte, 33)},
		{"purpose_reuse", "a", map[string][]byte{"a": testKey(1)}, testKey(1)},
		{"old_purpose_reuse", "a", map[string][]byte{"a": testKey(1), "b": testKey(3)}, testKey(3)},
		{"master_alias", "a", map[string][]byte{"a": testKey(1), "b": testKey(1)}, testKey(3)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := New(tc.active, tc.keys, tc.hmac)
			if err != ErrKeyring || got != nil {
				t.Fatal("unsafe keyring accepted or error disclosed input")
			}
		})
	}
	limitKeys := make(map[string][]byte)
	for i := range MaxKeys {
		limitKeys[fmt.Sprintf("key-%d", i)] = testKey(byte(i + 10))
	}
	if _, err := New("key-0", limitKeys, testKey(3)); err != nil {
		t.Fatal("documented maximum keyring rejected")
	}
	limitKeys["too-many"] = testKey(90)
	if got, err := New("key-0", limitKeys, testKey(3)); got != nil || err != ErrKeyring {
		t.Fatal("oversized keyring accepted")
	}
}

func TestReturnedValuesDoNotAlias(t *testing.T) {
	b := testBox(t)
	plaintext := []byte("synthetic-credential-never-log")
	p, w, err := b.Seal(testContext(), plaintext)
	if err != nil {
		t.Fatal(err)
	}
	clear(plaintext)
	first, err := b.Open(testContext(), p, w)
	if err != nil || string(first) != "synthetic-credential-never-log" {
		t.Fatal("Seal retained caller-owned plaintext")
	}
	clear(first)
	second, err := b.Open(testContext(), p, w)
	if err != nil || string(second) != "synthetic-credential-never-log" {
		t.Fatal("Open reused a caller-owned plaintext buffer")
	}
	p2, w2 := sealFixture(t, b)
	clear(p.Nonce)
	clear(p.Ciphertext)
	clear(w.Nonce)
	clear(w.Ciphertext)
	if _, err := b.Open(testContext(), p2, w2); err != nil {
		t.Fatal("Seal reused a previously returned nonce or ciphertext buffer")
	}
	digest, _ := b.Digest(PurposeResourceContent, second)
	expected := bytes.Clone(digest)
	clear(digest)
	again, _ := b.Digest(PurposeResourceContent, second)
	if !bytes.Equal(expected, again) {
		t.Fatal("Digest reused a previously returned digest buffer")
	}
}

func TestDigestDomainsAndBounds(t *testing.T) {
	b := testBox(t)
	seen := make(map[string]bool)
	for _, purpose := range []string{PurposeResourceContent, PurposeNodeIdentity, PurposeIdempotency, PurposeCursor} {
		digest, err := b.Digest(purpose, []byte("same synthetic content"))
		if err != nil || len(digest) != 32 || seen[string(digest)] {
			t.Fatal("HMAC purposes were not separated")
		}
		seen[string(digest)] = true
	}
	for _, purpose := range []string{"", "resource", PurposeCursor + "\x00", "secret-purpose"} {
		if data, err := b.Digest(purpose, nil); err != ErrDigest || data != nil {
			t.Fatal("unknown HMAC purpose accepted")
		}
	}
	p, w, err := b.Seal(testContext(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := b.Open(testContext(), p, w); err != nil || len(got) != 0 {
		t.Fatal("authenticated empty plaintext failed")
	}
	limit := bytes.Repeat([]byte{0x42}, MaxPlaintextBytes+1)
	p, w, err = b.Seal(testContext(), limit[:MaxPlaintextBytes])
	if err != nil {
		t.Fatal("documented payload maximum rejected")
	}
	if got, err := b.Open(testContext(), p, w); err != nil || !bytes.Equal(got, limit[:MaxPlaintextBytes]) {
		t.Fatal("maximum payload did not round trip")
	}
	if _, err := b.Digest(PurposeResourceContent, limit[:MaxDigestBytes]); err != nil {
		t.Fatal("documented digest maximum rejected")
	}
	if gotP, gotW, err := b.Seal(testContext(), limit); err != ErrSeal || !reflect.DeepEqual(gotP, Payload{}) || !reflect.DeepEqual(gotW, Wrapping{}) {
		t.Fatal("oversized plaintext accepted")
	}
	if got, err := b.Digest(PurposeResourceContent, limit); err != ErrDigest || got != nil {
		t.Fatal("oversized digest input accepted")
	}
	p.Ciphertext = append(p.Ciphertext, 0)
	assertRejected(t, b, testContext(), p, w)
}

func TestFailureNeverReturnsPartialCiphertextOrSecretDetails(t *testing.T) {
	for _, available := range []int{0, KeySize - 1, KeySize, KeySize + nonceSize - 1, KeySize + nonceSize, KeySize + 2*nonceSize - 1} {
		b := testBox(t)
		b.random = bytes.NewReader(make([]byte, available))
		p, w, err := b.Seal(testContext(), []byte("synthetic-never-log"))
		if err != ErrSeal || !reflect.DeepEqual(p, Payload{}) || !reflect.DeepEqual(w, Wrapping{}) {
			t.Fatal("randomness failure returned a partial encrypted record")
		}
	}
	b := testBox(t)
	p, w := sealFixture(t, b)
	b.random = brokenReader{}
	if got, err := b.Rewrap(testContext(), p, w); err != ErrOpen || !reflect.DeepEqual(got, Wrapping{}) {
		t.Fatal("rewrapping exposed a randomness error or partial replacement")
	}
	for _, empty := range []*Box{nil, {}} {
		if p, w, err := empty.Seal(testContext(), nil); err != ErrSeal || !reflect.DeepEqual(p, Payload{}) || !reflect.DeepEqual(w, Wrapping{}) {
			t.Fatal("uninitialized Box encrypted")
		}
		assertRejected(t, empty, testContext(), p, w)
		if got, err := empty.Digest(PurposeCursor, nil); err != ErrDigest || got != nil {
			t.Fatal("uninitialized Box computed a digest")
		}
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("synthetic-io-secret-never-log") }

func TestConcurrentOperations(t *testing.T) {
	b := testBox(t)
	for i := range 16 {
		t.Run(fmt.Sprintf("record-%d", i), func(t *testing.T) {
			t.Parallel()
			ctx := testContext()
			ctx.Revision = int64(i + 1)
			p, w, err := b.Seal(ctx, []byte("synthetic-concurrent"))
			if err != nil {
				t.Fatal(err)
			}
			next, err := b.Rewrap(ctx, p, w)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := b.Open(ctx, p, next); err != nil || string(got) != "synthetic-concurrent" {
				t.Fatal("concurrent encryption changed the authenticated data")
			}
		})
	}
}

func TestFormattingRedactsAllSensitiveAggregates(t *testing.T) {
	const marker = "synthetic-sensitive-value-never-log"
	values := []any{
		testBox(t), *testBox(t), Context{ScopeID: ir.ID(marker), Table: marker, ObjectID: ir.ID(marker)},
		Payload{Nonce: []byte(marker), Ciphertext: []byte(marker)},
		Wrapping{KeyID: marker, Nonce: []byte(marker), Ciphertext: []byte(marker)},
	}
	for _, value := range values {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			if got := fmt.Sprintf(verb, value); got != "[REDACTED]" {
				t.Fatal("fmt exposed a sensitive aggregate")
			}
		}
		var out bytes.Buffer
		slog.New(slog.NewJSONHandler(&out, nil)).Info("encryption", "value", value)
		if !strings.Contains(out.String(), `"value":"[REDACTED]"`) || strings.Contains(out.String(), marker) {
			t.Fatal("slog exposed a sensitive aggregate")
		}
	}
	// Envelopes deliberately retain an explicit JSON persistence representation.
	b := testBox(t)
	p, w := sealFixture(t, b)
	pJSON, _ := json.Marshal(p)
	wJSON, _ := json.Marshal(w)
	var fromP Payload
	var fromW Wrapping
	if err := json.Unmarshal(pJSON, &fromP); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wJSON, &fromW); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Open(testContext(), fromP, fromW); err != nil {
		t.Fatal("JSON persistence changed the envelope")
	}
	if bytes.Contains(pJSON, []byte("synthetic-credential-never-log")) || bytes.Contains(wJSON, []byte("synthetic-credential-never-log")) {
		t.Fatal("persistence representation contained plaintext")
	}
}

func TestAADV1EncodingIsFrozenAndDomainsDiffer(t *testing.T) {
	// Fixed wire fixture: prefix; sized purpose; version; sized scope/table/ID;
	// 64-bit revision; 32-bit schema. This preserves existing ciphertext decoding.
	want, err := hex.DecodeString(
		"70726f78796c6f6f6d2e736563726574626f782e61616400" +
			"000000077061796c6f616400000001" +
			"0000002431313131313131312d313131312d343131312d383131312d313131313131313131313131" +
			"000000127265736f757263655f7265766973696f6e73" +
			"0000002432323232323232322d323232322d343232322d383232322d323232323232323232323232" +
			"000000000000000700000001")
	if err != nil {
		t.Fatal(err)
	}
	got := contextAAD(testContext(), "payload", PayloadVersion)
	if !bytes.Equal(got, want) {
		t.Fatal("AAD v1 changed; existing ciphertext requires the original encoding")
	}
	if bytes.Equal(got, contextAAD(testContext(), "dek", WrappingVersion)) {
		t.Fatal("payload and DEK authentication domains overlap")
	}
	if bytes.Equal(appendField(appendField(nil, "ab"), "c"), appendField(appendField(nil, "a"), "bc")) {
		t.Fatal("variable-width fields have an ambiguous encoding")
	}
}
