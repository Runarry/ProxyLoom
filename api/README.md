# HTTP contract boundary

`openapi.yaml` is the full P0 OpenAPI 3.1 contract. Five authentication operations are implemented; the remaining operations on the
57 paths remain `contract-only`. `fixtures/api/implemented-routes.json` is checked
against the live HTTP route table. API implementation does not assert kernel capability.

Management uses its session Cookie. Runner operations belong to a separate mTLS
listener. Subscription retrieval requires its independent path token, live
database authorization and publication safety checks. OpenAPI has no path
location for an apiKey security scheme; the mandatory token parameter and
`x-token-authentication` describe that boundary without inventing an accepted
header or inheriting management Cookie authorization.

API `Revision` is a positive canonical decimal string and `Counter` is a
nonnegative canonical decimal string, both bounded by 9223372036854775807.
This includes revision, security/catalog epochs, generation, lease sequence and
SSE event sequence. Internal catalog/IR values remain int64. ETags retain the
strong `"r<N>"` syntax; Last-Event-ID contains the unquoted decimal sequence.
Bounded attempts, limits and measurements remain JSON numbers.

`internal/apicontract` validates requests against the embedded document and
rejects unknown/duplicate fields, trailing JSON, invalid Unicode, excessive
size/depth and undocumented null values. A schema error never reaches the client
as a library error. Requests and internal IR may contain secrets; ordinary read
DTOs have distinct, concrete types with configured-state booleans, including
REALITY fields. API responses never serialize an `ir.Resource`.

JSON number literals are bounded to 128 bytes and exponent magnitude 308 before
Schema validation. Canonical request JSON normalizes equal decimal values
exactly (`1`, `1.0`, `1e0`; `0.5`, `0.50`, `5e-1`) without float64 conversion.

`NodeCreateRequest.Input`, `NodePatchRequest.Merge` and
`ChainCreateRequest.Input` return catalog inputs. Secret patches preserve
absent/null/string for every authentication secret. Merge validates the full
result; clearing required authentication produces 422. API metadata IDs and
revisions remain server-owned. A valid If-Match must still be compared in the
same database transaction as the mutation.

Idempotency helpers validate authenticated metadata and canonical request bytes,
then delegate to the catalog repository's persistent transaction API. They do
not maintain a local response cache. Only typed, secret-free receipt metadata
is replayable; a request's canonical bytes must not be logged or persisted as
plaintext. Every replay requires the handler's current authorization checks.

Generate TypeScript with `pnpm generate:api` in `proxyloom-web`; verify with
`pnpm check:api`. The exact 7.9.1 generator reads the local document and rejects
external schema references. Drift checks rebuild in a fresh `.cache` directory
and compare without changing the checked-in output. Go schema/DTO checks are
`go test -mod=readonly ./api ./internal/apicontract`; persistent behavior is
covered separately by PostgreSQL integration checks.

T-006 authentication uses HttpOnly/Secure/SameSite=Lax cookies, exact configured
Origin checks and session-bound X-CSRF-Token on authenticated writes. Setup/login
require Origin and JSON without a pre-existing session token. The explicit HTTP
loopback development exception omits Secure only. CurrentUser carries csrf_token;
it never carries the opaque session ID. New administrator passwords are 12–1024
UTF-8 bytes; usernames are exact lowercase ASCII identifiers. See
`docs/m1-identity-contract.md` and ADR-0008 for initialization/reset lifecycle.
