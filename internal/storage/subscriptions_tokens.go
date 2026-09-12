package storage

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/Runarry/ProxyLoom/internal/subscriptions"
	"github.com/jackc/pgx/v5"
	"slices"
	"strings"
	"time"
)

func (s *Subscriptions) tokenDigest(publicID, secret string) []byte {
	m := hmac.New(sha256.New, s.pepper)
	_, _ = m.Write([]byte("subscription\x00" + publicID + "." + secret))
	return m.Sum(nil)
}
func (s *Subscriptions) IssueToken(ctx context.Context, a subscriptions.Actor, profile ir.ID, request subscriptions.TokenRequest) (subscriptions.TokenIssue, error) {
	if !validIDs(a.ScopeID, a.ID, profile) || apicontract.ValidateDTO("TokenCreateRequest", request) != nil {
		return subscriptions.TokenIssue{}, catalog.ErrInvalidInput
	}
	tx, err := s.catalog.pool.Begin(ctx)
	if err != nil {
		return subscriptions.TokenIssue{}, catalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	scope, err := dbgen.New(tx).LockScope(ctx, dbID(a.ScopeID))
	if err != nil {
		return subscriptions.TokenIssue{}, subError(err)
	}
	r, err := s.profile(ctx, tx, a.ScopeID, profile)
	if err != nil {
		return subscriptions.TokenIssue{}, subError(err)
	}
	if !r.Metadata.Enabled {
		return subscriptions.TokenIssue{}, subscriptions.ErrBlocked
	}
	keys := slices.Clone(request.AllowedTargets)
	slices.Sort(keys)
	originalRequest := request
	for _, key := range keys {
		allowed := false
		for _, target := range r.Payload.(*ir.SubscriptionProfile).Targets {
			if target.IsEnabled() && target.Key == key {
				allowed = true
			}
		}
		if !allowed {
			return subscriptions.TokenIssue{}, catalog.ErrInvalidInput
		}
	}
	id, replay, err := s.operation(ctx, tx, a, "token_issue", struct {
		Profile ir.ID
		Request subscriptions.TokenRequest
	}{profile, originalRequest}, jobs.NewID())
	if err != nil {
		return subscriptions.TokenIssue{}, subError(err)
	}
	issued := subscriptions.TokenIssue{Replayed: replay}
	if !replay {
		if request.ExpiresAt != nil && !request.ExpiresAt.After(time.Now()) {
			return issued, catalog.ErrInvalidInput
		}
		secret := make([]byte, 32)
		if _, err = rand.Read(secret); err != nil {
			return issued, catalog.ErrUnavailable
		}
		defer clear(secret)
		publicID := strings.ReplaceAll(string(id), "-", "")
		raw := base64.RawURLEncoding.EncodeToString(secret)
		issued.Token = "sub_" + publicID + "." + raw
		_, err = tx.Exec(ctx, `INSERT INTO public.subscription_tokens(id,scope_id,profile_id,public_id,secret_digest,name,allowed_targets,auth_epoch,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, dbID(id), dbID(a.ScopeID), dbID(profile), publicID, s.tokenDigest(publicID, raw), request.Name, keys, scope.AuthEpoch, request.ExpiresAt)
		if err == nil {
			err = s.audit(ctx, tx, a, id, "token_issue")
		}
		if err != nil {
			return subscriptions.TokenIssue{}, subError(err)
		}
	}
	issued.Metadata, err = s.tokenMetadata(ctx, tx, a.ScopeID, id)
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return subscriptions.TokenIssue{}, subError(err)
	}
	return issued, nil
}
func (s *Subscriptions) tokenMetadata(ctx context.Context, tx subTx, scope, id ir.ID) (subscriptions.TokenMetadata, error) {
	m := subscriptions.TokenMetadata{TokenID: id, State: "active"}
	var revision int64
	var expired bool
	err := tx.QueryRow(ctx, `SELECT profile_id::text,revision,name,allowed_targets,created_at,expires_at,revoked_at,COALESCE(expires_at<=clock_timestamp(),false) FROM public.subscription_tokens WHERE scope_id=$1 AND id=$2`, dbID(scope), dbID(id)).Scan(&m.SubscriptionID, &revision, &m.Name, &m.AllowedTargets, &m.CreatedAt, &m.ExpiresAt, &m.RevokedAt, &expired)
	m.Revision = subscriptions.Revision(revision)
	if m.RevokedAt != nil {
		m.State = "revoked"
	} else if expired {
		m.State = "expired"
	}
	return m, err
}
func (s *Subscriptions) Tokens(ctx context.Context, scope, profile, after ir.ID, limit int) ([]subscriptions.TokenMetadata, error) {
	if !validIDs(scope, profile) || limit < 1 || limit > 201 {
		return nil, catalog.ErrInvalidInput
	}
	if _, err := s.profile(ctx, s.catalog.pool, scope, profile); err != nil {
		return nil, subError(err)
	}
	rows, err := s.catalog.pool.Query(ctx, `SELECT id::text FROM public.subscription_tokens WHERE scope_id=$1 AND profile_id=$2 AND ($3::uuid IS NULL OR (created_at,id)>(SELECT created_at,id FROM public.subscription_tokens WHERE scope_id=$1 AND profile_id=$2 AND id=$3)) ORDER BY created_at,id LIMIT $4`, dbID(scope), dbID(profile), nullableID(after), limit)
	if err != nil {
		return nil, subError(err)
	}
	ids := []ir.ID{}
	for rows.Next() {
		var id ir.ID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, subError(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, subError(err)
	}
	out := []subscriptions.TokenMetadata{}
	for _, id := range ids {
		m, err := s.tokenMetadata(ctx, s.catalog.pool, scope, id)
		if err != nil {
			return nil, subError(err)
		}
		out = append(out, m)
	}
	return out, nil
}
func (s *Subscriptions) RevokeToken(ctx context.Context, a subscriptions.Actor, id ir.ID, expected int64) (subscriptions.TokenMetadata, error) {
	if !validIDs(a.ScopeID, a.ID, id) || expected < 1 {
		return subscriptions.TokenMetadata{}, catalog.ErrInvalidInput
	}
	tx, err := s.catalog.pool.Begin(ctx)
	if err != nil {
		return subscriptions.TokenMetadata{}, catalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = dbgen.New(tx).LockScope(ctx, dbID(a.ScopeID)); err != nil {
		return subscriptions.TokenMetadata{}, subError(err)
	}
	m, err := s.tokenMetadata(ctx, tx, a.ScopeID, id)
	if err != nil {
		return m, subError(err)
	}
	if int64(m.Revision) != expected {
		return m, catalog.ErrRevisionConflict
	}
	if m.RevokedAt == nil {
		_, err = tx.Exec(ctx, `UPDATE public.subscription_tokens SET revoked_at=clock_timestamp(),revision=revision+1 WHERE scope_id=$1 AND id=$2`, dbID(a.ScopeID), dbID(id))
		if err == nil {
			err = s.audit(ctx, tx, a, id, "token_revoke")
		}
	}
	if err == nil {
		m, err = s.tokenMetadata(ctx, tx, a.ScopeID, id)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return m, subError(err)
}
func (s *Subscriptions) Download(ctx context.Context, token, key string) (subscriptions.Download, error) {
	if len(token) > 128 || len(key) > 64 || !strings.HasPrefix(token, "sub_") {
		return subscriptions.Download{}, subscriptions.ErrToken
	}
	publicID, raw, ok := strings.Cut(strings.TrimPrefix(token, "sub_"), ".")
	if !ok || len(publicID) < 1 || len(publicID) > 64 {
		return subscriptions.Download{}, subscriptions.ErrToken
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	defer clear(secret)
	if err != nil || len(secret) != 32 {
		return subscriptions.Download{}, subscriptions.ErrToken
	}
	tx, err := s.catalog.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return subscriptions.Download{}, catalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	var scope, profile ir.ID
	var digest []byte
	var targets []string
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT t.scope_id::text,t.profile_id::text,t.secret_digest,t.allowed_targets,t.revoked_at IS NULL AND (t.expires_at IS NULL OR t.expires_at>clock_timestamp()) AND t.auth_epoch=s.auth_epoch FROM public.subscription_tokens t JOIN public.scopes s ON s.id=t.scope_id WHERE t.public_id=$1`, publicID).Scan(&scope, &profile, &digest, &targets, &allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return subscriptions.Download{}, subscriptions.ErrToken
	}
	if err != nil {
		return subscriptions.Download{}, subError(err)
	}
	if !allowed || !hmac.Equal(digest, s.tokenDigest(publicID, raw)) || !slices.Contains(targets, key) {
		return subscriptions.Download{}, subscriptions.ErrToken
	}
	r, err := s.profile(ctx, tx, scope, profile)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, catalog.ErrNotFound) {
		return subscriptions.Download{}, subscriptions.ErrBlocked
	}
	if err != nil {
		return subscriptions.Download{}, subError(err)
	}
	currentAllowed := false
	for _, t := range r.Payload.(*ir.SubscriptionProfile).Targets {
		if t.Key == key && t.IsEnabled() {
			currentAllowed = true
		}
	}
	if !currentAllowed {
		return subscriptions.Download{}, subscriptions.ErrToken
	}
	id, _, err := s.headID(ctx, tx, scope, profile)
	if err != nil {
		return subscriptions.Download{}, subError(err)
	}
	if id == "" {
		return subscriptions.Download{}, subscriptions.ErrNotReady
	}
	var batch ir.ID
	if err := tx.QueryRow(ctx, `SELECT batch_id::text FROM public.publications WHERE scope_id=$1 AND id=$2`, dbID(scope), dbID(id)).Scan(&batch); err != nil {
		return subscriptions.Download{}, subError(err)
	}
	if err = s.safePublished(ctx, tx, scope, id, r); err != nil {
		return subscriptions.Download{}, subError(err)
	}
	var artifact ir.ID
	var contentType string
	var format string
	if err := tx.QueryRow(ctx, `SELECT artifact_id::text,descriptor->>'format' FROM public.compile_outputs WHERE scope_id=$1 AND batch_id=$2 AND target_key=$3`, dbID(scope), dbID(batch), key).Scan(&artifact, &format); err != nil {
		return subscriptions.Download{}, subError(err)
	}
	contentType = "application/json"
	if ir.OutputFormat(format) == ir.MihomoYAML {
		contentType = "application/yaml"
	}
	plain, err := s.artifact(ctx, tx, scope, artifact)
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		clear(plain)
		return subscriptions.Download{}, subError(err)
	}
	return subscriptions.Download{Bytes: plain, ContentType: contentType}, nil
}

// Download authorization uses the relational immutable publication manifest.
// It does not decrypt or reconstruct compiler inputs on the subscription path.
func (s *Subscriptions) safePublished(ctx context.Context, tx pgx.Tx, scope, publication ir.ID, profile ir.Resource) error {
	if !profile.Metadata.Enabled {
		return subscriptions.ErrBlocked
	}
	var safe bool
	err := tx.QueryRow(ctx, `SELECT b.auth_epoch=sc.auth_epoch
 AND NOT EXISTS (SELECT 1 FROM public.publication_dependencies d LEFT JOIN public.resources r ON r.scope_id=d.scope_id AND r.id=d.resource_id WHERE d.publication_id=p.id AND (r.id IS NULL OR NOT r.enabled OR r.deleted_at IS NOT NULL OR r.security_epoch<>d.security_epoch))
 AND NOT EXISTS (SELECT 1 FROM public.compile_outputs o LEFT JOIN public.core_builds c ON c.id=o.core_build_id WHERE o.scope_id=p.scope_id AND o.batch_id=p.batch_id AND (c.id IS NULL OR NOT c.enabled))
 FROM public.publications p JOIN public.compile_batches b ON b.scope_id=p.scope_id AND b.id=p.batch_id JOIN public.scopes sc ON sc.id=p.scope_id WHERE p.scope_id=$1 AND p.id=$2`, dbID(scope), dbID(publication)).Scan(&safe)
	if err != nil {
		return err
	}
	if !safe {
		return subscriptions.ErrBlocked
	}
	rows, err := tx.Query(ctx, `SELECT o.target_key,o.descriptor->>'format' FROM public.publications p JOIN public.compile_outputs o ON o.scope_id=p.scope_id AND o.batch_id=p.batch_id WHERE p.scope_id=$1 AND p.id=$2`, dbID(scope), dbID(publication))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key, format string
		if err := rows.Scan(&key, &format); err != nil {
			return err
		}
		allowed := false
		for _, target := range profile.Payload.(*ir.SubscriptionProfile).Targets {
			if target.IsEnabled() && target.Key == key && string(target.Format) == format {
				allowed = true
			}
		}
		if !allowed {
			return subscriptions.ErrBlocked
		}
	}
	return rows.Err()
}
