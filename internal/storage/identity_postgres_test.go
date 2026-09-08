package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/identity"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
)

const identityTestPassword = "synthetic identity password 7!"

func newIdentityTest(t *testing.T, env *postgresEnv) (*Identity, string, []byte) {
	t.Helper()
	pepper := bytes.Repeat([]byte{0x57}, 32)
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x58}, 32))
	store, err := NewIdentity(env.runtime, identity.Options{ScopeID: env.scope, SetupToken: []byte(token), TokenPepper: pepper})
	if err != nil {
		t.Fatal("identity constructor failed")
	}
	return store, token, pepper
}

func identityTestSource(index int) identity.Source {
	return identity.Source{IP: fmt.Sprintf("192.0.2.%d", index), RequestID: "synthetic-identity-request"}
}

func setupIdentityTest(t *testing.T, env *postgresEnv, store *Identity, token string) identity.Session {
	t.Helper()
	session, err := store.Setup(env.ctx, token, "admin", identityTestPassword, identityTestSource(1))
	if err != nil {
		t.Fatal("synthetic identity setup failed")
	}
	return session
}

func TestPostgresIdentitySetupConcurrentAtomicAndNeverReopens(t *testing.T) {
	env := newPostgres(t, true)
	store, token, pepper := newIdentityTest(t, env)
	if _, err := store.Setup(env.ctx, "incorrect synthetic token", "admin", identityTestPassword, identityTestSource(2)); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("invalid setup token accepted")
	}
	var wg sync.WaitGroup
	results := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Setup(env.ctx, token, "admin", identityTestPassword, identityTestSource(1))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	winners, losers := 0, 0
	for err := range results {
		if err == nil {
			winners++
		} else if errors.Is(err, identity.ErrSetupComplete) {
			losers++
		} else {
			t.Fatal("concurrent setup returned unexpected error")
		}
	}
	if winners != 1 || losers != 2 {
		t.Fatal("setup was not single winner")
	}
	var users, sessions, audits int
	if err := env.admin.QueryRow(env.ctx, "SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM sessions), (SELECT count(*) FROM identity_audit_events WHERE action='setup' AND outcome='success')").Scan(&users, &sessions, &audits); err != nil || users != 1 || sessions != 1 || audits != 1 {
		t.Fatal("setup atomic rows invalid")
	}
	before, err := env.store.Scope(env.ctx, env.scope)
	if err != nil {
		t.Fatal("scope snapshot failed")
	}
	if err := store.ResetPassword(env.ctx, "admin", "synthetic replacement password 8!"); err != nil {
		t.Fatal("password reset failed")
	}
	if _, err := store.Setup(env.ctx, token, "admin", identityTestPassword, identityTestSource(3)); !errors.Is(err, identity.ErrSetupComplete) {
		t.Fatal("reset reopened setup")
	}
	after, err := env.store.Scope(env.ctx, env.scope)
	if err != nil || after.CatalogRevision != before.CatalogRevision || after.AuthEpoch != before.AuthEpoch {
		t.Fatal("identity changed catalog or scope epoch")
	}
	restarted, err := NewIdentity(env.runtime, identity.Options{TokenPepper: pepper})
	if err != nil {
		t.Fatal("constructor with removed setup secret failed")
	}
	if _, err := restarted.Login(env.ctx, "admin", "synthetic replacement password 8!", "", identityTestSource(4)); err != nil {
		t.Fatal("removing setup token broke existing administrator")
	}
}

func TestPostgresIdentitySessionLifecyclePersistenceAndReset(t *testing.T) {
	env := newPostgres(t, true)
	store, token, pepper := newIdentityTest(t, env)
	first := setupIdentityTest(t, env, store, token)
	if !first.ReauthenticatedAt.IsZero() || first.ExpiresAt.Sub(first.LastSeenAt) != identity.AbsoluteLifetime {
		t.Fatal("new session time policy invalid")
	}
	if err := store.AuditSensitive(env.ctx, first.ID, identity.SensitiveRevealSecret, identityTestSource(1)); !errors.Is(err, identity.ErrReauthenticationRequired) {
		t.Fatal("initial login bypassed explicit reauthentication")
	}
	restarted, err := NewIdentity(env.runtime, identity.Options{TokenPepper: pepper})
	if err != nil {
		t.Fatal("restart failed")
	}
	found, err := restarted.Authenticate(env.ctx, first.ID)
	if err != nil || found.CSRFToken != first.CSRFToken || found.User != first.User {
		t.Fatal("persisted session did not survive restart")
	}
	second, err := store.Login(env.ctx, "admin", identityTestPassword, first.ID, identityTestSource(1))
	if err != nil || second.ID == first.ID || second.CSRFToken == first.CSRFToken {
		t.Fatal("successful login did not rotate session")
	}
	if _, err := store.Authenticate(env.ctx, first.ID); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("rotated session remained valid")
	}
	reauthenticated, err := store.Reauthenticate(env.ctx, second.ID, identityTestPassword, identityTestSource(1))
	if err != nil || reauthenticated.ReauthenticatedAt.IsZero() || reauthenticated.CSRFToken != second.CSRFToken {
		t.Fatal("explicit reauthentication failed")
	}
	if err := store.AuditSensitive(env.ctx, second.ID, identity.SensitiveRevealSecret, identityTestSource(1)); err != nil {
		t.Fatal("fresh reauthentication failed guard")
	}
	if err := store.ResetPassword(env.ctx, "admin", "synthetic replacement password 8!"); err != nil {
		t.Fatal("reset failed")
	}
	if _, err := store.Authenticate(env.ctx, second.ID); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("reset failed to invalidate session")
	}
	if _, err := store.Login(env.ctx, "admin", identityTestPassword, "", identityTestSource(1)); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("reset left old password valid")
	}
	third, err := store.Login(env.ctx, "admin", "synthetic replacement password 8!", "", identityTestSource(1))
	if err != nil {
		t.Fatal("new password login failed")
	}
	if err := store.Logout(env.ctx, third.ID); err != nil {
		t.Fatal("logout failed")
	}
	if _, err := store.Authenticate(env.ctx, third.ID); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("logout left session valid")
	}
	var hash string
	var auditJSON string
	if err := env.admin.QueryRow(env.ctx, "SELECT password_hash FROM users WHERE login='admin'").Scan(&hash); err != nil || strings.Contains(hash, identityTestPassword) || !strings.Contains(hash, "argon2id$v=19$m=65536,t=3,p=1") {
		t.Fatal("password storage invalid")
	}
	if err := env.admin.QueryRow(env.ctx, "SELECT json_agg(identity_audit_events)::text FROM identity_audit_events").Scan(&auditJSON); err != nil {
		t.Fatal("audit read failed")
	}
	for _, secret := range []string{first.ID, first.CSRFToken, token, identityTestPassword, identityTestSource(1).IP, "synthetic replacement password 8!"} {
		if strings.Contains(auditJSON, secret) {
			t.Fatal("audit leaked credentials or source")
		}
	}
}

func insertIdentitySessionTest(t *testing.T, env *postgresEnv, store *Identity, user identity.User, created, lastSeen time.Time, reauth *time.Time) string {
	t.Helper()
	token, err := identity.NewSessionToken()
	if err != nil {
		t.Fatal("session fixture random failed")
	}
	digest, _ := identity.SessionDigest(store.pepper, token)
	if _, err := env.admin.Exec(env.ctx, "INSERT INTO sessions(id_hash,scope_id,user_id,auth_version,auth_epoch,created_at,expires_at,last_seen_at,reauth_at) SELECT $1,u.scope_id,u.id,u.auth_version,s.auth_epoch,$3,$3::timestamptz + interval '8 hours',$4,$5 FROM users u JOIN scopes s ON s.id=u.scope_id WHERE u.id=$2", digest, dbID(user.ID), created, lastSeen, reauth); err != nil {
		t.Fatal("could not create bounded session fixture")
	}
	return token
}

func TestPostgresIdentityExpiryEpochDisableAndForeignCredentials(t *testing.T) {
	env := newPostgres(t, true)
	store, token, _ := newIdentityTest(t, env)
	first := setupIdentityTest(t, env, store, token)
	var now time.Time
	if err := env.admin.QueryRow(env.ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal("database clock failed")
	}
	expired := insertIdentitySessionTest(t, env, store, first.User, now.Add(-9*time.Hour), now.Add(-2*time.Hour), nil)
	idle := insertIdentitySessionTest(t, env, store, first.User, now.Add(-time.Hour), now.Add(-31*time.Minute), nil)
	oldReauth := now.Add(-6 * time.Minute)
	staleReauth := insertIdentitySessionTest(t, env, store, first.User, now.Add(-time.Hour), now.Add(-time.Minute), &oldReauth)
	for _, credential := range []string{expired, idle, token, "sub_synthetic.token", "runner.synthetic", first.ID + "="} {
		if _, err := store.Authenticate(env.ctx, credential); !errors.Is(err, identity.ErrUnauthenticated) {
			t.Fatal("expired, idle or foreign credential accepted")
		}
	}
	if err := store.AuditSensitive(env.ctx, staleReauth, identity.SensitiveExportPrivate, identityTestSource(1)); !errors.Is(err, identity.ErrReauthenticationRequired) {
		t.Fatal("stale reauthentication accepted")
	}
	// Restore tooling owns epoch changes. The existing catalog trigger is
	// unchanged; this isolated admin fixture simulates an authorized restore.
	if _, err := env.admin.Exec(env.ctx, "ALTER TABLE scopes DISABLE TRIGGER scopes_advance"); err != nil {
		t.Fatal("restore fixture failed")
	}
	if _, err := env.admin.Exec(env.ctx, "UPDATE scopes SET auth_epoch=auth_epoch+1 WHERE id=$1", dbID(env.scope)); err != nil {
		t.Fatal("restore epoch failed")
	}
	if _, err := env.admin.Exec(env.ctx, "ALTER TABLE scopes ENABLE TRIGGER scopes_advance"); err != nil {
		t.Fatal("restore fixture cleanup failed")
	}
	if _, err := store.Authenticate(env.ctx, first.ID); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("scope epoch was not checked live")
	}
	current, err := store.Login(env.ctx, "admin", identityTestPassword, "", identityTestSource(1))
	if err != nil {
		t.Fatal("login after epoch reset failed")
	}
	if _, err := env.admin.Exec(env.ctx, "UPDATE users SET disabled=true,auth_version=auth_version+1 WHERE id=$1", dbID(first.User.ID)); err != nil {
		t.Fatal("disable fixture failed")
	}
	if _, err := store.Authenticate(env.ctx, current.ID); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("disabled administrator remained authenticated")
	}
}

func TestPostgresIdentityLoginDoesNotRevokeAnotherUser(t *testing.T) {
	env := newPostgres(t, true)
	store, token, _ := newIdentityTest(t, env)
	first := setupIdentityTest(t, env, store, token)
	id, err := identityUUID()
	if err != nil {
		t.Fatal("fixture ID failed")
	}
	hash, err := identity.HashPassword(env.ctx, identityTestPassword)
	if err != nil {
		t.Fatal("fixture password failed")
	}
	if _, err := dbgen.New(env.admin).CreateIdentityUser(env.ctx, dbgen.CreateIdentityUserParams{ID: id, ScopeID: dbID(env.scope), Login: "second", PasswordHash: hash}); err != nil {
		t.Fatal("second administrator fixture failed")
	}
	if _, err := store.Login(env.ctx, "second", identityTestPassword, first.ID, identityTestSource(2)); err != nil {
		t.Fatal("second administrator login failed")
	}
	if _, err := store.Authenticate(env.ctx, first.ID); err != nil {
		t.Fatal("login revoked another administrator cookie")
	}
}

func TestPostgresIdentityRateLimitsPersistRecoverAndBound(t *testing.T) {
	env := newPostgres(t, true)
	store, token, pepper := newIdentityTest(t, env)
	setupIdentityTest(t, env, store, token)
	for i := 1; i <= identity.AccountAttempts; i++ {
		if _, err := store.Login(env.ctx, "admin", "incorrect synthetic password", "", identityTestSource(i)); !errors.Is(err, identity.ErrUnauthenticated) {
			t.Fatal("account attempt unexpectedly rejected")
		}
	}
	restarted, err := NewIdentity(env.runtime, identity.Options{TokenPepper: pepper})
	if err != nil {
		t.Fatal("restart failed")
	}
	if _, err := restarted.Login(env.ctx, "admin", identityTestPassword, "", identityTestSource(10)); !errors.Is(err, identity.ErrRateLimited) {
		t.Fatal("persistent account limit bypassed")
	}
	if _, err := env.admin.Exec(env.ctx, "UPDATE identity_rate_limits SET window_start=clock_timestamp()-interval '2 minutes'"); err != nil {
		t.Fatal("rate expiration fixture failed")
	}
	if _, err := restarted.Login(env.ctx, "admin", identityTestPassword, "", identityTestSource(10)); err != nil {
		t.Fatal("expired rate window permanently locked account")
	}
	for i := 0; i < identity.IPAttempts; i++ {
		if _, err := store.Login(env.ctx, fmt.Sprintf("unknown%d", i), "incorrect synthetic password", "", identityTestSource(20)); !errors.Is(err, identity.ErrUnauthenticated) {
			t.Fatal("IP attempt unexpectedly rejected")
		}
	}
	if _, err := store.Login(env.ctx, "another", identityTestPassword, "", identityTestSource(20)); !errors.Is(err, identity.ErrRateLimited) {
		t.Fatal("IP limit bypassed by varying accounts")
	}
	// The persistent cap bounds attacker-controlled unique buckets. At cap,
	// a new request is refused before KDF work, and expiry recovers normally.
	if _, err := env.admin.Exec(env.ctx, "INSERT INTO identity_rate_limits(key_hash,window_start,attempts) SELECT decode(md5(g::text)||md5(('cap'||g)::text),'hex'),clock_timestamp(),1 FROM generate_series(1,16384) AS g ON CONFLICT DO NOTHING"); err != nil {
		t.Fatal("rate cap fixture failed")
	}
	if _, err := store.Login(env.ctx, "newaccount", identityTestPassword, "", identityTestSource(21)); !errors.Is(err, identity.ErrRateLimited) {
		t.Fatal("rate storage cap failed closed check")
	}
}

func TestPostgresIdentityAuditFailureRollsBackSensitiveTransitions(t *testing.T) {
	env := newPostgres(t, true)
	store, token, _ := newIdentityTest(t, env)
	revoke := func() {
		t.Helper()
		if _, err := env.admin.Exec(env.ctx, "REVOKE INSERT(id,scope_id,actor_id,action,outcome,account_hash,source_hash,request_id) ON identity_audit_events FROM proxyloom"); err != nil {
			t.Fatal("audit outage fixture failed")
		}
	}
	grant := func() {
		t.Helper()
		if _, err := env.admin.Exec(env.ctx, "GRANT INSERT(id,scope_id,actor_id,action,outcome,account_hash,source_hash,request_id) ON identity_audit_events TO proxyloom"); err != nil {
			t.Fatal("audit recovery fixture failed")
		}
	}
	revoke()
	if _, err := store.Setup(env.ctx, token, "admin", identityTestPassword, identityTestSource(1)); !errors.Is(err, identity.ErrUnavailable) {
		t.Fatal("setup returned success without audit")
	}
	var count int
	var consumed bool
	if err := env.admin.QueryRow(env.ctx, "SELECT (SELECT count(*) FROM users),setup_consumed_at IS NOT NULL FROM identity_state").Scan(&count, &consumed); err != nil || count != 0 || consumed {
		t.Fatal("failed setup was partially committed")
	}
	grant()
	first := setupIdentityTest(t, env, store, token)
	revoke()
	if _, err := store.Login(env.ctx, "admin", identityTestPassword, first.ID, identityTestSource(1)); !errors.Is(err, identity.ErrUnavailable) {
		t.Fatal("login returned without audit")
	}
	if _, err := store.Authenticate(env.ctx, first.ID); err != nil {
		t.Fatal("failed login rotation deleted original session")
	}
	if _, err := store.Reauthenticate(env.ctx, first.ID, identityTestPassword, identityTestSource(1)); !errors.Is(err, identity.ErrUnavailable) {
		t.Fatal("reauthentication returned without audit")
	}
	if err := store.ResetPassword(env.ctx, "admin", "synthetic replacement password 8!"); !errors.Is(err, identity.ErrUnavailable) {
		t.Fatal("reset returned without audit")
	}
	if err := store.Logout(env.ctx, first.ID); !errors.Is(err, identity.ErrUnavailable) {
		t.Fatal("logout returned without audit")
	}
	grant()
	found, err := store.Authenticate(env.ctx, first.ID)
	if err != nil || !found.ReauthenticatedAt.IsZero() {
		t.Fatal("failed transitions changed session")
	}
	if _, err := store.Login(env.ctx, "admin", identityTestPassword, "", identityTestSource(2)); err != nil {
		t.Fatal("failed reset changed password")
	}
	if _, err := store.Reauthenticate(env.ctx, first.ID, identityTestPassword, identityTestSource(1)); err != nil {
		t.Fatal("reauthentication recovery failed")
	}
	revoke()
	if err := store.AuditSensitive(env.ctx, first.ID, identity.SensitiveRevealSecret, identityTestSource(1)); !errors.Is(err, identity.ErrUnavailable) {
		t.Fatal("sensitive guard returned without audit")
	}
}

func TestPostgresIdentityResetSerializesLoginAndReauthentication(t *testing.T) {
	for _, reauth := range []bool{false, true} {
		t.Run(fmt.Sprintf("reauth_%t", reauth), func(t *testing.T) {
			env := newPostgres(t, true)
			store, token, _ := newIdentityTest(t, env)
			first := setupIdentityTest(t, env, store, token)
			// Queue both requests behind a real held identity row lock so their
			// order varies, then require every old credential to be dead at end.
			lock, err := env.admin.Begin(env.ctx)
			if err != nil {
				t.Fatal("race fixture transaction failed")
			}
			if _, err := dbgen.New(lock).LockIdentityState(env.ctx); err != nil {
				t.Fatal("race fixture lock failed")
			}
			var wg sync.WaitGroup
			wg.Add(2)
			var authErr, resetErr error
			var issued identity.Session
			go func() {
				defer wg.Done()
				if reauth {
					issued, authErr = store.Reauthenticate(env.ctx, first.ID, identityTestPassword, identityTestSource(1))
				} else {
					issued, authErr = store.Login(env.ctx, "admin", identityTestPassword, "", identityTestSource(1))
				}
			}()
			go func() {
				defer wg.Done()
				resetErr = store.ResetPassword(env.ctx, "admin", "synthetic replacement password 8!")
			}()
			if err := lock.Commit(env.ctx); err != nil {
				t.Fatal("race fixture release failed")
			}
			wg.Wait()
			if resetErr != nil || authErr != nil && !errors.Is(authErr, identity.ErrUnauthenticated) {
				t.Fatal("reset race returned unexpected outcome")
			}
			for _, credential := range []string{first.ID, issued.ID} {
				if credential != "" {
					if _, err := store.Authenticate(env.ctx, credential); !errors.Is(err, identity.ErrUnauthenticated) {
						t.Fatal("old-password session survived concurrent reset")
					}
				}
			}
			if _, err := store.Login(env.ctx, "admin", "synthetic replacement password 8!", "", identityTestSource(2)); err != nil {
				t.Fatal("new password failed after race")
			}
		})
	}
}

func TestPostgresIdentityPrivilegesAndDatabaseLoss(t *testing.T) {
	env := newPostgres(t, true)
	store, token, _ := newIdentityTest(t, env)
	first := setupIdentityTest(t, env, store, token)
	for _, query := range []string{"DELETE FROM users", "UPDATE users SET role='admin'", "UPDATE users SET disabled=true", "UPDATE sessions SET expires_at=expires_at+interval '1 hour'", "DELETE FROM identity_state", "TRUNCATE identity_state", "UPDATE identity_state SET setup_consumed_at=NULL", "DELETE FROM identity_audit_events", "UPDATE identity_audit_events SET outcome='denied'", "TRUNCATE identity_audit_events", "ALTER TABLE users ADD COLUMN bypass text"} {
		if _, err := env.runtime.Exec(env.ctx, query); err == nil {
			t.Fatal("runtime identity privilege or immutable constraint bypassed")
		}
	}
	// Closing the isolated test pool simulates complete database loss without
	// exposing credentials or affecting another test's PostgreSQL database.
	broken, err := NewIdentity(env.runtime, identity.Options{TokenPepper: store.pepper})
	if err != nil {
		t.Fatal("outage constructor failed")
	}
	env.runtime.Close()
	if _, err := broken.Authenticate(context.Background(), first.ID); !errors.Is(err, identity.ErrUnavailable) {
		t.Fatal("database outage returned a cached session")
	}
	if _, err := broken.Login(context.Background(), "admin", identityTestPassword, "", identityTestSource(1)); !errors.Is(err, identity.ErrUnavailable) {
		t.Fatal("database outage login did not fail closed")
	}
	if err := broken.AuditSensitive(context.Background(), first.ID, identity.SensitiveRevealSecret, identityTestSource(1)); !errors.Is(err, identity.ErrUnavailable) {
		t.Fatal("database outage sensitive guard did not fail closed")
	}
}

func TestPostgresIdentityPersistedDigestsTamperingAndLiveUserVersion(t *testing.T) {
	env := newPostgres(t, true)
	store, token, pepper := newIdentityTest(t, env)
	first := setupIdentityTest(t, env, store, token)
	want, _ := identity.SessionDigest(pepper, first.ID)
	raw, _ := identity.TokenBytes(first.ID)
	defer clear(raw)
	var stored []byte
	if err := env.admin.QueryRow(env.ctx, "SELECT id_hash FROM sessions WHERE user_id=$1", dbID(first.User.ID)).Scan(&stored); err != nil ||
		!bytes.Equal(stored, want) || bytes.Equal(stored, raw) || len(stored) != 32 {
		t.Fatal("persisted session is not a purpose-bound HMAC digest")
	}
	var persisted string
	if err := env.admin.QueryRow(env.ctx, "SELECT jsonb_build_object('sessions',(SELECT jsonb_agg(s) FROM sessions s),'users',(SELECT jsonb_agg(u) FROM users u),'state',(SELECT jsonb_agg(i) FROM identity_state i),'audit',(SELECT jsonb_agg(a) FROM identity_audit_events a))::text").Scan(&persisted); err != nil {
		t.Fatal("could not inspect isolated persisted identity records")
	}
	for _, secret := range []string{token, first.ID, first.CSRFToken, identityTestPassword, identityTestSource(1).IP} {
		if strings.Contains(persisted, secret) {
			t.Fatal("plaintext credential or source persisted")
		}
	}
	changed := "A" + first.ID[1:]
	if first.ID[0] == 'A' {
		changed = "B" + first.ID[1:]
	}
	if _, err := store.Authenticate(env.ctx, changed); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("tampered session token authenticated")
	}
	rotated, err := NewIdentity(env.runtime, identity.Options{TokenPepper: bytes.Repeat([]byte{0x59}, 32)})
	if err != nil {
		t.Fatal("rotated pepper fixture failed")
	}
	if _, err := rotated.Authenticate(env.ctx, first.ID); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("session survived token pepper replacement")
	}
	otherScope, err := NewIdentity(env.runtime, identity.Options{ScopeID: "10000000-0000-4000-8000-000000000002", TokenPepper: pepper})
	if err != nil {
		t.Fatal("scope isolation fixture failed")
	}
	if _, err := otherScope.Authenticate(env.ctx, first.ID); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("session crossed workspace scope")
	}
	// Directly advance a user's credential version while deliberately retaining
	// its old session row. This isolates the live version check from deletion.
	replacement := "synthetic replacement password 8!"
	hash, err := identity.HashPassword(env.ctx, replacement)
	if err != nil {
		t.Fatal("credential version fixture failed")
	}
	if _, err := env.admin.Exec(env.ctx, "UPDATE users SET password_hash=$2,auth_version=auth_version+1 WHERE id=$1", dbID(first.User.ID), hash); err != nil {
		t.Fatal("credential version update failed")
	}
	if _, err := store.Authenticate(env.ctx, first.ID); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("session ignored live user auth_version")
	}
	var count int
	if err := env.admin.QueryRow(env.ctx, "SELECT count(*) FROM sessions WHERE user_id=$1", dbID(first.User.ID)).Scan(&count); err != nil || count != 1 {
		t.Fatal("version test did not retain its stale session row")
	}
	if _, err := store.Login(env.ctx, "admin", replacement, "", identityTestSource(1)); err != nil {
		t.Fatal("updated credential failed after live version rejection")
	}
	if _, err := env.admin.Exec(env.ctx, "UPDATE users SET disabled=true,auth_version=auth_version+1 WHERE id=$1", dbID(first.User.ID)); err != nil {
		t.Fatal("disabled user fixture failed")
	}
	var now time.Time
	if err := env.admin.QueryRow(env.ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal("disabled user clock fixture failed")
	}
	matchedVersion := insertIdentitySessionTest(t, env, store, first.User, now.Add(-time.Minute), now, nil)
	if _, err := store.Authenticate(env.ctx, matchedVersion); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("disabled user authenticated with matching version and epoch")
	}
}
