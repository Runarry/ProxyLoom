// Package operations defines the bounded self-hosted administration surface.
package operations

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"sort"
	"time"

	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
)

var ErrInvalid = errors.New("operations_invalid_settings")

type Retention struct {
	JobEventDays     int `json:"job_event_days"`
	TestResultDays   int `json:"test_result_days"`
	AuditDays        int `json:"audit_days"`
	IdempotencyHours int `json:"idempotency_hours"`
	ImportDays       int `json:"import_days"`
	PublicationDays  int `json:"publication_days"`
	PublicationCount int `json:"publication_count"`
}
type CatalogLimits struct {
	MaxImportBytes            int `json:"max_import_bytes"`
	MaxImportItems            int `json:"max_import_items"`
	MaxDependencyResources    int `json:"max_dependency_resources"`
	MaxTargetsPerSubscription int `json:"max_targets_per_subscription"`
	MaxNodes                  int `json:"max_nodes"`
	MaxRules                  int `json:"max_rules"`
	MaxOutbounds              int `json:"max_outbounds"`
}
type Settings struct {
	Revision          runnerprotocol.Sequence `json:"revision"`
	Quota             jobs.Quota              `json:"quota"`
	Retention         Retention               `json:"retention"`
	CatalogLimits     CatalogLimits           `json:"catalog_limits"`
	PrivateProxyCIDRs []string                `json:"private_proxy_cidrs"`
	CleanupPaused     bool                    `json:"cleanup_paused"`
}

func Defaults() Settings {
	return Settings{Revision: 1, Quota: jobs.DefaultQuota(), Retention: Retention{7, 30, 180, 24, 7, 90, 20}, CatalogLimits: CatalogLimits{10 << 20, 5000, 10000, 32, 100000, 20000, 2000}, PrivateProxyCIDRs: []string{}}
}
func (s Settings) Validate() error {
	q, r, c := s.Quota, s.Retention, s.CatalogLimits
	if !q.Valid() || q.ConnectivityConcurrency > 4 || q.ThroughputConcurrency > 1 || r.JobEventDays < 1 || r.JobEventDays > 365 || r.TestResultDays < 1 || r.TestResultDays > 3650 || r.AuditDays < 1 || r.AuditDays > 3650 || r.IdempotencyHours < 1 || r.IdempotencyHours > 168 || r.ImportDays < 1 || r.ImportDays > 365 || r.PublicationDays < 1 || r.PublicationDays > 3650 || r.PublicationCount < 1 || r.PublicationCount > 1000 {
		return ErrInvalid
	}
	if c.MaxImportBytes < 1 || c.MaxImportBytes > 10<<20 || c.MaxImportItems < 1 || c.MaxImportItems > 5000 || c.MaxDependencyResources < 1 || c.MaxDependencyResources > 10000 || c.MaxTargetsPerSubscription < 1 || c.MaxTargetsPerSubscription > 32 || c.MaxNodes < 1 || c.MaxNodes > 1000000 || c.MaxRules < 1 || c.MaxRules > 20000 || c.MaxOutbounds < 1 || c.MaxOutbounds > 2000 {
		return ErrInvalid
	}
	_, err := s.PrivateProxyPrefixes()
	return err
}

// PrivateProxyPrefixes is the narrowly scoped exception for self-hosted proxy
// endpoints. It never applies to registered HTTP test targets.
func (s Settings) PrivateProxyPrefixes() ([]netip.Prefix, error) {
	if len(s.PrivateProxyCIDRs) > 16 {
		return nil, ErrInvalid
	}
	values := make([]netip.Prefix, 0, len(s.PrivateProxyCIDRs))
	for _, value := range s.PrivateProxyCIDRs {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix != prefix.Masked() || !privateProxyPrefix(prefix) || slices.Contains(values, prefix) {
			return nil, ErrInvalid
		}
		values = append(values, prefix)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].String() < values[j].String() })
	return values, nil
}

func privateProxyPrefix(prefix netip.Prefix) bool {
	for _, parent := range []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
	} {
		if prefix.Bits() >= parent.Bits() && parent.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}

type Actor struct {
	ScopeID, ID ir.ID
	RequestID   string
}
type PagePosition struct {
	ID        ir.ID
	CreatedAt time.Time
}
type AuditFilter struct {
	ActorID, ResourceID ir.ID
	Action, From, Until string
	After               PagePosition
	Limit               int
}
type Overview struct {
	Resources map[string]int64 `json:"resources"`
	Jobs      map[string]int64 `json:"jobs"`
	Budget    struct {
		Day      string `json:"utc_day"`
		Limit    int64  `json:"limit_bytes"`
		Reserved int64  `json:"reserved_bytes"`
		Settled  int64  `json:"settled_bytes"`
	} `json:"budget"`
	LastCleanup   *time.Time `json:"last_cleanup_at,omitempty"`
	CleanupPaused bool       `json:"cleanup_paused"`
	LastBackup    *time.Time `json:"last_backup_at,omitempty"`
}
type Repository interface {
	Settings(context.Context, ir.ID) (Settings, error)
	Update(context.Context, Actor, int64, Settings) (Settings, error)
	Audit(context.Context, ir.ID, AuditFilter) ([]json.RawMessage, bool, error)
	Overview(context.Context, ir.ID) (Overview, error)
}
