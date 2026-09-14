// capacity-probe runs only inside the owned deployment acceptance host. It
// seeds through the catalog repository, then measures the real deployed API.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/compiler"
	"github.com/Runarry/ProxyLoom/internal/config"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
	"github.com/Runarry/ProxyLoom/internal/storage"
	"github.com/Runarry/ProxyLoom/internal/subscriptions"
)

const origin = "http://127.0.0.1:8080"

type saved struct{ Password, Cookie, CSRF, NodeID, MetricsToken string }
type client struct {
	http   *http.Client
	cookie string
}
type distribution struct {
	Samples int     `json:"samples"`
	P50     float64 `json:"p50_ms"`
	P95     float64 `json:"p95_ms"`
	P99     float64 `json:"p99_ms"`
	Maximum float64 `json:"max_ms"`
}
type loadResult struct {
	Concurrency int                    `json:"concurrency"`
	Elapsed     float64                `json:"elapsed_seconds"`
	Requests    uint64                 `json:"requests"`
	Errors      uint64                 `json:"errors"`
	ErrorRate   float64                `json:"error_rate"`
	Latency     distribution           `json:"latency"`
	TTFB        distribution           `json:"ttfb"`
	Routes      map[string]routeResult `json:"routes"`
}

type routeResult struct {
	Requests int          `json:"requests"`
	Errors   int          `json:"errors"`
	Latency  distribution `json:"latency"`
}

func require(ok bool, code string) {
	if !ok {
		panic(code)
	}
}
func must(err error, code string) { require(err == nil, code) }
func read(path string) []byte {
	b, e := os.ReadFile(path)
	must(e, "fixture_file_unreadable")
	return b
}
func summarize(values []int64) distribution {
	require(len(values) > 0, "measurement_has_no_samples")
	slices.Sort(values)
	quantile := func(p float64) float64 { return float64(values[int(math.Ceil(float64(len(values))*p))-1]) / 1e6 }
	return distribution{Samples: len(values), P50: quantile(.5), P95: quantile(.95), P99: quantile(.99), Maximum: quantile(1)}
}
func (c client) get(ctx context.Context, path string, authenticated bool) (*http.Response, error) {
	r, e := http.NewRequestWithContext(ctx, http.MethodGet, origin+path, nil)
	if e != nil {
		return nil, e
	}
	if authenticated {
		r.Header.Set("Cookie", c.cookie)
	}
	return c.http.Do(r)
}
func (c client) session(ctx context.Context) identity.User {
	r, e := c.get(ctx, "/api/v1/auth/me", true)
	must(e, "session_request_failed")
	defer r.Body.Close()
	require(r.StatusCode == 200, "session_not_authenticated")
	var data apicontract.SessionResponse
	must(json.NewDecoder(r.Body).Decode(&data), "session_response_invalid")
	return identity.User{ID: data.Data.UserID, ScopeID: identity.DefaultScopeID, Role: data.Data.Role, Username: data.Data.Username}
}
func reference(r ir.Resource) ir.FrozenRef {
	return ir.FrozenRef{ResourceID: r.Metadata.ResourceID, Kind: r.Metadata.Kind, Revision: r.Metadata.Revision, SecurityEpoch: r.Metadata.SecurityEpoch}
}

func load(c client, name string, concurrency int, duration time.Duration, downloadPath string, expectedBytes int, expectedHash string) loadResult {
	started := time.Now()
	deadline := started.Add(duration)
	var count, errors atomic.Uint64
	var mu sync.Mutex
	var elapsed, firstByte []int64
	var routeTimes = map[string][]int64{}
	var routeErrors = map[string]int{}
	var wg sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		wg.Go(func() {
			paths := []string{"/api/v1/nodes?limit=100", "/api/v1/chains?limit=100", "/api/v1/subscriptions?limit=100"}
			cursors := make([]string, len(paths))
			iteration := worker
			for time.Now().Before(deadline) {
				path := downloadPath
				index := iteration % len(paths)
				iteration++
				if name == "management" {
					path = paths[index]
					if cursors[index] != "" {
						path += "&cursor=" + url.QueryEscape(cursors[index])
					}
				}
				start := time.Now()
				var first int64
				trace := &httptrace.ClientTrace{GotFirstResponseByte: func() { first = time.Since(start).Nanoseconds() }}
				ctx := httptrace.WithClientTrace(context.Background(), trace)
				r, e := c.get(ctx, path, name == "management")
				valid := e == nil
				if e == nil {
					valid = r.StatusCode == 200
					if name == "management" {
						var body struct {
							Data []json.RawMessage `json:"data"`
							Page struct {
								NextCursor string `json:"next_cursor"`
							} `json:"page"`
						}
						decodeErr := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&body)
						valid = valid && decodeErr == nil && len(body.Data) > 0
						if valid {
							cursors[index] = body.Page.NextCursor
						}
					} else {
						hash := sha256.New()
						size, readErr := io.Copy(hash, io.LimitReader(r.Body, int64(expectedBytes)+1))
						valid = valid && readErr == nil && size == int64(expectedBytes) && hex.EncodeToString(hash.Sum(nil)) == expectedHash
					}
					r.Body.Close()
				}
				count.Add(1)
				if !valid {
					errors.Add(1)
				}
				mu.Lock()
				took := time.Since(start).Nanoseconds()
				elapsed = append(elapsed, took)
				route := "subscription"
				if name == "management" {
					route = strings.Split(paths[index], "?")[0]
				}
				routeTimes[route] = append(routeTimes[route], took)
				if !valid {
					routeErrors[route]++
				}
				if first > 0 {
					firstByte = append(firstByte, first)
				}
				mu.Unlock()
				if errors.Load() > 100 {
					return
				}
			}
		})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			total := count.Load()
			failed := errors.Load()
			routes := map[string]routeResult{}
			for name, times := range routeTimes {
				routes[name] = routeResult{Requests: len(times), Errors: routeErrors[name], Latency: summarize(times)}
			}
			return loadResult{Concurrency: concurrency, Elapsed: time.Since(started).Seconds(), Requests: total, Errors: failed, ErrorRate: float64(failed) / float64(max(total, 1)), Latency: summarize(elapsed), TTFB: summarize(firstByte), Routes: routes}
		case <-ticker.C:
			fmt.Printf("CAPACITY_PROGRESS %s elapsed_seconds=%d requests=%d errors=%d\n", name, int(time.Since(started).Seconds()), count.Load(), errors.Load())
		}
	}
}

func main() {
	var reportPath string
	report := map[string]any{"schema_version": 1, "result": "fail", "started_at": time.Now().UTC(), "environment": "real_production_services_in_owned_linux_amd64_docker_host", "limits": map[string]any{"host_cpus": 4, "host_memory_bytes": 6 << 30, "api_cpus": 2, "api_memory_bytes": 512 << 20, "postgres_cpus": 1, "postgres_memory_bytes": 1 << 30, "runner_cpus": 2, "runner_memory_bytes": 1 << 30}}
	defer func() {
		failure := recover()
		if failure != nil {
			report["result"] = "fail"
			report["error_code"] = fmt.Sprint(failure)
		}
		report["finished_at"] = time.Now().UTC()
		if reportPath != "" {
			b, _ := json.MarshalIndent(report, "", "  ")
			_ = os.WriteFile(reportPath, append(b, '\n'), 0600)
		}
		if failure != nil {
			fmt.Fprintln(os.Stderr, failure)
			os.Exit(1)
		}
	}()
	require(len(os.Args) == 6, "usage_state_fixture_db_address_report_management_seconds")
	state, fixture, dbAddress := os.Args[1], os.Args[2], os.Args[3]
	reportPath = os.Args[4]
	restored := os.Args[5] == "restore"
	seconds, err := strconv.Atoi(os.Args[5])
	if restored {
		must(json.Unmarshal(read(reportPath), &report), "capacity_report_unavailable")
		require(report["result"] == "pass", "capacity_not_previously_verified")
		seconds, err = 10, nil
	}
	must(err, "duration_invalid")
	require(seconds >= 10 && seconds <= 1800, "duration_out_of_range")
	if !restored {
		report["baseline_run"] = seconds >= 1200
	}
	var session saved
	must(json.Unmarshal(read(fixture), &session), "fixture_state_invalid")
	c := client{http: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil, DisableCompression: true, MaxIdleConns: 80, MaxIdleConnsPerHost: 80}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, cookie: session.Cookie}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	user := c.session(ctx)
	require(user.ID.Validate() == nil && user.ScopeID.Validate() == nil && user.Role == "administrator", "fixture_identity_invalid")
	keys, err := (config.API{MasterKeyID: strings.TrimSpace(string(read(filepath.Join(state, "secrets/master-key-id")))), MasterKeyFile: filepath.Join(state, "secrets/master_key"), TokenPepperFile: filepath.Join(state, "secrets/token_pepper"), ContentHMACKeyFile: filepath.Join(state, "secrets/content_hmac_key")}).ReadKeys()
	must(err, "fixture_keys_invalid")
	defer keys.Clear()
	dsn, err := url.Parse(strings.TrimSpace(string(read(filepath.Join(state, "secrets/database_dsn")))))
	must(err, "fixture_dsn_invalid")
	dsn.Host = dbAddress
	pool, err := storage.Open(ctx, dsn.String())
	must(err, "database_unavailable")
	defer pool.Close()
	box, err := secretbox.New(keys.ActiveKeyID, keys.MasterKeys, keys.ContentHMACKey)
	must(err, "fixture_box_invalid")
	store, err := storage.NewCatalog(pool, box)
	must(err, "catalog_unavailable")
	queue, err := storage.NewJobs(pool, box)
	must(err, "queue_unavailable")
	publications, err := storage.NewSubscriptions(store, queue, keys.TokenPepper)
	must(err, "publication_store_unavailable")
	defer publications.Close()
	if restored {
		started := time.Now()
		rows, e := pool.Query(ctx, `SELECT id::text,kind FROM public.resources WHERE scope_id=$1 AND deleted_at IS NULL ORDER BY id`, string(user.ScopeID))
		must(e, "restored_dataset_unavailable")
		type subject struct{ id, kind string }
		var subjects []subject
		for rows.Next() {
			var item subject
			must(rows.Scan(&item.id, &item.kind), "restored_dataset_invalid")
			subjects = append(subjects, item)
		}
		must(rows.Err(), "restored_dataset_unreadable")
		rows.Close()
		counts := map[string]int{}
		for _, item := range subjects {
			resource, e := store.Head(ctx, user.ScopeID, ir.ID(item.id))
			must(e, "restored_revision_cannot_decrypt")
			require(string(resource.Metadata.Kind) == item.kind, "restored_revision_kind_mismatch")
			counts[item.kind]++
		}
		require(counts["node"] == 10000 && counts["chain"] == 500 && counts["subscription_profile"] == 200, "restored_dataset_size_mismatch")
		report["restoration"] = map[string]any{"result": "pass", "dataset": counts, "all_current_revisions_decrypted": len(subjects), "verification_seconds": time.Since(started).Seconds()}
		fmt.Println("PASS: capacity-restored-dataset")
		return
	}
	cores, err := capability.Load()
	must(err, "core_catalog_invalid")
	var presets []ir.Resource
	rows, err := pool.Query(ctx, `SELECT id::text FROM public.resources WHERE scope_id=$1 AND kind='client_preset' AND deleted_at IS NULL ORDER BY id`, string(user.ScopeID))
	must(err, "preset_query_failed")
	var ids []string
	for rows.Next() {
		var id string
		must(rows.Scan(&id), "preset_row_invalid")
		ids = append(ids, id)
	}
	must(rows.Err(), "preset_query_failed")
	rows.Close()
	for _, id := range ids {
		r, e := store.Head(ctx, user.ScopeID, ir.ID(id))
		must(e, "preset_read_failed")
		if !r.Payload.(*ir.ClientPreset).ControlAPI.Enabled {
			presets = append(presets, r)
		}
	}
	var targets []ir.Target
	var selectedPresets []ir.Resource
	for _, b := range cores.Builds() {
		if b.Arch != "amd64" {
			continue
		}
		for _, p := range presets {
			v := p.Payload.(*ir.ClientPreset)
			if v.CoreFamily == b.Family {
				targets = append(targets, ir.Target{Key: string(b.Family) + "-capacity", CoreFamily: b.Family, CoreBuildID: b.ID, CoreBuildSHA256: b.BinarySHA256, AdapterVersion: b.AdapterVersion, ClientPresetID: p.Metadata.ResourceID, ClientPresetRevision: p.Metadata.Revision, Format: v.Format})
				selectedPresets = append(selectedPresets, p)
				break
			}
		}
	}
	require(len(targets) == 3, "three_presets_required")
	first, err := store.Head(ctx, user.ScopeID, ir.ID(session.NodeID))
	must(err, "seed_node_missing")
	nodes := []ir.Resource{first}
	seedStart := time.Now()
	must(store.Transact(ctx, user.ScopeID, func(tx catalog.Tx) error {
		for i := 1; i < 10000; i++ {
			udp := false
			n := &ir.Node{SchemaVersion: 1, Protocol: ir.SOCKS5, Endpoint: ir.Endpoint{Host: fmt.Sprintf("peer-%05d.example.invalid", i), Port: 1080}, Auth: &ir.UsernamePasswordAuth{Kind: ir.AuthUsernamePassword, Username: "fixture", Password: ir.Secret("capacity-fixture-credential")}, Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP}, Security: &ir.NoSecurity{Mode: ir.SecurityNone}, Features: ir.Features{UDP: &udp}, Extensions: ir.Extensions{}}
			r, e := tx.Create(ctx, catalog.CreateInput{Name: fmt.Sprintf("容量节点 %05d", i), Tags: []string{}, Enabled: true, Payload: n})
			if e != nil {
				return e
			}
			nodes = append(nodes, r)
		}
		return nil
	}), "node_seed_failed")
	must(store.Transact(ctx, user.ScopeID, func(tx catalog.Tx) error {
		for i := 0; i < 500; i++ {
			_, e := tx.Create(ctx, catalog.CreateInput{Name: fmt.Sprintf("容量两跳 %03d", i), Tags: []string{}, Enabled: true, Payload: &ir.Chain{SchemaVersion: 1, Hops: []ir.NodeRef{{NodeID: nodes[i].Metadata.ResourceID}, {NodeID: nodes[i+500].Metadata.ResourceID}}, FailurePolicy: ir.FailClosed}})
			if e != nil {
				return e
			}
		}
		return nil
	}), "chain_seed_failed")
	create := func(name string, payload ir.ResourcePayload) ir.Resource {
		var r ir.Resource
		must(store.Transact(ctx, user.ScopeID, func(tx catalog.Tx) error {
			var e error
			r, e = tx.Create(ctx, catalog.CreateInput{Name: name, Tags: []string{}, Enabled: true, Payload: payload})
			return e
		}), "profile_seed_failed")
		return r
	}
	long := strings.Repeat("a", 55) + "." + strings.Repeat("b", 55) + "." + strings.Repeat("c", 55) + "." + strings.Repeat("d", 55)
	rules := make([]ir.RoutingRule, 2000)
	for i := range rules {
		rules[i] = ir.RoutingRule{Enabled: true, Match: ir.RouteMatch{DomainExact: []string{fmt.Sprintf("r%04d.%s.invalid", i, long), fmt.Sprintf("s%04d.%s.invalid", i, long)}}, Action: ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: nodes[i%500].Metadata.ResourceID}}
	}
	routing := create("容量路由", &ir.RoutingProfile{SchemaVersion: 1, Rules: rules, Final: rules[0].Action, DomainResolutionMode: ir.PreserveDomain})
	dns := create("容量 DNS", &ir.DNSProfile{SchemaVersion: 1, Bootstrap: []ir.BootstrapResolver{{ResolverID: "bootstrap", Kind: ir.DNSLocal}}, Resolvers: []ir.DNSResolver{{ResolverID: "local", Kind: ir.DNSLocal}}, Rules: []ir.DNSRule{}, FinalResolver: "local"})
	var selected ir.Target
	for _, target := range targets {
		if target.CoreFamily == ir.Xray {
			selected = target
		}
	}
	memberIDs := make([]ir.ID, 500)
	for i := range memberIDs {
		memberIDs[i] = nodes[i].Metadata.ResourceID
	}
	profile := ir.SubscriptionProfile{SchemaVersion: 1, Members: ir.SubscriptionMembers{IncludeIDs: memberIDs, ExcludeIDs: []ir.ID{}, Selector: ir.TagSelector{AllTags: []string{}, AnyTags: []string{}, NoneTags: []string{}}}, RoutingProfileID: routing.Metadata.ResourceID, DNSProfileID: dns.Metadata.ResourceID, Targets: []ir.SubscriptionTarget{{Key: selected.Key, CoreBuildID: selected.CoreBuildID, ClientPresetID: selected.ClientPresetID, Format: selected.Format, PolicyOverrides: []ir.PolicyOverride{}}}, PublishPolicy: "strict_all_targets"}
	publicationProfile := create("容量发布", &profile)
	must(store.Transact(ctx, user.ScopeID, func(tx catalog.Tx) error {
		for i := 1; i < 200; i++ {
			copy := profile
			copy.Members.IncludeIDs = []ir.ID{nodes[0].Metadata.ResourceID}
			_, e := tx.Create(ctx, catalog.CreateInput{Name: fmt.Sprintf("容量订阅 %03d", i), Tags: []string{}, Enabled: true, Payload: &copy})
			if e != nil {
				return e
			}
		}
		return nil
	}), "subscription_seed_failed")
	report["seed_seconds"] = time.Since(seedStart).Seconds()
	counts := map[string]int{}
	rows, err = pool.Query(ctx, `SELECT kind,count(*) FROM public.resources WHERE scope_id=$1 AND deleted_at IS NULL GROUP BY kind`, string(user.ScopeID))
	must(err, "dataset_count_failed")
	for rows.Next() {
		var kind string
		var count int
		must(rows.Scan(&kind, &count), "dataset_count_failed")
		counts[kind] = count
	}
	rows.Close()
	report["dataset"] = counts
	require(counts["node"] == 10000 && counts["chain"] == 500 && counts["subscription_profile"] == 200, "dataset_size_mismatch")
	frozen := ir.FrozenInputSpec{SchemaVersion: 1, SnapshotID: jobs.NewID(), ScopeID: user.ScopeID, CatalogRevision: 1, SecurityEpoch: 1, Resources: append(append(slices.Clone(nodes[:500]), routing, dns), selectedPresets...), Targets: targets}
	for _, node := range nodes[:500] {
		frozen.Members = append(frozen.Members, reference(node))
	}
	rr, dr := reference(routing), reference(dns)
	frozen.RoutingProfile = &rr
	frozen.DNSProfile = &dr
	input, err := ir.NewFrozenInput(frozen)
	must(err, "capacity_frozen_input_invalid")
	engine := compiler.New(cores)
	compiles := map[string]any{}
	compileRepeats := 100
	if seconds < 1200 {
		compileRepeats = 5 // Diagnostic preflight only; never accepted as NFR-04.
	}
	for _, target := range targets {
		var times []int64
		var expected string
		var length, nativeOutbounds, nativeRules int
		for i := 0; i < compileRepeats; i++ {
			started := time.Now()
			artifact, _, e := engine.Compile(ctx, input, target)
			took := time.Since(started).Nanoseconds()
			must(e, "capacity_compile_failed")
			digest := sha256.Sum256(artifact.Bytes)
			actual := hex.EncodeToString(digest[:])
			if i == 0 {
				expected = actual
				length = len(artifact.Bytes)
				nativeOutbounds = artifact.OutboundCount
				nativeRules = artifact.RuleCount
			}
			require(actual == expected, "compile_not_deterministic")
			times = append(times, took)
			clear(artifact.Bytes)
		}
		measured := summarize(times)
		compiles[string(target.CoreFamily)] = map[string]any{"duration": measured, "bytes": length, "artifact_sha256": expected, "subject_outbounds": 500, "native_outbounds": nativeOutbounds, "native_rules": nativeRules, "identical_repeats": compileRepeats}
		report["compile"] = compiles
		require(measured.P95 <= 3000, "compile_p95_exceeded")
		fmt.Println("PASS: capacity-compile-" + string(target.CoreFamily))
	}
	actor := subscriptions.Actor{ScopeID: user.ScopeID, ID: user.ID, Key: "capacity-compile"}
	batch, err := publications.Compile(ctx, actor, publicationProfile.Metadata.ResourceID, publicationProfile.Metadata.Revision, subscriptions.CompileRequest{TargetKeys: []string{selected.Key}})
	must(err, "publication_freeze_failed")
	deadline := time.Now().Add(90 * time.Second)
	for {
		batch, err = publications.GetBatch(ctx, actor, batch.BatchID)
		must(err, "publication_read_failed")
		if batch.State == "ready" {
			break
		}
		report["publication_validation_state"] = batch.State
		require(batch.State == "queued" || batch.State == "compiling" || batch.State == "validating", "real_publication_validation_failed")
		require(time.Now().Before(deadline), "publication_validation_deadline")
		time.Sleep(200 * time.Millisecond)
	}
	request := subscriptions.PublishRequest{BatchID: batch.BatchID, ExpectedGeneration: 0, EffectivePreviewHash: batch.EffectivePreviewHash}
	request.Confirmation.Acknowledged = true
	_, err = publications.Publish(ctx, actor, publicationProfile.Metadata.ResourceID, publicationProfile.Metadata.Revision, request)
	must(err, "publication_commit_failed")
	actor.Key = "capacity-read-token"
	issued, err := publications.IssueToken(ctx, actor, publicationProfile.Metadata.ResourceID, subscriptions.TokenRequest{Name: "容量验收", AllowedTargets: []string{selected.Key}})
	must(err, "token_issue_failed")
	download, err := publications.Download(ctx, issued.Token, selected.Key)
	must(err, "published_read_failed")
	size := len(download.Bytes)
	require(size >= 1<<20, "published_fixture_smaller_than_one_mib")
	digest := sha256.Sum256(download.Bytes)
	clear(download.Bytes)
	hash := hex.EncodeToString(digest[:])
	report["publication"] = map[string]any{"bytes": size, "sha256": hash, "real_kernel_validation": true, "target": string(selected.CoreFamily)}
	fmt.Println("PASS: capacity-dataset-and-real-publication")
	management := load(c, "management", 10, time.Duration(seconds)*time.Second, "", 0, "")
	report["management"] = management
	require(management.Elapsed >= float64(seconds) && management.Errors == 0 && management.Latency.P95 <= 500, "management_baseline_failed")
	readSeconds := min(seconds, 120)
	subscription := load(c, "subscription", 50, time.Duration(readSeconds)*time.Second, "/s/"+issued.Token+"/"+selected.Key, size, hash)
	report["subscription"] = subscription
	require(subscription.Elapsed >= float64(readSeconds) && subscription.Errors == 0 && subscription.TTFB.P95 <= 500, "subscription_baseline_failed")
	report["result"] = "pass"
	fmt.Println("PASS: capacity-measurements")
}
