# Isolation fixtures (T-053)

Rebuildable, test-only Trojan TLS proxies and an HTTP probe target. They do not use personal nodes and are not a production default.

- Library: `internal/isolation`
- Process entry: `proxyloom-fixtures` (not copied into API or Runner images)
- Compose overlay: `deploy/compose.isolation.yaml` (not included by `deploy/compose.dev.yaml`)

`go test ./internal/isolation` starts A, B and the HTTP target on loopback aliases, proves A→B reaches the target, proves a direct client to B is rejected, and proves stopping A does not produce a direct success.

The library tests generate certificates in-process. Compose services load reusable certificate files; they do not create a new CA on startup. All certificates carry the organization `ProxyLoom TEST ONLY`, and passwords are synthetic `EXAMPLE_ONLY_*` values.

## Rebuild and start Compose

Run from the repository root:

```text
go run ./proxyloom-fixtures init-certs
docker compose -f deploy/compose.isolation.yaml config --quiet
docker compose -f deploy/compose.isolation.yaml up --build -d
```

`init-certs` defaults to `.cache/isolation/certs`; `--out <directory>` selects another output directory. It refuses an existing directory, including an empty one, so it cannot silently rotate trust on restart. The default directory is excluded from Git and Docker build contexts. If using another directory, update the Compose bind sources and keep the generated material out of source control.

The output contains `ca.pem`, `a.pem`, `a-key.pem`, `b.pem`, and `b-key.pem`. The CA private key is never written. Directories are traversable and these synthetic test files are readable by the non-root container UID 10003. These permissions are for test fixtures only. Each proxy mounts only its own certificate and key, read-only; missing bind sources are not automatically created.

Clients must trust `ca.pem` and use `a.proxyloom.test` / `b.proxyloom.test` as SNI. Do not disable certificate verification. Restarting A or B reuses these files. For an intentional rotation, stop the stack, generate into a new directory, then update both server mounts and client trust together.

The standalone Compose network is internal, with subnet `172.30.253.0/24`: A is `172.30.253.10`, B is `172.30.253.20`, and the target is `172.30.253.30`. A/B have their corresponding `*.proxyloom.test` DNS aliases and listen on `8443` so the non-root UID 10003 can bind without extra capabilities; the HTTP target is reachable as `target:8080`. Clients use `a.proxyloom.test:8443` and `b.proxyloom.test:8443`. If the subnet overlaps another local network, adjust the subnet, all static addresses and B's allow-list together before startup.

B requires `PROXYLOOM_ISOLATION_ALLOW_FROM`, set to A's fixed address. An absent allow-list is permitted for A; an explicitly configured empty list, an invalid IP, or an empty comma-separated item fails startup. A/B also require `PROXYLOOM_ISOLATION_CERT_FILE` and `PROXYLOOM_ISOLATION_KEY_FILE`; missing or mismatched files fail startup without generating replacement certificates.

## Verification boundary

```text
go test -mod=readonly -count=1 ./internal/isolation ./internal/capability ./proxyloom-fixtures
```

Go regressions cover a single TLS write containing the Trojan header and application payload, fragmented writes, a payload larger than the read buffer, the existing chain/no-bypass checks, strict startup configuration and exported certificate trust. These are local-process checks.

Container smoke is `node scripts/verify-isolation-compose.mjs`. It starts this Compose project, attaches a probe on the internal network, and checks shared-CA trust, A→B success, direct B rejection with the correct password (`forbidden_source`), and failure after stopping A. It does not mark any capability `verified` or complete G0.

Real Xray/sing-box/Mihomo clients against these fixtures are `node scripts/verify-live-chain.mjs` (T-028). That runner installs the test CA into an ephemeral Linux container's system trust store; it does not disable TLS verification and does not inject a custom CA field into compiled configs.

Private-network exceptions exist only in this test package and the isolation Compose file. Do not copy them into production or development defaults.
