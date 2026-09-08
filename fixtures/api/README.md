# API contract fixtures

All identifiers, domains and credentials are synthetic. `manifest.json` drives
independent validation against the actual embedded `api/openapi.yaml` components.
`routes.json` enumerates SDD §10 operations individually, including internal and
subscription surfaces. These are contracts, not implemented business handlers.

Go tests also encode real request/read DTOs and validate them against those same
schemas, check every authentication branch and nested REALITY redaction, strict
JSON, status/precondition distinctions, signed cursor bindings, metadata-only
idempotency receipts, and lossless decimal int64 counters. Parent integration
tests cover persistent idempotency in PostgreSQL; these fixtures do not replace it.
