// Package migrations contains immutable, ordered schema changes shipped with the binary.
package migrations

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

//go:embed *.up.sql
var files embed.FS

// Migration is a frozen SQL input. A changed name or checksum fails validation.
type Migration struct {
	Version  int64
	Name     string
	SQL      string
	Checksum string
}

func Load() ([]Migration, error) { return load(files) }

func load(source fs.FS) ([]Migration, error) {
	names, err := fs.Glob(source, "*.up.sql")
	if err != nil || len(names) == 0 {
		return nil, errors.New("migration_manifest_invalid")
	}
	result := make([]Migration, 0, len(names))
	for _, name := range names {
		stem := strings.TrimSuffix(path.Base(name), ".up.sql")
		prefix, description, ok := strings.Cut(stem, "_")
		version, parseErr := strconv.ParseInt(prefix, 10, 64)
		if !ok || parseErr != nil || version <= 0 || description == "" {
			return nil, errors.New("migration_manifest_invalid")
		}
		sql, readErr := fs.ReadFile(source, name)
		if readErr != nil || strings.TrimSpace(string(sql)) == "" {
			return nil, errors.New("migration_manifest_invalid")
		}
		digest := sha256.Sum256(sql)
		result = append(result, Migration{Version: version, Name: name, SQL: string(sql), Checksum: hex.EncodeToString(digest[:])})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Version < result[j].Version })
	for i, migration := range result {
		if migration.Version != int64(i+1) {
			return nil, errors.New("migration_manifest_invalid")
		}
	}
	return result, nil
}
