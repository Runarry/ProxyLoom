package apicontract

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Runarry/ProxyLoom/api"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

type SetupRequest struct {
	SetupToken string `json:"setup_token"`
	Username   string `json:"username"`
	Password   string `json:"password"`
}
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type ReauthenticationRequest struct {
	Password string `json:"password"`
}
type LogoutRequest struct{}

func (SetupRequest) Format(s fmt.State, _ rune)            { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (LoginRequest) Format(s fmt.State, _ rune)            { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (ReauthenticationRequest) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "[REDACTED]") }
func (SetupRequest) LogValue() slog.Value                  { return slog.StringValue("[REDACTED]") }
func (LoginRequest) LogValue() slog.Value                  { return slog.StringValue("[REDACTED]") }
func (ReauthenticationRequest) LogValue() slog.Value       { return slog.StringValue("[REDACTED]") }

type CurrentUser struct {
	UserID                        ir.ID      `json:"user_id"`
	Username                      string     `json:"username"`
	Role                          string     `json:"role"`
	SessionExpiresAt              time.Time  `json:"session_expires_at"`
	RecentAuthenticationExpiresAt *time.Time `json:"recent_authentication_expires_at,omitempty"`
	CSRFToken                     string     `json:"csrf_token"`
}
type SessionResponse struct {
	RequestID string      `json:"request_id"`
	Data      CurrentUser `json:"data"`
}

// Only this explicit response projection serializes the CSRF token. It cannot
// serialize the opaque session ID, password hash, auth versions or scope state.
func NewSessionResponse(requestID string, session identity.Session) (SessionResponse, error) {
	response := SessionResponse{RequestID: safeRequestID(requestID), Data: CurrentUser{
		UserID: session.User.ID, Username: session.User.Username, Role: session.User.Role,
		SessionExpiresAt: session.ExpiresAt.UTC(), CSRFToken: session.CSRFToken,
	}}
	if !session.ReauthenticatedAt.IsZero() {
		until := session.ReauthenticatedAt.Add(identity.ReauthenticationLifetime).UTC()
		response.Data.RecentAuthenticationExpiresAt = &until
	}
	data, err := json.Marshal(response)
	if err != nil {
		return SessionResponse{}, NewError(InternalError)
	}
	var value any
	if json.Unmarshal(data, &value) != nil || api.Validate("SessionResponse", value) != nil {
		return SessionResponse{}, NewError(InternalError)
	}
	return response, nil
}
