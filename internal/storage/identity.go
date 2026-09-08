package storage

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/ir"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Identity is the PostgreSQL implementation of the administrator boundary. The
// singleton identity lock linearizes password checks, reset, session revocation,
// and one-time setup across processes. It never advances catalog revisions.
type Identity struct {
	pool         *pgxpool.Pool
	scope        ir.ID
	pepper       []byte
	setupDigest  [32]byte
	setupEnabled bool
}

var _ identity.Service = (*Identity)(nil)

func NewIdentity(pool *pgxpool.Pool, options identity.Options) (*Identity, error) {
	if options.ScopeID == "" {
		options.ScopeID = identity.DefaultScopeID
	}
	if pool == nil || options.ScopeID.Validate() != nil || len(options.TokenPepper) != 32 {
		return nil, identity.ErrInvalidInput
	}
	if len(options.SetupToken) != 0 {
		bytes, ok := identity.TokenBytes(string(options.SetupToken))
		clear(bytes)
		if !ok {
			return nil, identity.ErrInvalidInput
		}
	}
	return &Identity{pool: pool, scope: options.ScopeID, pepper: append([]byte(nil), options.TokenPepper...),
		setupEnabled: len(options.SetupToken) != 0, setupDigest: sha256.Sum256(options.SetupToken)}, nil
}

type identityTx struct {
	store    *Identity
	q        *dbgen.Queries
	consumed bool
	now      pgtype.Timestamptz
	outcome  error
}

func (s *Identity) transact(ctx context.Context, fn func(*identityTx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return identity.ErrUnavailable
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	q := dbgen.New(tx)
	consumed, err := q.LockIdentityState(ctx)
	if err != nil {
		return identity.ErrUnavailable
	}
	t := &identityTx{store: s, q: q, consumed: consumed.Valid}
	if err := t.refreshTime(ctx); err != nil {
		return identity.ErrUnavailable
	}
	if err := fn(t); err != nil {
		return identityError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.ErrUnavailable
	}
	return t.outcome
}

func identityError(err error) error {
	for _, safe := range []error{identity.ErrUnauthenticated, identity.ErrForbidden, identity.ErrReauthenticationRequired,
		identity.ErrSetupComplete, identity.ErrRateLimited, identity.ErrInvalidInput} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	return identity.ErrUnavailable
}

func (t *identityTx) refreshTime(ctx context.Context) error {
	now, err := t.q.IdentityNow(ctx)
	if err != nil || !now.Valid || now.InfinityModifier != pgtype.Finite {
		return identity.ErrUnavailable
	}
	t.now = now
	return nil
}

func identityUUID() (pgtype.UUID, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return pgtype.UUID{}, identity.ErrUnavailable
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

func (t *identityTx) audit(ctx context.Context, action, outcome, account string, actor pgtype.UUID, source identity.Source) error {
	id, err := identityUUID()
	if err != nil {
		return err
	}
	args := dbgen.AppendIdentityAuditParams{ID: id, ActorID: actor, Action: action, Outcome: outcome, RequestID: source.RequestID}
	if actor.Valid {
		args.ScopeID = dbID(t.store.scope)
	}
	if account != "" {
		args.AccountHash = identity.DigestKey(t.store.pepper, "audit/account", account)
	}
	if source.IP != "" {
		args.SourceHash = identity.DigestKey(t.store.pepper, "audit/source", source.IP)
	}
	return t.q.AppendIdentityAudit(ctx, args)
}

// Denials commit their mandatory audit and consumed rate budget; database and
// audit errors instead roll back the transaction and disclose no driver detail.
func (t *identityTx) deny(ctx context.Context, action, account string, actor pgtype.UUID, source identity.Source, denial error) error {
	outcome := "denied"
	if errors.Is(denial, identity.ErrRateLimited) {
		outcome = "rate_limited"
	}
	if err := t.audit(ctx, action, outcome, account, actor, source); err != nil {
		return err
	}
	t.outcome = denial
	return nil
}

// Limits are persistent one-minute windows. The global row cap fails closed and
// expired buckets are pruned on every attempt, so no permanent lockout exists.
// Taking the IP bucket first prevents exhausted sources creating new accounts.
func (t *identityTx) attempt(ctx context.Context, account string, source identity.Source) error {
	if err := t.q.PruneIdentityRateLimits(ctx, t.now); err != nil {
		return err
	}
	for _, limit := range []struct {
		purpose, value string
		max            int32
	}{
		{"rate/source", source.IP, identity.IPAttempts}, {"rate/account", account, identity.AccountAttempts},
	} {
		key := identity.DigestKey(t.store.pepper, limit.purpose, limit.value)
		row, err := t.q.GetIdentityRateLimit(ctx, key)
		if errors.Is(err, pgx.ErrNoRows) {
			count, err := t.q.CountIdentityRateLimits(ctx)
			if err != nil {
				return err
			}
			if count >= 16384 {
				return identity.ErrRateLimited
			}
			if err := t.q.CreateIdentityRateLimit(ctx, dbgen.CreateIdentityRateLimitParams{KeyHash: key, WindowStart: t.now}); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if row.Attempts >= limit.max {
			return identity.ErrRateLimited
		} else if err := t.q.IncrementIdentityRateLimit(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

func validIdentityLogin(username, password string, source identity.Source) bool {
	return identity.ValidUsername(username) && utf8.ValidString(password) && len(password) <= 1024 && identity.ValidSource(source)
}

func (s *Identity) Setup(ctx context.Context, token, username, password string, source identity.Source) (identity.Session, error) {
	if !validIdentityLogin(username, password, source) || !identity.ValidPassword(password) || len(token) > 4096 {
		return identity.Session{}, identity.ErrInvalidInput
	}
	var result identity.Session
	err := s.transact(ctx, func(t *identityTx) error {
		if err := t.attempt(ctx, "@setup", source); err != nil {
			if errors.Is(err, identity.ErrRateLimited) {
				return t.deny(ctx, "setup", username, pgtype.UUID{}, source, err)
			}
			return err
		}
		digest := sha256.Sum256([]byte(token))
		if !hmac.Equal(s.setupDigest[:], digest[:]) || !s.setupEnabled {
			return t.deny(ctx, "setup", username, pgtype.UUID{}, source, identity.ErrForbidden)
		}
		count, err := t.q.CountIdentityUsers(ctx)
		if err != nil {
			return err
		}
		if t.consumed || count != 0 {
			return t.deny(ctx, "setup", username, pgtype.UUID{}, source, identity.ErrSetupComplete)
		}
		hash, err := identity.HashPassword(ctx, password)
		if err != nil {
			return err
		}
		if err := t.q.EnsureScope(ctx, dbgen.EnsureScopeParams{ID: dbID(s.scope), Name: "ProxyLoom"}); err != nil {
			return err
		}
		id, err := identityUUID()
		if err != nil {
			return err
		}
		user, err := t.q.CreateIdentityUser(ctx, dbgen.CreateIdentityUserParams{ID: id, ScopeID: dbID(s.scope), Login: username, PasswordHash: hash})
		if err != nil {
			return err
		}
		if err := t.q.ConsumeIdentitySetup(ctx); err != nil {
			return err
		}
		result, err = t.newSession(ctx, user)
		if err != nil {
			return err
		}
		return t.audit(ctx, "setup", "success", username, user.ID, source)
	})
	if err != nil {
		return identity.Session{}, err
	}
	return result, nil
}

func (t *identityTx) newSession(ctx context.Context, user dbgen.User) (identity.Session, error) {
	if err := t.refreshTime(ctx); err != nil {
		return identity.Session{}, err
	}
	if err := t.q.DeleteExpiredIdentitySessions(ctx, t.now); err != nil {
		return identity.Session{}, err
	}
	epoch, err := t.q.GetIdentityScopeEpoch(ctx, user.ScopeID)
	if err != nil {
		return identity.Session{}, err
	}
	token, err := identity.NewSessionToken()
	if err != nil {
		return identity.Session{}, err
	}
	digest, _ := identity.SessionDigest(t.store.pepper, token)
	row, err := t.q.CreateIdentitySession(ctx, dbgen.CreateIdentitySessionParams{IDHash: digest, ScopeID: user.ScopeID, UserID: user.ID,
		AuthVersion: user.AuthVersion, AuthEpoch: epoch, CreatedAt: t.now})
	if err != nil {
		return identity.Session{}, err
	}
	return identity.Session{ID: token, CSRFToken: identity.CSRFToken(t.store.pepper, token),
		User:      identity.User{ID: irID(user.ID), ScopeID: irID(user.ScopeID), Username: user.Login, Role: "administrator"},
		ExpiresAt: row.ExpiresAt.Time, LastSeenAt: row.LastSeenAt.Time}, nil
}

func (s *Identity) Login(ctx context.Context, username, password, oldSession string, source identity.Source) (identity.Session, error) {
	if !validIdentityLogin(username, password, source) {
		return identity.Session{}, identity.ErrInvalidInput
	}
	var result identity.Session
	err := s.transact(ctx, func(t *identityTx) error {
		if err := t.attempt(ctx, username, source); err != nil {
			if errors.Is(err, identity.ErrRateLimited) {
				return t.deny(ctx, "login", username, pgtype.UUID{}, source, err)
			}
			return err
		}
		user, err := t.q.GetIdentityUser(ctx, dbgen.GetIdentityUserParams{ScopeID: dbID(s.scope), Login: username})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		found := err == nil
		valid, err := identity.VerifyPassword(ctx, password, user.PasswordHash)
		if err != nil {
			return err
		}
		if !valid || !found || user.Disabled || user.Role != "admin" {
			return t.deny(ctx, "login", username, user.ID, source, identity.ErrUnauthenticated)
		}
		// A caller-supplied old cookie may remove only a live session owned by
		// the successfully authenticated user, never another administrator's.
		if oldSession != "" {
			old, err := t.session(ctx, oldSession)
			if err != nil && !errors.Is(err, identity.ErrUnauthenticated) {
				return err
			}
			if err == nil && old.UserID == user.ID {
				if err := t.q.DeleteIdentitySession(ctx, old.IDHash); err != nil {
					return err
				}
			}
		}
		result, err = t.newSession(ctx, user)
		if err != nil {
			return err
		}
		return t.audit(ctx, "login", "success", username, user.ID, source)
	})
	if err != nil {
		return identity.Session{}, err
	}
	return result, nil
}

func (t *identityTx) session(ctx context.Context, token string) (dbgen.GetIdentitySessionRow, error) {
	digest, ok := identity.SessionDigest(t.store.pepper, token)
	if !ok {
		return dbgen.GetIdentitySessionRow{}, identity.ErrUnauthenticated
	}
	row, err := t.q.GetIdentitySession(ctx, dbgen.GetIdentitySessionParams{IDHash: digest, ScopeID: dbID(t.store.scope)})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, identity.ErrUnauthenticated
	}
	if err != nil {
		return row, err
	}
	// Read wall time after all row locks. A request queued behind reset or a
	// long password check cannot revive a session using its transaction start.
	if err := t.refreshTime(ctx); err != nil {
		return row, err
	}
	if row.Disabled || row.Role != "admin" || row.AuthVersion != row.CurrentAuthVersion || row.AuthEpoch != row.CurrentAuthEpoch ||
		!t.now.Time.Before(row.ExpiresAt.Time) || !t.now.Time.Before(row.LastSeenAt.Time.Add(identity.IdleLifetime)) ||
		t.now.Time.Before(row.CreatedAt.Time) || t.now.Time.Before(row.LastSeenAt.Time) {
		return row, identity.ErrUnauthenticated
	}
	return row, nil
}

func (t *identityTx) publicSession(token string, row dbgen.GetIdentitySessionRow) identity.Session {
	return identity.Session{ID: token, CSRFToken: identity.CSRFToken(t.store.pepper, token),
		User:      identity.User{ID: irID(row.UserID), ScopeID: irID(row.ScopeID), Username: row.Login, Role: "administrator"},
		ExpiresAt: row.ExpiresAt.Time, LastSeenAt: t.now.Time, ReauthenticatedAt: row.ReauthAt.Time}
}

func (s *Identity) Authenticate(ctx context.Context, token string) (identity.Session, error) {
	var result identity.Session
	err := s.transact(ctx, func(t *identityTx) error {
		row, err := t.session(ctx, token)
		if err != nil {
			return err
		}
		if err := t.q.TouchIdentitySession(ctx, dbgen.TouchIdentitySessionParams{IDHash: row.IDHash, LastSeenAt: t.now}); err != nil {
			return err
		}
		result = t.publicSession(token, row)
		return nil
	})
	if err != nil {
		return identity.Session{}, err
	}
	return result, nil
}

func (s *Identity) Reauthenticate(ctx context.Context, token, password string, source identity.Source) (identity.Session, error) {
	if !identity.ValidSource(source) || !utf8.ValidString(password) || len(password) > 1024 {
		return identity.Session{}, identity.ErrInvalidInput
	}
	var result identity.Session
	err := s.transact(ctx, func(t *identityTx) error {
		row, err := t.session(ctx, token)
		if err != nil {
			return err
		}
		if err := t.attempt(ctx, row.Login, source); err != nil {
			if errors.Is(err, identity.ErrRateLimited) {
				return t.deny(ctx, "reauthenticate", row.Login, row.UserID, source, err)
			}
			return err
		}
		valid, err := identity.VerifyPassword(ctx, password, row.PasswordHash)
		if err != nil {
			return err
		}
		if !valid {
			return t.deny(ctx, "reauthenticate", row.Login, row.UserID, source, identity.ErrUnauthenticated)
		}
		// Re-check expiry after Argon work; locks already serialize reset.
		row, err = t.session(ctx, token)
		if err != nil {
			return err
		}
		if err := t.q.ReauthenticateIdentitySession(ctx, dbgen.ReauthenticateIdentitySessionParams{IDHash: row.IDHash, LastSeenAt: t.now}); err != nil {
			return err
		}
		row.ReauthAt = t.now
		result = t.publicSession(token, row)
		return t.audit(ctx, "reauthenticate", "success", row.Login, row.UserID, source)
	})
	if err != nil {
		return identity.Session{}, err
	}
	return result, nil
}

func (s *Identity) Logout(ctx context.Context, token string) error {
	return s.transact(ctx, func(t *identityTx) error {
		row, err := t.session(ctx, token)
		if err != nil {
			return err
		}
		if err := t.q.DeleteIdentitySession(ctx, row.IDHash); err != nil {
			return err
		}
		return t.audit(ctx, "logout", "success", row.Login, row.UserID, identity.Source{})
	})
}

// ResetPassword is exclusively for the controlled host CLI, never an HTTP
// capability. The state/user locks span hashing, version increment, revocation,
// and audit, so a racing login cannot issue a surviving old-password session.
func (s *Identity) ResetPassword(ctx context.Context, username, password string) error {
	if !identity.ValidUsername(username) || !identity.ValidPassword(password) {
		return identity.ErrInvalidInput
	}
	return s.transact(ctx, func(t *identityTx) error {
		user, err := t.q.GetIdentityUser(ctx, dbgen.GetIdentityUserParams{ScopeID: dbID(s.scope), Login: username})
		if errors.Is(err, pgx.ErrNoRows) {
			return t.deny(ctx, "password.reset", username, pgtype.UUID{}, identity.Source{}, identity.ErrUnauthenticated)
		}
		if err != nil {
			return err
		}
		hash, err := identity.HashPassword(ctx, password)
		if err != nil {
			return err
		}
		if err := t.q.ResetIdentityPassword(ctx, dbgen.ResetIdentityPasswordParams{ID: user.ID, PasswordHash: hash}); err != nil {
			return err
		}
		if err := t.q.DeleteIdentityUserSessions(ctx, user.ID); err != nil {
			return err
		}
		return t.audit(ctx, "password.reset", "success", username, user.ID, identity.Source{})
	})
}

func (s *Identity) AuditSensitive(ctx context.Context, token string, action identity.SensitiveAction, source identity.Source) error {
	if !action.Valid() || !identity.ValidSource(source) {
		return identity.ErrInvalidInput
	}
	return s.transact(ctx, func(t *identityTx) error {
		row, err := t.session(ctx, token)
		if err != nil {
			return err
		}
		if !row.ReauthAt.Valid || t.now.Time.Before(row.ReauthAt.Time) || !t.now.Time.Before(row.ReauthAt.Time.Add(identity.ReauthenticationLifetime)) {
			return t.deny(ctx, string(action), row.Login, row.UserID, source, identity.ErrReauthenticationRequired)
		}
		if err := t.q.TouchIdentitySession(ctx, dbgen.TouchIdentitySessionParams{IDHash: row.IDHash, LastSeenAt: t.now}); err != nil {
			return err
		}
		return t.audit(ctx, string(action), "success", row.Login, row.UserID, source)
	})
}
