# T-048 source-window independent acceptance — 2026-09-10

## Scope and code state

This acceptance covers the remote-source management slice of T-048 through the
built Vue application, the real Go HTTP handler, the API worker, and an isolated
PostgreSQL database. It checks the manual preview boundary, selected atomic commit,
source binding, typed local overrides, source-value restore, safe-update replay
rejection, candidate pagination, 409/412 draft retention, secret-preserving source
editing, and reauthenticated Base64 node export.

- Git HEAD: `29e29c82f99a50e3ecb2fbe6343bfb6c1310246c` plus the shared uncommitted source/policy working tree.
- Exact source state: [source-manifest.json](source-manifest.json), 343 files, SHA-256 `40DA10BEDA41C2D2D64BB6061BDC6255C51AE9906BA5B27C260693853F3085AA`.
- The independently generated manifests immediately before and after the successful run had that same SHA-256.
- Browser harness SHA-256: `92FD5B3399D5B266259EEE21AC44ADF1521B6B8A852BB6D2EE274A8680B90FB7`.
- Playwright spec SHA-256: `10A1DD56597F0F1E40369197ED14508EE1D84D8ED4C96E8E6C53364D27F204DC`.
- Built web `index.html` SHA-256: `9D9A07491E051DB69501F159DB26A2E2D0A645A77DBD537A9BAC7CAB5A3ADAD0`.
- Environment: Windows amd64, Go 1.26.0, Node 24.6.0, pnpm 10.33.2,
  Playwright 1.58.2 with installed Chrome headlessly, locked PostgreSQL 17.6 image.
- Database container: `proxyloom-acceptance-9b376c49-4301-4bb2-8de4-78f55f1f3b3f`.
  The acceptance script created separate runtime/migration/admin roles and the Go
  test created a unique database inside it. Credentials and keys were generated
  under ignored `.cache/` storage and were not copied into evidence.
- The source fixture was an in-process loopback HTTP server. Only the test harness
  supplied `127.0.0.0/8` to its own `safefetch.Client`; production construction and
  blocked-address behavior were unchanged.

## Results

| Status | Check | Command / operation | Result |
| --- | --- | --- | --- |
| PASS | Production web build | `pnpm build` in `proxyloom-web` | Vue typecheck passed; Vite transformed 74 modules and built the production assets. |
| PASS | Real source browser acceptance | `PROXYLOOM_SOURCE_WINDOW_BROWSER_TEST=true`, disposable DSN file variables, `PROXYLOOM_E2E_BROWSER=chrome`, then `go test ./internal/storage -run '^TestSourceWindowBrowserAcceptance$' -count=1 -timeout=8m -v` | Playwright 4/4 passed in 21.2 s; the complete Go harness passed in 23.18 s. |
| PASS | Manual source preview and commit | First browser case | The source form defaulted to `manual`. Refresh produced a source-linked immutable preview. Node search returned zero before confirmation. The selected create request carried `If-Match`, an idempotency key and `source_revision`; commit created one bound node. |
| PASS | Stable identity, override and restore | First browser case | A second upstream response kept explicit `external_key` while changing name and port. The preview showed two upstream changes but only one effective change because the local name override remained. The update decision carried the stable node ID and `expected_binding_revision`; the node kept its ID and local name while the endpoint advanced. Restoring `/name` applied the current upstream name and removed the override marker. |
| PASS | Safe updates cannot be applied twice | Second browser case | The safe-update refresh created and bound the node automatically. Its candidate was marked `已自动应用 · 只读`, its selector and zero-item commit were disabled, and a direct attempted `create` replay against the same real preview returned HTTP 409 without another node. |
| PASS | Candidate pagination and selection | Second browser case | A real source response produced 205 distinct candidates. Page 1 contained 200 and page 2 contained five. Selecting each page retained exactly 205 choices after returning to page 1. |
| PASS | HTTP 409 draft/selection retention | Third browser case | A concurrent source PATCH superseded the selected preview. The stale commit returned HTTP 409, the UI displayed `预览或节点修订冲突 · 选择已保留`, the selected count remained one, and rereading the batch reported `superseded`. |
| PASS | HTTP 412 and source-secret boundary | Third browser case | A bearer source exposed only redacted status. Edit controls loaded source URL and bearer token in `keep` mode. A concurrent source PATCH made the browser draft stale; the stale PATCH returned 412, retained the complete local name draft, and omitted the source object, URL and bearer token. Page text and both browser storage areas contained no token. |
| PASS | Reauthenticated Base64 export | Fourth browser case | A selected synthetic secret-bearing Shadowsocks node opened the reauthentication dialog before any export request. After password reauthentication, the request selected `base64_uri_list`, downloaded `nodes-base64.txt`, round-tripped strict standard Base64, decoded to an `ss://` URI, and preserved the credential through SIP002 Base64URL userinfo. The secret was absent from DOM and browser storage; the downloaded test artifact was explicitly deleted. |
| PASS | Normal-suite gate | `go test ./internal/storage -run '^TestSourceWindowBrowserAcceptance$' -count=1 -v` without the opt-in variable | The reusable real-browser harness skipped by default and the package command passed, so ordinary unit runs do not unexpectedly start browsers or PostgreSQL acceptance work. |
| PASS | Disposable environment cleanup | `node scripts/acceptance-db.mjs stop 9b376c49-4301-4bb2-8de4-78f55f1f3b3f` | The ownership-checked script removed the exact container and network. Its final state records both ownership flags as false. |
| NOT RUN | T-019 policy-group browser flow | No policy-group UI is part of this window | Backend/API acceptance belongs to the parent's real-PostgreSQL verification. This report does not infer T-019 behavior from source UI results. |

## Earlier diagnostic runs

The first Edge run passed the safe-update/pagination and conflict/secret cases, then
reported two test-harness problems: the preview helper followed the prior committed
preview before the new refresh updated the link, and an exact accessible-name locator
did not account for the server-address hint. The helper now waits for a changed preview
URL and the locator uses the actual accessible name.

The next Chrome run passed the first three cases. Its export assertion incorrectly
expected the Shadowsocks password to appear literally in the URI. SIP002 encodes
`method:password` in Base64URL userinfo. The final assertion decodes that userinfo and
compares only a boolean result, so failure output cannot disclose the synthetic value.
The complete suite was rerun after both test corrections.

During preparation, independent review found that URI/VMess `external_key` metadata
was retained but then rejected by the parser's unknown-field allowlist. That would
have made stable source identity unavailable when connection fields changed. The
parent assigned the application fix before final verification. The stable manifest
contains the validated metadata handling, and the final real-browser update proved
the same bound node ID survived an endpoint change.

## Existing evidence reviewed and limits

`docs/evidence/T-017/acceptance.md` covers two-hop chain CRUD and explicitly excludes
orchestration UI. `docs/evidence/T-018/acceptance.md` covers dependency expansion for
chains and explicitly excludes policy/routing/DNS expansion. They are useful
prerequisite context but do not establish the new T-019 policy-group behavior.

Expired-preview and newer-refresh rejection have real backend coverage in the parent
source-preview PostgreSQL run. This browser suite exercised the equivalent source-edit
supersession state and HTTP 409 path; it did not alter database timestamps to repeat
the already-covered expiry case. Source network isolation, redirect, timeout and size
limits remain the SafeFetcher/backend suites' responsibility.

## Files added by this acceptance

- `internal/storage/source_window_browser_test.go`
- `proxyloom-web/tests/e2e/source-window.spec.ts`
- `docs/evidence/T-048/source-window/source-manifest.json`
- `docs/evidence/T-048/source-window/independent-acceptance.md`
- ignored `.cache/source-window-acceptance/README.md`
- ignored `.cache/source-window-acceptance/source-manifest.mjs`

No application code, dependency, lockfile, migration, or project configuration was
modified by this acceptance agent.
