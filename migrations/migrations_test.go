package migrations

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"testing/fstest"
)

func TestEmbeddedMigrationsHaveChecksummedOrderedInputs(t *testing.T) {
	items, err := Load()
	if err != nil || len(items) == 0 {
		t.Fatalf("load migrations: %v", err)
	}
	for i, item := range items {
		digest := sha256.Sum256([]byte(item.SQL))
		if item.Version != int64(i+1) || item.Checksum != hex.EncodeToString(digest[:]) {
			t.Fatal("migration order or checksum does not match frozen SQL")
		}
	}
}

func TestMalformedMigrationManifestFailsClosed(t *testing.T) {
	for _, names := range [][]string{
		{}, {"bad.up.sql"}, {"0_bad.up.sql"}, {"1_.up.sql"}, {"2_gap.up.sql"},
		{"1_first.up.sql", "1_duplicate.up.sql"}, {"1_first.up.sql", "3_gap.up.sql"},
	} {
		source := fstest.MapFS{}
		for _, name := range names {
			source[name] = &fstest.MapFile{Data: []byte("SELECT 1;")}
		}
		if _, err := load(source); err == nil {
			t.Fatalf("invalid manifest accepted: %v", names)
		}
	}
}
