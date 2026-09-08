# T-047 independent acceptance — 2026-09-08

## Scope and code state

Verified the M1 Vue shell, authentication flow, generated API types, error presentation, keyboard/mobile behavior, and browser-only secret handling against a real locally served API where applicable.

- Git base: `ae456a293985eb34da78c3bd97fc54ef386bcd82` with the shared M1 working tree.
- API executable SHA-256: `b0704a67062fa44af8cfc727c3d1834ed021127a27a3f9f0d4576689bb932d17`.
- Built web `index.html` SHA-256: `07c2f7d4c6a605d3f1917838e40f0d4dceeda6f06e73de93c9a2540902d3bb01`.
- Real E2E spec SHA-256 after the final UI-delete addition: `fbe462f772d897f0bc899142c53cdbcc85851ccd545a57903e888d6e8d0545d4`.
- Environment: Windows amd64, Go 1.26.0, Node 24.6.0, pnpm 10.33.2, Playwright 1.58.2 using installed Chrome headlessly, real API on loopback HTTP development mode, disposable PostgreSQL container `proxyloom-acceptance-2d38fd4e-f03b-4d2f-9479-8a930f5426e7`.
- Runtime credentials and keys were freshly generated under ignored `.cache/m1-ui/`; no values were written to evidence.

## Results

| Status | Check | Command / operation | Result |
| --- | --- | --- | --- |
| PASS | Unit-level secret/form semantics | `pnpm test` in `proxyloom-web` | 7/7 passed in 219 ms. Covers keep/replace/clear, protocol switching, hidden values, mask rejection, cleanup, optional TLS clears, and required REALITY fields. |
| PASS | Production build and typecheck | `pnpm build` | Vue typecheck and Vite production build passed; 61 modules transformed. |
| PASS | OpenAPI-generated type consistency | `pnpm check:api` | `PASS: API types match local OpenAPI regeneration.` |
| PASS | Browser UI behavior | `PROXYLOOM_E2E_BROWSER=chrome; pnpm test:ui` | 10/10 passed in 12.1 s. Covers outage recovery, hidden-field switching, 412 draft retention, import conflict/limits, reauthentication failure, Escape focus restoration, mobile width, long names, Chinese errors, and keyboard navigation. These tests use controlled API responses. |
| PASS | Real browser/auth/API integration | `pnpm test:e2e` with the loopback API and synthetic administrator environment | 6/6 passed in 33.6 s after rebuilding the API. Real setup had already been consumed in the preserved database, so this final run covered login, reload restore, logout, session cleanup, and all business flows without mocks. |
| PASS | Setup is closed after initialization | Replayed `POST /api/v1/setup` using the consumed test-only setup token | HTTP 409 `STATE_CONFLICT`; no second administrator was created. The original fresh-database setup returned HTTP 201 earlier in the same preserved environment. |
| PASS | Browser persistence boundary | Assertions in the real suite | `localStorage.length === 0` and `sessionStorage.length === 0` after node creation, reveal, and imports. Revealed material disappeared when closed; retained secrets were omitted from edit requests. |

## Corrected test issues

- An initial setup/logout run produced HTTP 503 because the test navigated immediately after clicking Logout and canceled the in-flight request. Awaiting the login heading fixed the test; an awaited repeat returned HTTP 200 followed by `/auth/me` HTTP 401.
- Early protocol-selection timeouts came from an ambiguous label locator that never performed the action. Role-based combobox locators fixed the harness.
- Six fresh logins plus one reauthentication exceeded the product's five-attempt account window. The final harness reuses only HttpOnly cookies in worker memory, performs two logins plus one reauthentication, writes no storage-state file, and revokes the cached session in teardown.

## Files added by independent acceptance

- `scripts/verify-m1-ui.mjs`
- `docs/evidence/T-047/independent-acceptance.md`
- `docs/evidence/T-048/independent-acceptance.md`

No application code, dependency, lockfile, or project configuration was changed by the acceptance agent.
