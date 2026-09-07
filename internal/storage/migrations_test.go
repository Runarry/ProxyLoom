package storage

import (
	"errors"
	"testing"

	"github.com/Runarry/ProxyLoom/migrations"
)

func TestMigrationStateAcceptsOnlyMatchingPrefix(t *testing.T) {
	expected := []migrations.Migration{
		{Version: 1, Name: "000001_first.up.sql", Checksum: "first-checksum"},
		{Version: 2, Name: "000002_next.up.sql", Checksum: "next-checksum"},
	}
	first := appliedMigration{Version: 1, Name: "000001_first.up.sql", Checksum: "first-checksum"}
	next := appliedMigration{Version: 2, Name: "000002_next.up.sql", Checksum: "next-checksum"}
	for _, test := range []struct {
		name    string
		applied []appliedMigration
		valid   bool
		current bool
	}{
		{"empty", nil, true, false},
		{"pending", []appliedMigration{first}, true, false},
		{"current", []appliedMigration{first, next}, true, true},
		{"deleted_prefix", []appliedMigration{next}, false, false},
		{"duplicate", []appliedMigration{first, first}, false, false},
		{"future", []appliedMigration{first, next, {Version: 3}}, false, false},
		{"changed_name", []appliedMigration{{Version: 1, Name: "renamed", Checksum: first.Checksum}}, false, false},
		{"changed_checksum", []appliedMigration{{Version: 1, Name: first.Name, Checksum: "tampered"}}, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, err := compareApplied(expected, test.applied)
			if !test.valid {
				if !errors.Is(err, ErrMigrationMismatch) {
					t.Fatalf("corrupt migration state accepted: %v", err)
				}
				return
			}
			if err != nil || status.Current != test.current || status.Applied != len(test.applied) || status.Pending != len(expected)-len(test.applied) || status.Latest != 2 {
				t.Fatalf("unexpected migration status: %+v, %v", status, err)
			}
		})
	}
}
