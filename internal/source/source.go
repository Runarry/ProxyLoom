// Package source defines remote subscription sources. It is not compile IR.
package source

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
)

const SchemaVersion = 1

type AuthKind string
type CommitMode string
type MissingPolicy string
type Format string

const (
	AuthNone    AuthKind      = "none"
	AuthBearer  AuthKind      = "bearer"
	AuthBasic   AuthKind      = "basic"
	Manual      CommitMode    = "manual"
	SafeUpdates CommitMode    = "safe_updates"
	Retain      MissingPolicy = "retain"
	Disable     MissingPolicy = "disable"
	FormatAuto  Format        = "auto"
	FormatURI   Format        = "uri_list"
	FormatB64   Format        = "base64_uri_list"
)

type Auth struct {
	Kind     AuthKind  `json:"kind"`
	Token    ir.Secret `json:"token,omitempty"`
	Username ir.Secret `json:"username,omitempty"`
	Password ir.Secret `json:"password,omitempty"`
}

type RefreshPolicy struct {
	Enabled         bool          `json:"enabled"`
	IntervalSeconds int           `json:"interval_seconds"`
	CommitMode      CommitMode    `json:"commit_mode"`
	MissingPolicy   MissingPolicy `json:"missing_policy"`
}

type FetchLimits struct {
	TimeoutMS          int `json:"timeout_ms"`
	MaxCompressedBytes int `json:"max_compressed_bytes"`
	MaxDecodedBytes    int `json:"max_decoded_bytes"`
	MaxRedirects       int `json:"max_redirects"`
}

type Config struct {
	SchemaVersion        int                       `json:"schema_version"`
	URL                  ir.Secret                 `json:"url"`
	Format               Format                    `json:"format"`
	Auth                 Auth                      `json:"auth"`
	RefreshPolicy        RefreshPolicy             `json:"refresh_policy"`
	FetchLimits          FetchLimits               `json:"fetch_limits"`
	BindingRevision      int64                     `json:"binding_revision"`
	LastSuccessAt        *time.Time                `json:"last_success_at,omitempty"`
	LastJobID            ir.ID                     `json:"last_job_id,omitempty"`
	LatestPreviewBatchID ir.ID                     `json:"latest_preview_batch_id,omitempty"`
	LastError            *runnerprotocol.SafeError `json:"last_error,omitempty"`
}

type Document struct {
	Metadata ir.Metadata `json:"metadata"`
	Source   Config      `json:"source"`
}

type Cursor struct {
	CreatedAt time.Time
	ID        ir.ID
}

type Page struct {
	Items []Document
	Next  *Cursor
}

type Mutation struct {
	ScopeID          ir.ID
	PrincipalID      ir.ID
	ResourceID       ir.ID
	RequestID        string
	Name             string
	Tags             []string
	Enabled          bool
	Config           Config
	ExpectedRevision int64
}

type RefreshRequest struct {
	ScopeID          ir.ID
	PrincipalID      ir.ID
	SourceID         ir.ID
	RequestID        string
	ExpectedRevision int64
	IdempotencyKey   string
}

func (Config) String() string                   { return "[REDACTED]" }
func (Config) GoString() string                 { return "[REDACTED]" }
func (Config) LogValue() slog.Value             { return slog.StringValue("[REDACTED]") }
func (Document) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, "[REDACTED]") }
func (Document) String() string                 { return "[REDACTED]" }
func (Document) GoString() string               { return "[REDACTED]" }
func (Document) LogValue() slog.Value           { return slog.StringValue("[REDACTED]") }

func DisplayURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", fmt.Errorf("source_url_invalid")
	}
	host := parsed.Hostname()
	if host == "" {
		return "", fmt.Errorf("source_url_invalid")
	}
	if port := parsed.Port(); port != "" {
		return parsed.Scheme + "://" + net.JoinHostPort(host, port), nil
	}
	return parsed.Scheme + "://" + host, nil
}

func ParseURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" || len(raw) > 4096 {
		return nil, fmt.Errorf("source_url_invalid")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || parsed.Fragment != "" || parsed.Host == "" {
		return nil, fmt.Errorf("source_url_invalid")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("source_url_invalid")
	}
	return parsed, nil
}

func (c Config) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("source_document_invalid")
	}
	if _, err := ParseURL(string(c.URL)); err != nil {
		return err
	}
	switch c.Format {
	case FormatAuto, FormatURI, FormatB64:
	default:
		return fmt.Errorf("source_format_invalid")
	}
	switch c.Auth.Kind {
	case AuthNone:
		if c.Auth.Token != "" || c.Auth.Username != "" || c.Auth.Password != "" {
			return fmt.Errorf("source_auth_invalid")
		}
	case AuthBearer:
		if c.Auth.Token == "" || c.Auth.Username != "" || c.Auth.Password != "" {
			return fmt.Errorf("source_auth_invalid")
		}
	case AuthBasic:
		if c.Auth.Username == "" || c.Auth.Password == "" || c.Auth.Token != "" {
			return fmt.Errorf("source_auth_invalid")
		}
	default:
		return fmt.Errorf("source_auth_invalid")
	}
	if c.RefreshPolicy.IntervalSeconds < 60 || c.RefreshPolicy.IntervalSeconds > 2592000 {
		return fmt.Errorf("source_refresh_invalid")
	}
	if c.RefreshPolicy.CommitMode != Manual && c.RefreshPolicy.CommitMode != SafeUpdates {
		return fmt.Errorf("source_refresh_invalid")
	}
	if c.RefreshPolicy.MissingPolicy != Retain && c.RefreshPolicy.MissingPolicy != Disable {
		return fmt.Errorf("source_refresh_invalid")
	}
	if c.FetchLimits.TimeoutMS < 1000 || c.FetchLimits.TimeoutMS > 60000 ||
		c.FetchLimits.MaxCompressedBytes < 1 || c.FetchLimits.MaxCompressedBytes > 10<<20 ||
		c.FetchLimits.MaxDecodedBytes < 1 || c.FetchLimits.MaxDecodedBytes > 10<<20 ||
		c.FetchLimits.MaxRedirects < 0 || c.FetchLimits.MaxRedirects > 5 {
		return fmt.Errorf("source_limits_invalid")
	}
	if c.BindingRevision < 1 {
		return fmt.Errorf("source_document_invalid")
	}
	return nil
}

func Canonical(document Document) ([]byte, error) {
	document.Metadata.Tags = append([]string{}, document.Metadata.Tags...)
	sort.Strings(document.Metadata.Tags)
	if document.Metadata.Kind != ir.KindSource || document.Metadata.Validate() != nil || document.Source.Validate() != nil {
		return nil, fmt.Errorf("source_document_invalid")
	}
	return json.Marshal(document)
}

func ParserFormat(format Format) string {
	switch format {
	case FormatURI:
		return "text"
	case FormatB64:
		return "base64"
	default:
		return "auto"
	}
}
