// Package source defines remote subscription sources. It is not compile IR.
package source

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
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
	AuthNone   AuthKind      = "none"
	AuthBearer AuthKind      = "bearer"
	AuthBasic  AuthKind      = "basic"
	Manual     CommitMode    = "manual"
	SafeUpdates CommitMode   = "safe_updates"
	Retain     MissingPolicy = "retain"
	Disable    MissingPolicy = "disable"
	FormatAuto Format        = "auto"
	FormatURI  Format        = "uri_list"
	FormatB64  Format        = "base64_uri_list"
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
	SchemaVersion  int            `json:"schema_version"`
	URL            ir.Secret      `json:"url"`
	Format         Format         `json:"format"`
	Auth           Auth           `json:"auth"`
	RefreshPolicy  RefreshPolicy  `json:"refresh_policy"`
	FetchLimits    FetchLimits    `json:"fetch_limits"`
	BindingRevision int64         `json:"binding_revision"`
	LastSuccessAt  *time.Time     `json:"last_success_at,omitempty"`
	LastJobID      ir.ID          `json:"last_job_id,omitempty"`
	LastError      *runnerprotocol.SafeError `json:"last_error,omitempty"`
}

type Document struct {
	Metadata ir.Metadata `json:"metadata"`
	Source   Config      `json:"source"`
}

func (Config) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, "[REDACTED]") }
func (Document) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "[REDACTED]")
}

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

func Canonical(document Document) ([]byte, error) {
	if document.Metadata.Kind != ir.KindSource || document.Source.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("source_document_invalid")
	}
	if _, err := ParseURL(string(document.Source.URL)); err != nil {
		return nil, err
	}
	switch document.Source.Format {
	case FormatAuto, FormatURI, FormatB64:
	default:
		return nil, fmt.Errorf("source_format_invalid")
	}
	if document.Source.RefreshPolicy.IntervalSeconds < 60 || document.Source.RefreshPolicy.IntervalSeconds > 2592000 {
		return nil, fmt.Errorf("source_refresh_invalid")
	}
	return json.Marshal(document)
}
