package storage

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/Runarry/ProxyLoom/internal/backup"
	"github.com/Runarry/ProxyLoom/internal/config"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/subscriptions"
	"github.com/jackc/pgx/v5/pgxpool"
)

func backupTestDSN(pool *pgxpool.Pool) string {
	c := pool.Config().ConnConfig
	u := url.URL{Scheme: "postgres", Host: net.JoinHostPort(c.Host, strconv.Itoa(int(c.Port))), Path: "/" + c.Database, User: url.UserPassword(c.User, c.Password), RawQuery: "sslmode=disable"}
	return u.String()
}

func TestRecoveryPostgresBackupRestoreRealToolsAndOldAuthorization(t *testing.T) {
	toolsDir := os.Getenv("PROXYLOOM_PG_TOOLS_DIR")
	if toolsDir == "" {
		if os.Getenv("PROXYLOOM_REQUIRE_RECOVERY_TESTS") == "true" {
			t.Fatal("recovery tools required")
		}
		t.Skip("locked PostgreSQL client tools not configured")
	}
	start := time.Now()
	h := newPublicationTest(t)
	e := h.env
	keys := config.KeyMaterial{ActiveKeyID: "old", MasterKeys: map[string][]byte{"old": bytes.Repeat([]byte{0x11}, 32)}, ContentHMACKey: bytes.Repeat([]byte{0x33}, 32), TokenPepper: bytes.Repeat([]byte{0x71}, 32)}
	defer keys.Clear()
	setup := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x62}, 32))
	identities, err := NewIdentity(e.runtime, identity.Options{ScopeID: e.scope, SetupToken: []byte(setup), TokenPepper: keys.TokenPepper})
	if err != nil {
		t.Fatal(err)
	}
	session := setupIdentityTest(t, e, identities, setup)
	h.publish(t, h.validate(t, h.compile(t), false), 0)
	issued, err := h.store.IssueToken(e.ctx, h.actor, h.profile.Metadata.ResourceID, subscriptions.TokenRequest{Name: "old backup token", AllowedTargets: h.keys})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.jobs.Enqueue(e.ctx, networkInput(e, jobs.DownloadThroughput, 200)); err != nil {
		t.Fatal(err)
	}
	lease := claimNetwork(t, e, h.store.jobs, jobs.DownloadThroughput, jobs.NewID())
	if lease == nil {
		t.Fatal("expected active download")
	}
	if _, err = h.store.jobs.Enqueue(e.ctx, networkInput(e, jobs.DownloadThroughput, 100)); err != nil {
		t.Fatal(err)
	}
	ageKey, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	status, err := MigrationStatus(e.ctx, e.migrator)
	if err != nil {
		t.Fatal(err)
	}
	required, err := RequiredMasterKeyIDs(e.ctx, e.migrator, status.Applied)
	if err != nil || len(required) != 1 || required[0] != "old" {
		t.Fatal("backup key inventory incomplete", err)
	}
	archive := filepath.Join(t.TempDir(), "snapshot.age")
	receipt, err := backup.Snapshot(e.ctx, toolsDir, backupTestDSN(e.migrator), archive, ageKey.Recipient().String(), keys, status.Latest)
	if err != nil || receipt.SHA256 == "" {
		t.Fatal("real encrypted pg_dump failed", err)
	}
	if _, err = backup.Snapshot(e.ctx, toolsDir, backupTestDSN(e.migrator), archive, ageKey.Recipient().String(), keys, status.Latest); !errors.Is(err, backup.ErrOutputExists) {
		t.Fatal("backup overwrite accepted")
	}
	if err = identities.Logout(e.ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.RevokeToken(e.ctx, h.actor, issued.Metadata.TokenID, 1); err != nil {
		t.Fatal(err)
	}
	restored := newPostgres(t, false)
	ciphertext, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = backup.Restore(restored.ctx, toolsDir, backupTestDSN(restored.migrator), bytes.NewReader(ciphertext[:len(ciphertext)-1]), ageKey.String(), keys); !errors.Is(err, backup.ErrArchive) {
		t.Fatal("truncated backup reached restore")
	}
	if count := importCount(t, restored, `SELECT count(*) FROM pg_tables WHERE schemaname='public'`); count != 0 {
		t.Fatal("failed archive changed empty database")
	}
	badArchive := filepath.Join(t.TempDir(), "mismatched-schema.age")
	if _, err = backup.Snapshot(e.ctx, toolsDir, backupTestDSN(e.migrator), badArchive, ageKey.Recipient().String(), keys, status.Latest-1); err != nil {
		t.Fatal(err)
	}
	badCipher, err := os.ReadFile(badArchive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = backup.Restore(restored.ctx, toolsDir, backupTestDSN(restored.migrator), bytes.NewReader(badCipher), ageKey.String(), keys); !errors.Is(err, backup.ErrDatabase) {
		t.Fatal("mismatched schema accepted", err)
	}
	if count := importCount(t, restored, `SELECT count(*) FROM pg_tables WHERE schemaname='public'`); count != 0 {
		t.Fatal("failed finalization committed old authorization")
	}
	manifest, err := backup.Restore(restored.ctx, toolsDir, backupTestDSN(restored.migrator), bytes.NewReader(ciphertext), ageKey.String(), keys)
	if err != nil || manifest.BackupID != receipt.BackupID {
		t.Fatal("real pg_restore failed", err)
	}
	status, err = MigrationStatus(restored.ctx, restored.migrator)
	if err != nil || !status.Current {
		t.Fatal("restored schema or grants invalid", err)
	}
	if epoch, epochErr := ControlAuthorizationEpoch(restored.ctx, restored.runtime); epochErr != nil || epoch == "" {
		t.Fatal("restore did not reset authority in its transaction", epochErr)
	}
	rIdentity, err := NewIdentity(restored.runtime, identity.Options{ScopeID: e.scope, TokenPepper: keys.TokenPepper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rIdentity.Authenticate(restored.ctx, session.ID); err == nil {
		t.Fatal("old backup revived logged-out session")
	}
	rQueue, err := NewJobs(restored.runtime, restored.box)
	if err != nil {
		t.Fatal(err)
	}
	rPublications, err := NewSubscriptions(restored.store, rQueue, keys.TokenPepper)
	if err != nil {
		t.Fatal(err)
	}
	defer rPublications.Close()
	if _, err = rPublications.Download(restored.ctx, issued.Token, h.keys[0]); err == nil {
		t.Fatal("old backup revived revoked token")
	}
	if _, err = rQueue.Heartbeat(restored.ctx, lease.Identity); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatal("old lease survived actual restore")
	}
	budgetTotals(t, restored, 0, 200)
	if count := importCount(t, restored, `SELECT count(*) FROM public.jobs WHERE finished_at IS NULL`); count != 0 {
		t.Fatal("restore repeated unfinished work")
	}
	if _, err = restored.store.Head(restored.ctx, e.scope, h.node.Metadata.ResourceID); err != nil {
		t.Fatal("restored encrypted node unreadable", err)
	}
	before, _ := ControlAuthorizationEpoch(restored.ctx, restored.runtime)
	if _, err = backup.Restore(restored.ctx, toolsDir, backupTestDSN(restored.migrator), bytes.NewReader(ciphertext), ageKey.String(), keys); !errors.Is(err, backup.ErrDatabase) {
		t.Fatal("nonempty database restore accepted", err)
	}
	after, _ := ControlAuthorizationEpoch(restored.ctx, restored.runtime)
	if before != after {
		t.Fatal("rejected restore changed authority")
	}
	t.Logf("PASS: encrypted custom dump, authenticated restore, no authorization revival; synthetic RTO %s", time.Since(start).Round(time.Millisecond))
}
