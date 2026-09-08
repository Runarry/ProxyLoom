package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	// Keep these file-boundary limits aligned with secretbox's keyring contract.
	keyBytes        = 32
	maxMasterKeys   = 16
	maxKeyIDBytes   = 64
	maxKeyMapBytes  = 64 << 10
	maxKeyPathBytes = 4096
)

// KeyMaterial owns fresh file-read buffers. Pass the values to secretbox.New,
// which copies them, then Clear the temporary material. Callers must not log
// individual fields. Unlike encrypted envelopes this type has no JSON wire form.
type KeyMaterial struct {
	ActiveKeyID    string
	MasterKeys     map[string][]byte
	TokenPepper    []byte
	ContentHMACKey []byte
}

func (KeyMaterial) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, "[REDACTED]") }
func (KeyMaterial) LogValue() slog.Value           { return slog.StringValue("[REDACTED]") }
func (KeyMaterial) MarshalJSON() ([]byte, error)   { return []byte(`"[REDACTED]"`), nil }

// Clear zeroes owned buffers and drops the references. It is best effort: Go's
// runtime and cryptographic implementations can retain inaccessible copies.
func (k *KeyMaterial) Clear() {
	if k == nil {
		return
	}
	for id, key := range k.MasterKeys {
		clear(key)
		delete(k.MasterKeys, id)
	}
	clear(k.TokenPepper)
	clear(k.ContentHMACKey)
	*k = KeyMaterial{}
}

// ReadKeys rereads and validates the key files on every call. MasterKeyID is
// mandatory; OldMasterKeysFile is optional. The latter is a strict JSON object
// of old key_id -> absolute raw 32-byte file path, with at most 15 old keys.
// The three primary keys remain independent raw files, never environment values.
func (c API) ReadKeys() (KeyMaterial, error) {
	if !validMasterKeyID(c.MasterKeyID) {
		return KeyMaterial{}, configError("PROXYLOOM_MASTER_KEY_ID", "invalid_key_id")
	}
	k := KeyMaterial{ActiveKeyID: c.MasterKeyID, MasterKeys: make(map[string][]byte)}
	succeeded := false
	defer func() {
		if !succeeded {
			k.Clear()
		}
	}()
	var err error
	k.MasterKeys[c.MasterKeyID], err = readKey(c.MasterKeyFile, "PROXYLOOM_MASTER_KEY_FILE")
	if err != nil {
		return KeyMaterial{}, err
	}
	k.TokenPepper, err = readKey(c.TokenPepperFile, "PROXYLOOM_TOKEN_PEPPER_FILE")
	if err != nil {
		return KeyMaterial{}, err
	}
	if bytes.Equal(k.TokenPepper, k.MasterKeys[c.MasterKeyID]) {
		return KeyMaterial{}, configError("PROXYLOOM_TOKEN_PEPPER_FILE", "reused_key")
	}
	k.ContentHMACKey, err = readKey(c.ContentHMACKeyFile, "PROXYLOOM_CONTENT_HMAC_KEY_FILE")
	if err != nil {
		return KeyMaterial{}, err
	}
	if bytes.Equal(k.ContentHMACKey, k.TokenPepper) || bytes.Equal(k.ContentHMACKey, k.MasterKeys[c.MasterKeyID]) {
		return KeyMaterial{}, configError("PROXYLOOM_CONTENT_HMAC_KEY_FILE", "reused_key")
	}
	if c.OldMasterKeysFile != "" {
		if err := k.readOldKeys(c.OldMasterKeysFile); err != nil {
			return KeyMaterial{}, err
		}
	}
	succeeded = true
	return k, nil
}

func (k *KeyMaterial) readOldKeys(path string) error {
	const name = "PROXYLOOM_OLD_MASTER_KEYS_FILE"
	data, err := readFile(path, name, maxKeyMapBytes)
	if err != nil {
		return err
	}
	defer clear(data)
	entries, err := parseOldKeyFiles(data, k.ActiveKeyID)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		key, err := readKey(entry.path, name)
		if err != nil {
			return err
		}
		reused := bytes.Equal(key, k.TokenPepper) || bytes.Equal(key, k.ContentHMACKey)
		for _, previous := range k.MasterKeys {
			reused = reused || bytes.Equal(key, previous)
		}
		if reused {
			clear(key)
			return configError(name, "reused_key")
		}
		k.MasterKeys[entry.id] = key
	}
	return nil
}

type keyFile struct{ id, path string }

func parseOldKeyFiles(data []byte, activeKeyID string) ([]keyFile, error) {
	invalid := configError("PROXYLOOM_OLD_MASTER_KEYS_FILE", "invalid_key_map")
	if len(data) > maxKeyMapBytes || !utf8.Valid(data) {
		return nil, invalid
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return nil, invalid
	}
	seen := map[string]bool{activeKeyID: true}
	var entries []keyFile
	for dec.More() {
		if len(entries) >= maxMasterKeys-1 {
			return nil, invalid
		}
		token, err := dec.Token()
		id, ok := token.(string)
		if err != nil || !ok || !validMasterKeyID(id) || seen[id] {
			return nil, invalid
		}
		seen[id] = true
		token, err = dec.Token()
		path, ok := token.(string)
		if err != nil || !ok || !validKeyPath(path) {
			return nil, invalid
		}
		entries = append(entries, keyFile{id: id, path: path})
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') {
		return nil, invalid
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, invalid
	}
	return entries, nil
}

func readKey(path, name string) ([]byte, error) {
	key, err := readFile(path, name, keyBytes)
	if err != nil {
		return nil, err
	}
	if len(key) != keyBytes {
		clear(key)
		return nil, configError(name, "invalid_key_size")
	}
	return key, nil
}

func validMasterKeyID(id string) bool {
	if len(id) == 0 || len(id) > maxKeyIDBytes {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validKeyPath(path string) bool {
	if len(path) == 0 || len(path) > maxKeyPathBytes || !filepath.IsAbs(path) || strings.ContainsRune(path, utf8.RuneError) {
		return false
	}
	for _, c := range path {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}
