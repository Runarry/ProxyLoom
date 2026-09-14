// Package backup implements the encrypted self-hosted database archive. It
// never writes plaintext dumps or key material to an archive or process log.
package backup

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/Runarry/ProxyLoom/internal/config"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
)

var (
	ErrArchive = errors.New("backup_archive_invalid")
	ErrKeys    = errors.New("backup_keys_missing_or_mismatched")
	ErrWrite   = errors.New("backup_write_failed")
)

const archiveMagic = "ProxyLoom encrypted database backup v1\n"
const maxManifest = 16 << 10

type Manifest struct {
	Version        int       `json:"schema_version"`
	BackupID       string    `json:"backup_id"`
	CreatedAt      time.Time `json:"created_at"`
	DatabaseSchema int64     `json:"database_schema"`
	Format         string    `json:"dump_format"`
	MasterKeyIDs   []string  `json:"master_key_ids"`
	KeyCheck       string    `json:"key_check_hmac"`
}

func keyCheck(m Manifest, keys config.KeyMaterial) (string, error) {
	if len(keys.ContentHMACKey) != 32 || len(keys.TokenPepper) != 32 || len(m.MasterKeyIDs) < 1 || len(m.MasterKeyIDs) > 16 {
		return "", ErrKeys
	}
	mac := hmac.New(sha256.New, keys.ContentHMACKey)
	io.WriteString(mac, "proxyloom-backup-key-check-v1\x00"+m.BackupID+"\x00")
	mac.Write(keys.TokenPepper)
	prior := ""
	for _, id := range m.MasterKeyIDs {
		key, exists := keys.MasterKeys[id]
		if !exists || len(key) != 32 || id == "" || id <= prior || strings.ContainsRune(id, 0) {
			return "", ErrKeys
		}
		io.WriteString(mac, id+"\x00")
		mac.Write(key)
		prior = id
	}
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// Encrypt writes a versioned manifest followed by PostgreSQL's custom dump.
// The caller must Close the returned writer only after pg_dump succeeds, then
// fsync and atomically publish its temporary encrypted file.
func Encrypt(dst io.Writer, recipient string, keys config.KeyMaterial, schema int64) (io.WriteCloser, Manifest, error) {
	m := Manifest{Version: 1, BackupID: string(jobs.NewID()), CreatedAt: time.Now().UTC(), DatabaseSchema: schema, Format: "postgresql-custom", MasterKeyIDs: []string{}}
	for id := range keys.MasterKeys {
		m.MasterKeyIDs = append(m.MasterKeyIDs, id)
	}
	sort.Strings(m.MasterKeyIDs)
	var err error
	m.KeyCheck, err = keyCheck(m, keys)
	if err != nil || schema < 1 {
		return nil, Manifest{}, ErrKeys
	}
	r, err := age.ParseX25519Recipient(strings.TrimSpace(recipient))
	if err != nil {
		return nil, Manifest{}, ErrKeys
	}
	w, err := age.Encrypt(dst, r)
	if err != nil {
		return nil, Manifest{}, ErrWrite
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		return nil, Manifest{}, ErrWrite
	}
	if _, err = io.WriteString(w, archiveMagic+string(encoded)+"\n"); err != nil {
		return nil, Manifest{}, ErrWrite
	}
	return w, m, nil
}

// Decrypt checks the key inventory before exposing dump bytes. Consumers must
// read through authenticated EOF; a truncated stream must never be restored.
func Decrypt(src io.Reader, identity string, keys config.KeyMaterial) (io.Reader, Manifest, error) {
	i, err := age.ParseX25519Identity(strings.TrimSpace(identity))
	if err != nil {
		return nil, Manifest{}, ErrKeys
	}
	plain, err := age.Decrypt(src, i)
	if err != nil {
		return nil, Manifest{}, ErrArchive
	}
	r := bufio.NewReaderSize(plain, maxManifest)
	magic, err := r.ReadSlice('\n')
	if err != nil || string(magic) != archiveMagic {
		return nil, Manifest{}, ErrArchive
	}
	line, err := r.ReadSlice('\n')
	if err != nil || len(line) >= maxManifest {
		return nil, Manifest{}, ErrArchive
	}
	var m Manifest
	decoder := json.NewDecoder(strings.NewReader(string(line)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&m) != nil || m.Version != 1 || m.DatabaseSchema < 1 || m.Format != "postgresql-custom" || m.CreatedAt.IsZero() || !validBackupID(m.BackupID) {
		return nil, Manifest{}, ErrArchive
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, Manifest{}, ErrArchive
	}
	check, err := keyCheck(m, keys)
	if err != nil || !hmac.Equal([]byte(check), []byte(m.KeyCheck)) {
		return nil, Manifest{}, ErrKeys
	}
	return r, m, nil
}
func validBackupID(id string) bool { return ir.ID(id).Validate() == nil }

func Verify(src io.Reader, identity string, keys config.KeyMaterial) (Manifest, error) {
	r, m, err := Decrypt(src, identity, keys)
	if err != nil {
		return Manifest{}, err
	}
	var header [5]byte
	if _, err = io.ReadFull(r, header[:]); err != nil || string(header[:]) != "PGDMP" {
		return Manifest{}, ErrArchive
	}
	if _, err = io.Copy(io.Discard, r); err != nil {
		return Manifest{}, ErrArchive
	}
	return m, nil
}
