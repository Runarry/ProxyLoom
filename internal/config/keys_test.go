package config

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/secretbox"
)

func keyMapFile(t *testing.T, entries map[string]string) string {
	t.Helper()
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	return secretFile(t, data)
}

func TestExplicitMasterKeyID(t *testing.T) {
	for _, id := range []string{"", " ", "secret/id", "key.id", "key\x00id", "非ASCII", strings.Repeat("k", 65)} {
		env := apiEnvironment(t)
		env["PROXYLOOM_MASTER_KEY_ID"] = id
		got, err := LoadAPI(lookup(env))
		if err == nil || got != (API{}) || err.Error() != "PROXYLOOM_MASTER_KEY_ID: invalid_key_id" {
			t.Fatal("invalid or missing explicit key ID was accepted or exposed")
		}
	}
	for _, id := range []string{"test-master-v1", "0", strings.Repeat("K", maxKeyIDBytes), "old_key-1"} {
		env := apiEnvironment(t)
		env["PROXYLOOM_MASTER_KEY_ID"] = id
		got, err := LoadAPI(lookup(env))
		if err != nil || got.MasterKeyID != id {
			t.Fatal("valid explicit master key ID rejected")
		}
	}
}

func TestReadKeysRotationAndIndependentOwnership(t *testing.T) {
	env := apiEnvironment(t)
	oldFile := secretFile(t, bytes.Repeat([]byte{4}, keyBytes))
	env["PROXYLOOM_OLD_MASTER_KEYS_FILE"] = keyMapFile(t, map[string]string{"old-master-v0": oldFile})
	c, err := LoadAPI(lookup(env))
	if err != nil {
		t.Fatal(err)
	}
	first, err := c.ReadKeys()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Clear()
	if first.ActiveKeyID != "test-master-v1" || len(first.MasterKeys) != 2 ||
		!bytes.Equal(first.MasterKeys["test-master-v1"], bytes.Repeat([]byte{1}, keyBytes)) ||
		!bytes.Equal(first.MasterKeys["old-master-v0"], bytes.Repeat([]byte{4}, keyBytes)) ||
		!bytes.Equal(first.TokenPepper, bytes.Repeat([]byte{2}, keyBytes)) ||
		!bytes.Equal(first.ContentHMACKey, bytes.Repeat([]byte{3}, keyBytes)) {
		t.Fatal("key file loading changed bytes, IDs, or independent purposes")
	}
	box, err := secretbox.New(first.ActiveKeyID, first.MasterKeys, first.ContentHMACKey)
	if err != nil {
		t.Fatal("configuration and secretbox keyring contracts disagree")
	}
	beforeDigest, err := box.Digest(secretbox.PurposeCursor, []byte("synthetic cursor"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.ReadKeys()
	if err != nil {
		t.Fatal(err)
	}
	defer second.Clear()
	masterBuffer := first.MasterKeys[first.ActiveKeyID]
	oldBuffer, pepperBuffer, hmacBuffer := first.MasterKeys["old-master-v0"], first.TokenPepper, first.ContentHMACKey
	first.Clear()
	if !reflect.DeepEqual(first, KeyMaterial{}) ||
		!bytes.Equal(masterBuffer, make([]byte, keyBytes)) || !bytes.Equal(oldBuffer, make([]byte, keyBytes)) ||
		!bytes.Equal(pepperBuffer, make([]byte, keyBytes)) || !bytes.Equal(hmacBuffer, make([]byte, keyBytes)) {
		t.Fatal("Clear did not zero all owned buffers and drop references")
	}
	if !bytes.Equal(second.MasterKeys[second.ActiveKeyID], bytes.Repeat([]byte{1}, keyBytes)) {
		t.Fatal("successive reads shared mutable key buffers")
	}
	afterDigest, err := box.Digest(secretbox.PurposeCursor, []byte("synthetic cursor"))
	if err != nil || !bytes.Equal(beforeDigest, afterDigest) {
		t.Fatal("clearing temporary key material broke the Box's private key ownership")
	}
	if err := os.WriteFile(oldFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := c.ReadKeys(); err == nil || !reflect.DeepEqual(got, KeyMaterial{}) {
		t.Fatal("changed or missing old key was silently replaced or a partial keyring escaped")
	}
	if err := c.ValidateSecrets(); err == nil {
		t.Fatal("readiness did not revalidate the old key files")
	}
}

func TestStrictOldKeyMapParsing(t *testing.T) {
	validPath := secretFile(t, bytes.Repeat([]byte{4}, keyBytes))
	quotedPath, _ := json.Marshal(validPath)
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"null", []byte(`null`)},
		{"array", []byte(`[]`)},
		{"nested", []byte(`{"old":{"path":"value"}}`)},
		{"null_path", []byte(`{"old":null}`)},
		{"number_path", []byte(`{"old":42}`)},
		{"bool_path", []byte(`{"old":true}`)},
		{"empty_path", []byte(`{"old":""}`)},
		{"relative_path", []byte(`{"old":"secret-master-file"}`)},
		{"secret_key_value", []byte(`{"old":"c3ludGhldGljLWtleS1ieXRlcy1tdXN0LW5vdC1iZS1leHBvc2Vk"}`)},
		{"active_collision", []byte(`{"test-master-v1":` + string(quotedPath) + `}`)},
		{"duplicate", []byte(`{"old":` + string(quotedPath) + `,"old":` + string(quotedPath) + `}`)},
		{"escaped_duplicate", []byte(`{"old":` + string(quotedPath) + `,"\u006fld":` + string(quotedPath) + `}`)},
		{"bad_id", []byte(`{"synthetic-secret/id":` + string(quotedPath) + `}`)},
		{"empty_id", []byte(`{"":` + string(quotedPath) + `}`)},
		{"long_id", []byte(`{"` + strings.Repeat("k", 65) + `":` + string(quotedPath) + `}`)},
		{"trailing_object", []byte(`{} {}`)},
		{"trailing_scalar", []byte(`{} true`)},
		{"trailing_garbage", []byte(`{} secret-value`)},
		{"trailing_comma", []byte(`{"old":` + string(quotedPath) + `,}`)},
		{"missing_close", []byte(`{"old":` + string(quotedPath))},
		{"invalid_utf8", []byte{'{', '"', 0xff, '"', ':', '"', '/', '"', '}'}},
		{"unpaired_surrogate", []byte(`{"old":"C:\\synthetic\ud800"}`)},
		{"escaped_control", []byte(`{"old":"C:\\synthetic\u0000"}`)},
		{"long_path", []byte(`{"old":"C:\\` + strings.Repeat("p", maxKeyPathBytes) + `"}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := apiEnvironment(t)
			env["PROXYLOOM_OLD_MASTER_KEYS_FILE"] = secretFile(t, tc.data)
			got, err := LoadAPI(lookup(env))
			if err == nil || got != (API{}) || err.Error() != "PROXYLOOM_OLD_MASTER_KEYS_FILE: invalid_key_map" {
				t.Fatal("invalid key map accepted or parser error/input was disclosed")
			}
		})
	}
	for _, valid := range []string{`{}`, " \n\t{}\r\n", `{"old":` + string(quotedPath) + `}`} {
		env := apiEnvironment(t)
		env["PROXYLOOM_OLD_MASTER_KEYS_FILE"] = secretFile(t, []byte(valid))
		if _, err := LoadAPI(lookup(env)); err != nil {
			t.Fatal("valid key map rejected")
		}
	}
}

func TestOldKeysMissingMalformedReusedAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name      string
		data      []byte
		directory bool
		missing   bool
	}{
		{"empty", nil, false, false},
		{"short", bytes.Repeat([]byte{4}, 31), false, false},
		{"long", bytes.Repeat([]byte{4}, 33), false, false},
		{"duplicate_master", bytes.Repeat([]byte{1}, 32), false, false},
		{"duplicate_pepper", bytes.Repeat([]byte{2}, 32), false, false},
		{"duplicate_hmac", bytes.Repeat([]byte{3}, 32), false, false},
		{"directory", nil, true, false},
		{"missing", nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := apiEnvironment(t)
			path := secretFile(t, tc.data)
			if tc.directory {
				path = t.TempDir()
			}
			if tc.missing {
				path = filepath.Join(t.TempDir(), "synthetic-secret-file-never-log")
			}
			env["PROXYLOOM_OLD_MASTER_KEYS_FILE"] = keyMapFile(t, map[string]string{"synthetic-private-key-id": path})
			got, err := LoadAPI(lookup(env))
			if err == nil || got != (API{}) || !strings.HasPrefix(err.Error(), "PROXYLOOM_OLD_MASTER_KEYS_FILE: ") {
				t.Fatal("invalid old key accepted")
			}
			if strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "synthetic-private-key-id") {
				t.Fatal("old key identifier or path leaked through errors")
			}
		})
	}
	env := apiEnvironment(t)
	duplicateA := secretFile(t, bytes.Repeat([]byte{4}, 32))
	duplicateB := secretFile(t, bytes.Repeat([]byte{4}, 32))
	env["PROXYLOOM_OLD_MASTER_KEYS_FILE"] = keyMapFile(t, map[string]string{"a": duplicateA, "b": duplicateB})
	if _, err := LoadAPI(lookup(env)); err == nil || err.Error() != "PROXYLOOM_OLD_MASTER_KEYS_FILE: reused_key" {
		t.Fatal("duplicate raw keys under different old IDs accepted")
	}
	for _, mapPath := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing-map"), secretFile(t, bytes.Repeat([]byte{' '}, maxKeyMapBytes+1))} {
		env["PROXYLOOM_OLD_MASTER_KEYS_FILE"] = mapPath
		if got, err := LoadAPI(lookup(env)); err == nil || got != (API{}) || strings.Contains(err.Error(), mapPath) {
			t.Fatal("missing, non-file or oversized map accepted or its path exposed")
		}
	}
	entries := make(map[string]string)
	for i := range maxMasterKeys - 1 {
		entries[fmt.Sprintf("old-%d", i)] = secretFile(t, bytes.Repeat([]byte{byte(i + 10)}, keyBytes))
	}
	env["PROXYLOOM_OLD_MASTER_KEYS_FILE"] = keyMapFile(t, entries)
	if _, err := LoadAPI(lookup(env)); err != nil {
		t.Fatal("documented maximum old key map rejected")
	}
	entries["too-many"] = secretFile(t, bytes.Repeat([]byte{90}, keyBytes))
	env["PROXYLOOM_OLD_MASTER_KEYS_FILE"] = keyMapFile(t, entries)
	if _, err := LoadAPI(lookup(env)); err == nil || err.Error() != "PROXYLOOM_OLD_MASTER_KEYS_FILE: invalid_key_map" {
		t.Fatal("key map exceeded the total master key bound")
	}
}

func TestKeyMaterialRedaction(t *testing.T) {
	const marker = "synthetic-sensitive-key-marker-12"
	k := KeyMaterial{
		ActiveKeyID: marker, MasterKeys: map[string][]byte{marker: []byte(marker)},
		TokenPepper: []byte(marker), ContentHMACKey: []byte(marker),
	}
	defer k.Clear()
	for _, value := range []any{k, &k} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			if got := fmt.Sprintf(verb, value); got != "[REDACTED]" {
				t.Fatal("fmt exposed key material")
			}
		}
		var out bytes.Buffer
		slog.New(slog.NewJSONHandler(&out, nil)).Info("keys", "value", value, "nested", map[string]any{"keys": value})
		data, err := json.Marshal(map[string]any{"keys": value})
		if err != nil {
			t.Fatal(err)
		}
		for _, encoded := range []string{marker, hex.EncodeToString([]byte(marker)), base64.StdEncoding.EncodeToString([]byte(marker))} {
			if strings.Contains(out.String(), encoded) || bytes.Contains(data, []byte(encoded)) {
				t.Fatal("logging or JSON serialization exposed key material")
			}
		}
		if string(data) != `{"keys":"[REDACTED]"}` {
			t.Fatal("key material unexpectedly gained a JSON wire representation")
		}
	}
	var zero *KeyMaterial
	zero.Clear()
}

func TestRunnerRejectsRotationConfiguration(t *testing.T) {
	for _, name := range []string{"PROXYLOOM_MASTER_KEY_ID", "PROXYLOOM_OLD_MASTER_KEYS_FILE"} {
		if _, err := LoadRunner([]string{name + "=synthetic-sensitive-value"}); err == nil {
			t.Fatal("Runner accepted an API key management setting")
		}
	}
}
