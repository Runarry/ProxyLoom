// Package identity defines the administrator trust boundary without database or
// HTTP dependencies. Subscription and Runner credentials are never accepted.
package identity

import (
	"context"
	"errors"
	"time"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

const (
	DefaultScopeID           ir.ID = "10000000-0000-4000-8000-000000000001"
	AbsoluteLifetime               = 8 * time.Hour
	IdleLifetime                   = 30 * time.Minute
	ReauthenticationLifetime       = 5 * time.Minute
	RateWindow                     = time.Minute
	AccountAttempts                = 5
	IPAttempts                     = 30
)

var (
	ErrUnauthenticated          = errors.New("identity: unauthenticated")
	ErrForbidden                = errors.New("identity: forbidden")
	ErrReauthenticationRequired = errors.New("identity: reauthentication required")
	ErrSetupComplete            = errors.New("identity: setup complete")
	ErrRateLimited              = errors.New("identity: rate limited")
	ErrInvalidInput             = errors.New("identity: invalid input")
	ErrUnavailable              = errors.New("identity: unavailable")
)

// Options contains host-provided configuration. An empty SetupToken disables
// setup while preserving existing sessions. A token is canonical base64url of
// exactly 32 random bytes. ScopeID defaults to the single P0 workspace.
type Options struct {
	ScopeID     ir.ID
	SetupToken  []byte
	TokenPepper []byte
}

type Source struct {
	IP        string
	RequestID string
}

type User struct {
	ID       ir.ID  `json:"id"`
	ScopeID  ir.ID  `json:"scope_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// Session is an in-memory response. ID is exclusively for the HttpOnly cookie;
// the public response may expose CSRFToken and the non-secret session metadata.
type Session struct {
	ID                string    `json:"-"`
	CSRFToken         string    `json:"csrf_token"`
	User              User      `json:"user"`
	ExpiresAt         time.Time `json:"expires_at"`
	LastSeenAt        time.Time `json:"last_seen_at"`
	ReauthenticatedAt time.Time `json:"reauthenticated_at,omitempty"`
}

// SensitiveAction is an allowlist, not caller-controlled audit text. A caller
// must obtain this guard before releasing sensitive output; audit failure fails
// closed. Mutations still require their own business transaction audit.
type SensitiveAction string

const (
	SensitiveRevealSecret  SensitiveAction = "secret.reveal"
	SensitiveExportPrivate SensitiveAction = "private.export"
	SensitiveIssueToken    SensitiveAction = "token.issue"
	SensitiveRotateKey     SensitiveAction = "key.rotate"
)

func (a SensitiveAction) Valid() bool {
	switch a {
	case SensitiveRevealSecret, SensitiveExportPrivate, SensitiveIssueToken, SensitiveRotateKey:
		return true
	}
	return false
}

type Service interface {
	Setup(context.Context, string, string, string, Source) (Session, error)
	Login(context.Context, string, string, string, Source) (Session, error)
	Authenticate(context.Context, string) (Session, error)
	Reauthenticate(context.Context, string, string, Source) (Session, error)
	Logout(context.Context, string) error
	ResetPassword(context.Context, string, string) error
	AuditSensitive(context.Context, string, SensitiveAction, Source) error
}
