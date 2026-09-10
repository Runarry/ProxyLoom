# T-029 M1 compiler mapping and native execution

Execution date: 2026-09-10. Adapter: `0.2.0-m1-orchestration`.

The scoped compiler and adapter implementation is complete. The final native run returned exit 0 and checked an unchanged source manifest. See `native-compiler-source.json` for every captured source hash, the Linux test binary hash, and the pinned container image; the emitted configurations are identified by SHA-256 in `native-config-matrix.json`. DNS-specific mappings and executions are recorded separately by the DNS suite. Broader project and integration checks are owned by the parent acceptance run.

## Verified commands

- `go test ./internal/compiler ./internal/adapter/...` — PASS, including deterministic native goldens, exact unsupported-field diagnostics, target overrides, complete frozen Plan pins, group member isolation, pre-expansion budgets, native expansion boundaries of exactly 2,000 outbounds and 20,000 rules, and the 10 MiB final artifact limit. Native counts include emitted selectors/balancers, builtins, final routes and DNS dispatch rules; fixed aliases add no native group. Native-only tests skip outside their explicitly enabled Linux fixture.
- `go vet ./internal/compiler ./internal/adapter/...` — PASS.
- `node scripts/verify-m1-native.mjs` — PASS in pinned Linux/amd64 Docker with `--network none`, read-only fixture/core mounts, and no application credentials mounted. This script builds the compiler tests, verifies each core SHA-256, runs real native checks and runtime traffic, and rejects source drift during execution. Docker was run with the required escalation.
- `gofmt -l internal/compiler internal/adapter` — empty; `git diff --check -- internal/compiler internal/adapter compat fixtures/compiler` — PASS.

## Configuration matrix

All 10 P0 candidates have positive compiler goldens and negative compiler diagnostics. The real kernel matrix accepted 72 configurations and rejected 72 copies with deliberately invalid outbound protocols. Of these, 60 configurations cover the protocol candidates and their explicitly tested alternatives; the remaining 12 cover orchestration, policies, and the local-bootstrap chain. No raw credentials are stored in execution reports.

| Candidate | Explicit alternatives in native checks | Three-core configurations |
| --- | --- | ---: |
| P0-SS-TCP | AES-128-GCM, AES-256-GCM, ChaCha20-IETF-Poly1305 | 9 |
| P0-VMESS-TCP | none and TLS | 6 |
| P0-VMESS-WS | none and TLS, explicit path and Host | 6 |
| P0-VLESS-TCP | none and TLS | 6 |
| P0-VLESS-WS | none and TLS, explicit path and Host | 6 |
| P0-VLESS-REALITY | native TCP, public key, short ID, fingerprint, Vision flow | 3 |
| P0-TROJAN-TCP | TLS, SNI, ALPN, certificate verification | 3 |
| P0-TROJAN-WS | WebSocket and TLS | 3 |
| P0-SOCKS5-TCP | no authentication and username/password | 6 |
| P0-HTTP-TCP | none/TLS, each with and without authentication | 12 |

This records native configuration acceptance, not remote interoperability for every protocol or a parameter Cartesian product. The configurations use reserved fixture endpoints. Broad capability and client states remain `unverified`; arm64 and desktop/mobile imports were not executed.

## Runtime results

- `native-manual-selection.json`: sing-box and Mihomo start with the selected default, accept a real controller PUT to change the selected member, and move subsequent SOCKS traffic to that member. Closing the selected relay causes failure without traffic through the other relay or direct to the endpoint. CORS allows the exact loopback controller origin and denies an unrelated origin. No external UI is configured.
- `native-round-robin.json`: Xray and Mihomo carry six real requests as three through each member. Closing both relays causes failure without endpoint bypass. Mihomo health probes reach only the local TLS fixture; Xray health-disabled configuration performs no probes.
- `native-latency-best.json`: Mihomo selects the fast member instead of the configured slow default after explicit local TLS health checks (1,000 ms interval, 900 ms timeout, 5 ms tolerance). Three requests use the fast member; closing both members fails without direct endpoint traffic.
- `native-routing.json`: all three cores preserve different-field AND, multiple-value OR, rule order, explicit final reject, suffix apex matching, and the dot boundary between domains. Positive requests reach the expected relays; conflicting fields and unmatched domains fail.
- `native-ip-routing.md`: final follow-up covers 60 requests across supported IP-resolution modes and three no-IP controls. It preserves the native sing-box NXDOMAIN counterexample and records the user's explicit scope approval: sing-box rejects resolve-for-IP mode with effective IP conditions; no fallback or mode substitution is emitted.
- `native-chain-bootstrap.json`: Xray resolves the H2 `localhost` name before sending its destination through H1; the relay observes an IP address, both hops carry the request, and closing H2 fails without bypass.

## Exact limitations and fixes

- sing-box `round_robin` returns `CAPABILITY_UNSUPPORTED`; it is never replaced with urltest. Manual selection requires the approved loopback controller preset. Xray manual selection remains unsupported.
- Enabled health timeout semantics are not equivalent in Xray/sing-box; they return resource ID, target key and `/payload/health_check/timeout_ms`. Mihomo round-robin requires health enabled because omitting it activates the native public default. Mihomo intervals must be whole seconds; round-robin tolerance must be zero. Mihomo latency-best maps its explicit URL, interval, timeout and tolerance.
- Xray round-robin with a fail-closed fallback requires the observatory feature. The generated empty subject selector satisfies that dependency without starting a probe loop. This follows the pinned [balancer dependency](https://github.com/XTLS/Xray-core/blob/v26.3.27/app/router/balancing.go) and [observer start condition](https://github.com/XTLS/Xray-core/blob/v26.3.27/app/observatory/observer.go).
- Xray H2 hostnames with an explicit DNS profile use `ForceIP` only where bootstrap and business DNS are all local and therefore equivalent. A differing business resolver produces an exact H2 `/endpoint/host` diagnostic. Xray's pinned [socket dialer](https://github.com/XTLS/Xray-core/blob/v26.3.27/transport/internet/dialer.go) otherwise forwards the domain through H1, and its ForceIP lookup uses global business DNS. M0 configurations without a DNS profile keep their existing behavior.
- The first native attempt identified and fixed three concrete native differences: Xray's observatory dependency, sing-box's required resolver for explicit direct, and Mihomo's simple IP-CIDR action/`no-resolve` ordering. Initial output is preserved in `native-compiler-initial-failures.txt`; the final successful output is `native-compiler-output.txt`.
- sing-box explicit direct resolves at that action's routing position before dialing, so business DNS rule order is retained without making earlier IP rules resolve under `preserve_domain`. Xray direct uses `ForceIP` with an explicit DNS profile. DNS transport and bootstrap limitations are covered by the separate DNS evidence.

No subscription publication, release, remote node test, commit, or push was performed by this scoped task.
