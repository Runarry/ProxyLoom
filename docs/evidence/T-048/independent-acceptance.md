# T-048 node/import UI independent acceptance — 2026-09-08

## Scope and acceptance criteria

Verified the authorized T-048 node/local-import slice through installed Chrome connected to the real Go API and disposable PostgreSQL database. The checks cover six node forms, redacted reads, secret-preserving edits, optional TLS clears, server-side clone, immutable revisions, references, batch tag/disable/enable, UI soft-delete confirmation, real stale-write handling, import preview isolation, mixed diagnostics, pagination, selected atomic commit, Base64 file import, expiry, and database-loss behavior.

The code state and environment are the same as `docs/evidence/T-047/independent-acceptance.md`. The independent fault verifier SHA-256 is `538e1c6b18d0e548258793f86310c60e84879066814bb628e5fb87506e6cf09e`.

## Results

| Status | Check | Command / operation | Result |
| --- | --- | --- | --- |
| PASS | Six forms and node lifecycle on real API | Final `pnpm test:e2e` plus focused `pnpm test:e2e -- --grep "six protocol forms"` after the delete addition | Shadowsocks, VMess, VLESS, Trojan, SOCKS5, and HTTP created successfully. Edit omitted retained secret material. Trojan optional ALPN/fingerprint were explicitly cleared and absent on reread. Clone used a new ID, revisions returned two immutable entries, references were empty, seven nodes were tagged/disabled/enabled in batch, and the clone was deleted through the UI after exact-name confirmation; subsequent GET returned 404. Focused final case passed 1/1 in 5.6 s. |
| PASS | Real stale edit / AC-06 | Final real E2E | A concurrent API patch advanced the node, the browser's stale PATCH returned 412, the complete local name draft remained, comparison loaded revision 2, and retry required explicit acknowledgement before saving. |
| PASS | Secret reveal | Final real E2E | Recent password reauthentication was required; revealed material matched the synthetic value, was absent from browser storage, and was removed from the DOM on close. |
| PASS | Mixed paged import / AC-02 and AC-03 slice | Final real E2E | Previewed 205 valid unique items plus a duplicate, malformed line, and unsupported protocol. Node search returned zero before confirmation. Page 1 contained 200 candidates and page 2 contained 8; diagnostics identified the duplicate and two invalid entries. Cross-page selection retained exactly 205 valid choices. The commit sent If-Match and an idempotency key, created 205 nodes atomically, and cleanup soft-deleted every synthetic result. |
| PASS | File/Base64 import | Final real E2E | Explicit Base64 URI-list format accepted a synthetic local text file, the redacted preview survived reload, and the one selected candidate committed successfully. |
| PASS | Expired preview | `scripts/verify-m1-ui.mjs` against a row-specific generated batch | The verifier validated the generated UUID, moved only that batch past retention through the admin connection, reloaded it in Chrome, observed `预览已过期`, and confirmed zero candidate rows remained. |
| PASS | Database loss and recovery | Final `scripts/verify-m1-ui.mjs` run, 17.18 s | Disconnecting only the owned container network caused authenticated node listing to return HTTP 503 `SERVICE_UNAVAILABLE` with no `data`; the UI displayed `服务暂时不可用，请稍后重试。`. The `finally` block reconnected the network, preserved the published port, and verified `/readyz` 200 plus authenticated node-list 200 recovery. Output: `{"expired_preview":"PASS","management_api_unavailable":"PASS","ui_error":"PASS","database_restored":"PASS"}`. |
| PASS | Client selection ceiling | `pnpm test:ui` | Selection across 25 controlled pages stopped at 5,000, refused a 5,001st item, and retained the existing selection. |
| REUSED | Server 5,000-decision boundary | Parent/import owner reported `TestImportsHTTP5000DecisionCommitAndPreconditions` and real PostgreSQL import coverage passed | Not independently rerun by this browser-focused assignment. The real browser commit used 205 selected candidates across two pages. |

## Defect reproduction and resolution

The first hard database-network-loss run on the pre-fix executable left authenticated `/api/v1/nodes` without any response for more than 30 seconds, so the UI could not display a safe error. This was a product availability defect, distinct from the test-harness issues recorded under T-047.

The parent added bounded management request contexts (10 seconds, with longer explicit import-commit and SSE budgets) and increased the HTTP write deadline accordingly. After rebuilding the API, the identical owned-network disconnect returned the safe 503 and the UI/recovery assertions passed. No cached node data or secrets were returned during either run.

## Limits

- Remote source refresh, override markers, and source identity are outside the authorized local-import slice and were not run.
- A 5,000-item commit was not driven through Chrome; the browser exercised cross-page atomic commit with 205 items, the UI ceiling with 5,001 controlled candidates, and reused the backend owner's reported 5,000-decision evidence.
- Full subscription publication and old-publication revocation remain later milestones and were not inferred from node disable/delete behavior.
