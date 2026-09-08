// Generated from api/openapi.yaml by openapi-typescript 7.9.1. Do not edit.
export interface paths {
    "/api/v1/audit-events": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List bounded audit metadata without request bodies or secrets */
        get: operations["listAuditEvents"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/auth/login": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Authenticate and rotate the management session
         * @description Requires exact Origin and application/json; no existing session or CSRF token required. Login does not establish a recent reauthentication marker.
         */
        post: operations["login"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/auth/logout": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Revoke the current management session
         * @description Requires exact Origin, application/json and X-CSRF-Token. Authentication operations have no resource If-Match precondition.
         */
        post: operations["logout"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/auth/me": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Read the authenticated administrator and recent authentication status */
        get: operations["getCurrentUser"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/auth/reauth": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Establish a recent authentication marker on the current session
         * @description Requires exact Origin, application/json and X-CSRF-Token. The marker expires after five minutes.
         */
        post: operations["reauthenticate"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/capabilities": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * List field constraints, capability state and traceable evidence
         * @description A locked build, supported schema, or successful compilation does not imply a verified capability. Publishing requires the selected combination to be verified.
         */
        get: operations["listCapabilities"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/chains": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List two-node chain resources */
        get: operations["listChains"];
        put?: never;
        /**
         * Create two distinct concrete node hops with the last hop as exit
         * @description References follow node heads in the editing state. Does not modify either node or permit policy-group hops, fixed old revisions, or a direct fallback.
         */
        post: operations["createChain"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/chains/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        /** Read a chain revision */
        get: operations["getChain"];
        put?: never;
        post?: never;
        /** Soft-delete a chain while preserving revisions and references */
        delete: operations["deleteChain"];
        options?: never;
        head?: never;
        /** Replace typed chain fields in a new immutable revision */
        patch: operations["updateChain"];
        trace?: never;
    };
    "/api/v1/client-presets": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List reviewed structured client presets */
        get: operations["listClientPresets"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/compile-batches/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        /**
         * Read safe effective membership, dependencies, output summaries and diagnostics
         * @description Returns no native configuration or complete IR. The server records the preview hash presented to the current actor; that record is checked when publishing.
         */
        get: operations["getCompileBatch"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/cores": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * List pinned registered core builds and exact digests
         * @description There is no binary upload, arbitrary installation or remote executable URL endpoint.
         */
        get: operations["listCores"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/cores/{id}/disable": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Disable a core build and immediately block unsafe dependent publications */
        post: operations["disableCore"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/dns-profiles": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List DNS profiles */
        get: operations["listDNSProfiles"];
        put?: never;
        /** Create separate bootstrap and business resolvers with cycle checks */
        post: operations["createDNSProfile"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/dns-profiles/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        /** Read typed DNS resolvers and ordered domain rules */
        get: operations["getDNSProfile"];
        put?: never;
        post?: never;
        /** Soft-delete a DNS profile */
        delete: operations["deleteDNSProfile"];
        options?: never;
        head?: never;
        /** Update typed DNS fields and recheck resolver and outbound dependencies */
        patch: operations["updateDNSProfile"];
        trace?: never;
    };
    "/api/v1/exports": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Export explicit resource revisions or publication artifacts
         * @description Secret-bearing exports require server-verified recent authentication and an audit record. Source credentials, tokens, master keys and arbitrary native configuration are never accepted as input. No new resource revision is written.
         */
        post: operations["createExport"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/imports": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Create a bounded import preview job on the API Worker */
        post: operations["createImport"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/imports/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        /** Read redacted import candidates and per-entry diagnostics */
        get: operations["getImport"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/imports/{id}/commit": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Commit selected candidates transactionally against the preview revision */
        post: operations["commitImport"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/jobs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List persistent jobs with execution state distinct from verdict */
        get: operations["listJobs"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/jobs/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        /**
         * Read a child job or parent test-batch snapshot
         * @description Passing a batch_id returns TestBatchResponse with aggregate progress and all bounded child summaries. Passing a child job_id returns JobResponse. Successful execution with a failing tested node is state=succeeded and verdict=fail.
         */
        get: operations["getJob"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/jobs/{id}/cancel": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Persist cancellation of a child job or all unfinished children of a batch
         * @description id may be a job_id or batch_id. Returns the updated snapshot; acknowledgement means cancellation requested, not that a running process has already exited. Budget settlement still applies to canceled attempts.
         */
        post: operations["cancelJob"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/jobs/{id}/events": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        /**
         * Replay bounded safe job events over SSE
         * @description Each job has stable strictly increasing seq values; SSE id is that decimal seq. Last-Event-ID replays later events without rerunning work. Retention gaps produce event=snapshot_reset carrying SnapshotResetEvent, then close; clients reread GET /jobs/{id}. A future sequence or malformed ID is 400. event=job_event carries JobEvent. Neither event contains raw stdout, stderr, native configuration or arbitrary event data. SSE is not the persistent job fact source. Heartbeat comments carry no application data.
         */
        get: operations["streamJobEvents"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/nodes": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List nodes using management-only redacted DTOs */
        get: operations["listNodes"];
        put?: never;
        /**
         * Create a typed node at revision 1
         * @description Generates ID, scope, revision and epoch on the server. No If-Match is required for creation.
         */
        post: operations["createNode"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/nodes/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        /** Read a node without returning auth or REALITY secrets */
        get: operations["getNode"];
        put?: never;
        post?: never;
        /**
         * Soft-delete a node and immediately revoke dependent publication access
         * @description Preserves immutable history and references; advances revision and security epoch.
         */
        delete: operations["deleteNode"];
        options?: never;
        head?: never;
        /**
         * Apply a typed node patch and create a new immutable revision
         * @description An absent secret preserves its previous value, null clears it, and a string replaces it. The merged node is validated; clearing a required password, UUID, username or REALITY field returns 422. Empty or masked credentials are invalid. Authentication changes and disabling advance the security epoch; a caller cannot label a security change as ordinary.
         */
        patch: operations["updateNode"];
        trace?: never;
    };
    "/api/v1/nodes/{id}/clone": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Clone the matched node revision with a new stable ID */
        post: operations["cloneNode"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/nodes/{id}/references": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        /** List current active and historical reverse references */
        get: operations["listNodeReferences"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/nodes/{id}/reveal": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Reveal typed secrets once after recent authentication with an audit record
         * @description Requires the matched node revision and server-verified recent authentication. This dedicated response must never be cached, logged or stored for idempotent replay.
         */
        post: operations["revealNodeSecrets"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/nodes/{id}/revisions": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        /**
         * List immutable redacted revisions with their original metadata
         * @description Historical name, tags, enabled and epoch are taken from that revision, never from the current head. History cannot authorize publication of old secrets.
         */
        get: operations["listNodeRevisions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/nodes/batch": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Apply a bounded metadata operation with individual revision preconditions
         * @description Each node has its own precondition and result. A missing item precondition is 428 and a stale one is 412 in that item's result; the batch has no synthetic shared ETag.
         */
        post: operations["updateNodesBatch"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/policy-groups": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List policy groups and compatibility diagnostics */
        get: operations["listPolicyGroups"];
        put?: never;
        /** Create a nonnested policy group of nodes and chains */
        post: operations["createPolicyGroup"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/policy-groups/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        /** Read a typed policy group */
        get: operations["getPolicyGroup"];
        put?: never;
        post?: never;
        /** Soft-delete a policy group and block unsafe dependent publication access */
        delete: operations["deletePolicyGroup"];
        options?: never;
        head?: never;
        /** Update typed strategy fields and validate membership */
        patch: operations["updatePolicyGroup"];
        trace?: never;
    };
    "/api/v1/routing-profiles": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List routing profiles */
        get: operations["listRoutingProfiles"];
        put?: never;
        /** Create ordered first-match rules with an explicit final target */
        post: operations["createRoutingProfile"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/routing-profiles/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        /** Read ordered typed routing rules */
        get: operations["getRoutingProfile"];
        put?: never;
        post?: never;
        /** Soft-delete a routing profile */
        delete: operations["deleteRoutingProfile"];
        options?: never;
        head?: never;
        /** Create a routing revision with whole-array rule replacement */
        patch: operations["updateRoutingProfile"];
        trace?: never;
    };
    "/api/v1/rule-sets": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List inline domain and CIDR rule sets */
        get: operations["listRuleSets"];
        put?: never;
        /** Create a normalized inline rule set and compute its content hash */
        post: operations["createRuleSet"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/rule-sets/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        /** Read normalized entries and immutable content hash */
        get: operations["getRuleSet"];
        put?: never;
        post?: never;
        /** Soft-delete a rule set while preserving frozen publication dependencies */
        delete: operations["deleteRuleSet"];
        options?: never;
        head?: never;
        /** Replace inline rules with safe indexed diagnostics on invalid entries */
        patch: operations["updateRuleSet"];
        trace?: never;
    };
    "/api/v1/setup": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Initialize the administrator once while the system is empty
         * @description Requires the configured one-time setup credential, exact Origin and JSON. Never accepts a scope or resource revision. Disabled after the first successful initialization.
         */
        post: operations["setup"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/sources": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List source metadata without fetch credentials or secret URLs */
        get: operations["listSources"];
        put?: never;
        /** Create a remote source with a constrained fetch policy */
        post: operations["createSource"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/sources/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        /** Read redacted source configuration and latest refresh status */
        get: operations["getSource"];
        put?: never;
        post?: never;
        /** Soft-delete the source and stop new refreshes */
        delete: operations["deleteSource"];
        options?: never;
        head?: never;
        /** Create an immutable source revision with secret-preserving patches */
        patch: operations["updateSource"];
        trace?: never;
    };
    "/api/v1/sources/{id}/refresh": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Enqueue an API Worker refresh with one active lease per source
         * @description Captures the matched source revision; final commit also compares source and binding revisions. Never assigned to a Runner.
         */
        post: operations["refreshSource"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/subscriptions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List subscription profiles and publication heads */
        get: operations["listSubscriptions"];
        put?: never;
        /** Create a subscription profile with explicit target keys */
        post: operations["createSubscription"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/subscriptions/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        /** Read a typed subscription profile */
        get: operations["getSubscription"];
        put?: never;
        post?: never;
        /** Soft-delete the profile and synchronously block new subscription downloads */
        delete: operations["deleteSubscription"];
        options?: never;
        head?: never;
        /** Create a profile revision without mutating existing publication bytes */
        patch: operations["updateSubscription"];
        trace?: never;
    };
    "/api/v1/subscriptions/{id}/compile": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Freeze all enabled targets and enqueue compilation plus native validation
         * @description Compilation is an API Worker job; config_validate is a separate Runner job for each output. No target is published or silently omitted by this operation.
         */
        post: operations["compileSubscription"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/subscriptions/{id}/publications": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        /** List immutable publication records and current safe blocking reasons */
        get: operations["listPublications"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/subscriptions/{id}/publish": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Atomically publish a confirmed ready batch for all enabled targets
         * @description The server verifies the current actor's confirmation for exactly batch_id and effective_preview_hash, never trusts a client-supplied actor ID, and rechecks catalog revision, dependency security epochs, build enablement, native validation and every enabled target. If-Match applies to the subscription profile; expected_generation is a separate publication-head compare-and-swap, with 0 meaning no previous publication. Generation or obsolete batch conflicts return 409; stale profile ETag returns 412.
         */
        post: operations["publishSubscription"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/subscriptions/{id}/rollback": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Create a new publication event from a safe historical publication
         * @description Rechecks current dependency security epochs, enabled builds and current target authorization. Historical credentials cannot be revived. expected_generation is separate from the profile If-Match.
         */
        post: operations["rollbackSubscription"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/subscriptions/{id}/tokens": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        /** List token metadata without token values, verification HMACs or URLs */
        get: operations["listSubscriptionTokens"];
        put?: never;
        /**
         * Issue a target-scoped token and return its secret exactly once
         * @description This creates a token and has no token-resource If-Match. The referenced profile and allowed target keys are validated in the issuance transaction. Replaying the idempotency key returns metadata with replayed=true and no token. A lost first response requires revocation and a new issuance key. Original tokens and raw response bytes are never persisted for replay.
         */
        post: operations["issueSubscriptionToken"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/system/settings": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Read whitelisted quota, concurrency and retention settings */
        get: operations["getSystemSettings"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /**
         * Update a bounded whitelist of operational settings
         * @description Database credentials, master keys, executable paths, shells, arbitrary environment variables and listener authentication are not writable settings.
         */
        patch: operations["updateSystemSettings"];
        trace?: never;
    };
    "/api/v1/test-results": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List traceable results bound to subject revision, core build and execution location */
        get: operations["listTestResults"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/test-targets": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List registered controlled test targets and their safety state */
        get: operations["listTestTargets"];
        put?: never;
        /**
         * Register an administrator-controlled target for safety checks
         * @description Registration alone does not make a target usable. URL, resolution, connection IP, redirects, certificate and response expectations are checked before safety_state becomes approved.
         */
        post: operations["createTestTarget"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/test-targets/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        /** Read a registered test target and safety diagnostics */
        get: operations["getTestTarget"];
        put?: never;
        post?: never;
        /** Disable and soft-delete a target so it cannot be used for new tests */
        delete: operations["deleteTestTarget"];
        options?: never;
        head?: never;
        /** Update target settings and invalidate approval when connection semantics change */
        patch: operations["updateTestTarget"];
        trace?: never;
    };
    "/api/v1/tests": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Freeze subject revisions and reserve budget for a parent batch and child jobs
         * @description Accepts only registered test_target_id for network tests, never a URL. Effective limits cannot exceed system, target or available quota limits. Parent cancellation requests cancellation of every unfinished child; terminal children remain immutable.
         */
        post: operations["createTestBatch"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/tokens/{id}/revoke": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Commit token revocation before acknowledging success
         * @description Requests starting authorization after commit are rejected. Responses whose authorization already completed cannot be withdrawn.
         */
        post: operations["revokeToken"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/internal/v1/jobs/{id}/events": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Submit a bounded typed event under the current fenced lease
         * @description Body job_id must equal path id. event_id deduplicates retries within this attempt; the server assigns stable job-wide seq. Unknown fields, raw stdout and unredacted messages are rejected, and only safe diagnostic summaries are retained.
         */
        post: operations["submitRunnerJobEvent"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/internal/v1/jobs/{id}/heartbeat": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Renew the current attempt and fenced lease and read cancellation state
         * @description Body job_id must equal path id. Identity, attempt, lease_seq, nonterminal state and unexpired lease are checked atomically. A stale lease returns 409 LEASE_LOST; no resource If-Match is used on internal lease operations.
         */
        post: operations["heartbeatRunnerJob"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/internal/v1/jobs/{id}/result": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Atomically finalize the current attempt and settle its budget once
         * @description Body job_id equals path id. Matching identity, attempt, lease_seq and result_hash replays the accepted receipt without another settlement; conflicting hashes or stale leases return 409. Late attempts cannot change a terminal result. The server verifies the canonical safe-result hash and effective limits; it does not trust a submitted hash or byte count to authorize more budget. Execution completion and test verdict are independent: a completed negative probe is succeeded plus fail.
         */
        post: operations["submitRunnerJobResult"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/internal/v1/jobs/lease": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Let the server assign an eligible Runner task and frozen payload
         * @description The request cannot choose job_id, arbitrary URL, shell, path or native configuration. Selection atomically checks certificate registration, exact core digest, type, slot and the attempt's budget reservation. Only config_validate, connectivity and download_throughput are eligible; source_refresh, import_parse and compile remain on the API Worker. No eligible task returns lease=null. Default lease is 30 seconds with 5-second heartbeat; a Runner without a valid renewal stops before expiry.
         */
        post: operations["leaseRunnerJob"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/internal/v1/runners/heartbeat": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Report registered build digests and available slots from a certificate-bound Runner
         * @description runner_id must match the registered client-certificate identity. Self-reported capabilities cannot expand the server registration.
         */
        post: operations["heartbeatRunner"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/s/{token}/{target_key}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Read exact immutable bytes after live token and publication safety checks
         * @description The mandatory path token is independent of management Cookie and Runner mTLS. Primary-database authorization checks token digest, expiry, revocation, auth epoch, allowed target, current profile and dependency security state before any cache access. Database failure returns 503. Invalid, expired, revoked and unauthorized-target tokens all return the same generic 404. An authorized token with no safe publication returns 503. This GET never compiles, runs a kernel, publishes, redirects to direct, serves an empty success configuration or relies on 304/shared cache. Logs use the path template and never record token or full request URI.
         */
        get: operations["getPublishedSubscription"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
}
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
        AcknowledgementResponse: {
            data: {
                /** @constant */
                acknowledged: true;
            };
            request_id: components["schemas"]["RequestID"];
        };
        ALPN: components["schemas"]["ShortText"][];
        /** @description An empty patch array clears optional ALPN; omission preserves the current value. */
        ALPNPatch: components["schemas"]["ShortText"][];
        /** @enum {string} */
        APIWorkerJobType: "source_refresh" | "import_parse" | "compile";
        /** @enum {string} */
        Architecture: "amd64" | "arm64";
        /** @enum {string} */
        AuditAction: "setup" | "login" | "logout" | "reauth" | "create" | "update" | "delete" | "reveal" | "source_refresh" | "import_commit" | "compile" | "publish" | "rollback" | "token_issue" | "token_revoke" | "test_create" | "job_cancel" | "core_disable" | "settings_update" | "secret_rewrap" | "export";
        /** @description Allowlisted metadata only. Never includes original request/response, credential values, token HMACs, ciphertext, native configuration, arbitrary JSON metadata or raw logs. */
        AuditEvent: {
            action: components["schemas"]["AuditAction"];
            actor_id?: components["schemas"]["UUID"];
            /** @enum {string} */
            actor_type: "administrator" | "runner" | "system";
            changed_fields: components["schemas"]["SafeFieldPath"][];
            created_at: components["schemas"]["Timestamp"];
            error_code?: components["schemas"]["ErrorCode"];
            event_id: components["schemas"]["UUID"];
            operation_id?: components["schemas"]["UUID"];
            /** @enum {string} */
            outcome: "success" | "failure";
            request_id: components["schemas"]["RequestID"];
            resource_id?: components["schemas"]["UUID"];
            resource_kind?: components["schemas"]["ResourceKind"];
            revision?: components["schemas"]["Revision"];
        };
        AuditEventListResponse: {
            data: components["schemas"]["AuditEvent"][];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        /** @description UTF-8 password, at most 1024 bytes. New passwords require at least 12 bytes; domain violations return 422. Passwords are never normalized. */
        AuthPassword: string;
        /** @description Exact lowercase ASCII identifier; no implicit trimming or case conversion. */
        AuthUsername: string;
        BooleanFieldConstraint: {
            allowed_values: boolean[];
            field_path: components["schemas"]["SafeFieldPath"];
            /** @constant */
            kind: "boolean";
        };
        /** @description Explicit bootstrap choice; never silently substitutes a public resolver. UDP bootstrap uses a literal address to avoid recursive bootstrap dependencies. */
        BootstrapResolver: components["schemas"]["LocalBootstrapResolver"] | components["schemas"]["UDPBootstrapResolver"];
        BuiltinRef: {
            /** @enum {string} */
            builtin: "direct" | "reject";
            /** @constant */
            type: "builtin";
        };
        Capability: {
            capability_id: components["schemas"]["UUID"];
            constraints: components["schemas"]["FieldConstraint"][];
            diagnostics: components["schemas"]["Diagnostics"];
            evidence: components["schemas"]["CapabilityEvidence"][];
            key: components["schemas"]["CapabilityKey"];
            status: components["schemas"]["CapabilityStatus"];
        };
        CapabilityEvidence: {
            artifact_sha256: components["schemas"]["SHA256"];
            checked_at: components["schemas"]["Timestamp"];
            evidence_id: components["schemas"]["UUID"];
            /** @description Controlled evidence reference; not an arbitrary executable or fetch URL. */
            reference: string;
            /** @enum {string} */
            result: "pass" | "fail" | "inconclusive";
        };
        CapabilityKey: {
            /** @enum {string} */
            chain_position?: "single" | "entry" | "exit";
            client_preset_id: components["schemas"]["UUID"];
            core_build_id: components["schemas"]["UUID"];
            core_family: components["schemas"]["CoreFamily"];
            /** @enum {string} */
            dns_feature?: "local" | "udp" | "https" | "domain_rule";
            feature_set: ("node" | "chain" | "fixed" | "manual_select" | "latency_best" | "round_robin" | "routing" | "dns" | "udp" | "multiplex" | "vision")[];
            platform: components["schemas"]["Platform"];
            protocol?: components["schemas"]["Protocol"];
            /** @constant */
            protocol_variant?: "xtls-rprx-vision";
            /** @enum {string} */
            route_feature?: "domain_exact" | "domain_suffix" | "ip_cidr" | "destination_port" | "network" | "rule_set";
            /** @enum {string} */
            security_mode?: "none" | "tls" | "reality";
            /** @enum {string} */
            transport?: "native_tcp" | "websocket";
            /** @enum {string} */
            udp_requirement?: "unspecified" | "disabled" | "required";
        };
        CapabilityListResponse: {
            data: components["schemas"]["Capability"][];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        /** @enum {string} */
        CapabilityStatus: "unsupported" | "unverified" | "verified";
        CatalogLimits: {
            max_dependency_resources: number;
            max_import_bytes: number;
            max_import_items: number;
            max_targets_per_subscription: number;
        };
        CatalogLimitsPatch: {
            max_dependency_resources?: number;
            max_import_bytes?: number;
            max_import_items?: number;
            max_targets_per_subscription?: number;
        };
        Chain: {
            /** @constant */
            failure_policy: "fail_closed";
            hops: components["schemas"]["ChainHops"];
            schema_version: components["schemas"]["SchemaVersion"];
        };
        ChainCreateRequest: {
            enabled?: boolean;
            /** @constant */
            failure_policy: "fail_closed";
            hops: components["schemas"]["ChainHops"];
            name: components["schemas"]["Name"];
            tags?: components["schemas"]["Tags"];
        };
        /** @description Ordered client perspective: first hop then final exit. Both IDs are distinct, concrete enabled nodes in the same scope. No fixed historical revisions. */
        ChainHops: components["schemas"]["NodeRef"][];
        ChainListResponse: {
            data: components["schemas"]["ChainResource"][];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        ChainPatchRequest: {
            enabled?: boolean;
            /** @constant */
            failure_policy?: "fail_closed";
            hops?: components["schemas"]["ChainHops"];
            name?: components["schemas"]["Name"];
            tags?: components["schemas"]["Tags"];
        };
        ChainReadResponse: {
            data: components["schemas"]["ChainResource"];
            request_id: components["schemas"]["RequestID"];
        };
        ChainResource: {
            chain: components["schemas"]["Chain"];
            metadata: components["schemas"]["ResourceMetadata"];
        };
        /** @description Canonical IPv4 or IPv6 network CIDR. Domain validation additionally parses the address, checks family prefix bounds and host bits. */
        CIDR: string;
        CIDRRuleSetEntry: {
            cidr: components["schemas"]["CIDR"];
            /** @constant */
            kind: "cidr";
        };
        /** @description Whitelist constrained further by the frozen approved preset; cannot add arbitrary native settings, listeners, credentials or scripts. */
        ClientParameterOverride: {
            control_api_enabled?: boolean;
            local_port?: number;
        };
        ClientPreset: {
            control_api: components["schemas"]["ControlAPIPreset"];
            core_family: components["schemas"]["CoreFamily"];
            /** @constant */
            dns_mode: "profile";
            format: components["schemas"]["OutputFormat"];
            /** @enum {string} */
            import_method: "file" | "subscription_url";
            local_listener: components["schemas"]["LocalListener"];
            platform: components["schemas"]["Platform"];
            /** @constant */
            review_status: "approved";
            reviewed_at: components["schemas"]["Timestamp"];
            schema_version: components["schemas"]["SchemaVersion"];
        };
        ClientPresetListResponse: {
            data: components["schemas"]["ClientPresetResource"][];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        ClientPresetResource: {
            metadata: components["schemas"]["ResourceMetadata"];
            preset: components["schemas"]["ClientPreset"];
        };
        /** @description Safe preview only. effective_preview_hash is present once the full effective dependency/target preview is frozen. It identifies exactly what the actor must confirm; no raw credentials, full IR or native output. */
        CompileBatch: {
            batch_id: components["schemas"]["UUID"];
            catalog_revision: components["schemas"]["Revision"];
            created_at: components["schemas"]["Timestamp"];
            dependencies: components["schemas"]["PublicationDependency"][];
            diagnostics: components["schemas"]["Diagnostics"];
            effective_preview_hash?: components["schemas"]["SHA256"];
            job_ids: components["schemas"]["UUID"][];
            outputs: components["schemas"]["CompileOutputSummary"][];
            revision: components["schemas"]["Revision"];
            /** @enum {string} */
            state: "queued" | "compiling" | "validating" | "ready" | "failed" | "obsolete";
            subscription_id: components["schemas"]["UUID"];
            subscription_revision: components["schemas"]["Revision"];
        };
        CompileBatchResponse: {
            data: components["schemas"]["CompileBatch"];
            request_id: components["schemas"]["RequestID"];
        };
        CompileOutputSummary: {
            adapter_version: components["schemas"]["ShortText"];
            artifact_id?: components["schemas"]["UUID"];
            client_preset_id: components["schemas"]["UUID"];
            client_preset_revision: components["schemas"]["Revision"];
            core_build_id: components["schemas"]["UUID"];
            core_build_sha256: components["schemas"]["SHA256"];
            diagnostics: components["schemas"]["Diagnostics"];
            format: components["schemas"]["OutputFormat"];
            /** @enum {string} */
            state: "queued" | "compiling" | "validating" | "ready" | "failed";
            target_key: components["schemas"]["TargetKey"];
            validation_job_id?: components["schemas"]["UUID"];
            /** @enum {string} */
            validation_verdict?: "pass" | "fail" | "inconclusive";
        };
        /** @description The set must equal all enabled profile targets for a publishable strict_all_targets batch. Expected profile revision is supplied only through If-Match. No floating native input or client-supplied frozen metadata. */
        CompileRequest: {
            target_keys: components["schemas"]["TargetKey"][];
        };
        ConnectivityPublishGate: {
            max_age_seconds: number;
            /** @constant */
            required_verdict: "pass";
            /** @enum {string} */
            subject_policy: "all_members" | "all_chains";
            test_target_id: components["schemas"]["UUID"];
        };
        /** @description Reviewed local-only control API. No arbitrary listener, secret or script configuration. */
        ControlAPIPreset: {
            enabled: boolean;
            /** @enum {string} */
            listen?: "127.0.0.1" | "::1";
            port?: number;
        } & unknown;
        CoreBuild: {
            adapter_version: components["schemas"]["ShortText"];
            architecture: components["schemas"]["Architecture"];
            build_sha256: components["schemas"]["SHA256"];
            /** @enum {string} */
            capability_status: "unsupported" | "unverified" | "verified";
            core_build_id: components["schemas"]["UUID"];
            core_family: components["schemas"]["CoreFamily"];
            disable_reason?: components["schemas"]["ShortText"];
            disabled_at?: components["schemas"]["Timestamp"];
            enabled: boolean;
            platform: components["schemas"]["Platform"];
            registered_at: components["schemas"]["Timestamp"];
            revision: components["schemas"]["Revision"];
            version: components["schemas"]["ShortText"];
        };
        /** @enum {string} */
        CoreFamily: "xray" | "sing-box" | "mihomo";
        CoreIdentity: {
            adapter_version: components["schemas"]["ShortText"];
            architecture: components["schemas"]["Architecture"];
            build_sha256: components["schemas"]["SHA256"];
            core_build_id: components["schemas"]["UUID"];
            core_family: components["schemas"]["CoreFamily"];
            platform: components["schemas"]["Platform"];
            version: components["schemas"]["ShortText"];
        };
        CoreListResponse: {
            data: components["schemas"]["CoreBuild"][];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        CoreResponse: {
            data: components["schemas"]["CoreBuild"];
            request_id: components["schemas"]["RequestID"];
        };
        /** @description Canonical nonnegative decimal int64 string, 0..9223372036854775807. No signs or leading zeros. */
        Counter: string;
        CurrentUser: {
            /** @description Session-bound token for X-CSRF-Token. Keep in memory only; never an authorization substitute. */
            csrf_token: string;
            recent_authentication_expires_at?: components["schemas"]["Timestamp"];
            /** @constant */
            role: "administrator";
            session_expires_at: components["schemas"]["Timestamp"];
            user_id: components["schemas"]["UUID"];
            username: components["schemas"]["AuthUsername"];
        };
        Diagnostic: {
            code: components["schemas"]["ErrorCode"];
            field_path?: components["schemas"]["SafeFieldPath"];
            /** @description Fixed redacted explanation. */
            message: string;
            resource_id?: components["schemas"]["UUID"];
            /** @enum {string} */
            severity: "info" | "warning" | "error";
            target_key?: components["schemas"]["TargetKey"];
        };
        Diagnostics: components["schemas"]["Diagnostic"][];
        /** @description Resolver IDs are unique stable local keys, not names. final_resolver and each rule refer to a declared resolver. Bootstrap, resolver and outbound references are checked together for cycles. FakeIP and arbitrary native DNS options are outside P0. */
        DNSProfile: {
            bootstrap: components["schemas"]["BootstrapResolver"][];
            final_resolver: components["schemas"]["TargetKey"];
            resolvers: components["schemas"]["DNSResolver"][];
            rules: components["schemas"]["DNSRule"][];
            schema_version: components["schemas"]["SchemaVersion"];
        };
        DNSProfileCreateRequest: {
            dns_profile: components["schemas"]["DNSProfile"];
            enabled?: boolean;
            name: components["schemas"]["Name"];
            tags?: components["schemas"]["Tags"];
        };
        DNSProfileListResponse: {
            data: components["schemas"]["DNSProfileResource"][];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        DNSProfilePatch: {
            bootstrap?: components["schemas"]["BootstrapResolver"][];
            final_resolver?: components["schemas"]["TargetKey"];
            resolvers?: components["schemas"]["DNSResolver"][];
            rules?: components["schemas"]["DNSRule"][];
        };
        DNSProfilePatchRequest: {
            dns_profile?: components["schemas"]["DNSProfilePatch"];
            enabled?: boolean;
            name?: components["schemas"]["Name"];
            tags?: components["schemas"]["Tags"];
        };
        DNSProfileResource: {
            diagnostics: components["schemas"]["Diagnostics"];
            dns_profile: components["schemas"]["DNSProfile"];
            metadata: components["schemas"]["ResourceMetadata"];
        };
        DNSProfileResponse: {
            data: components["schemas"]["DNSProfileResource"];
            request_id: components["schemas"]["RequestID"];
        };
        DNSResolver: components["schemas"]["LocalDNSResolver"] | components["schemas"]["UDPDNSResolver"] | components["schemas"]["HTTPSDNSResolver"];
        DNSRule: {
            comment: string;
            enabled: boolean;
            match: components["schemas"]["DomainMatch"];
            resolver_id: components["schemas"]["TargetKey"];
        };
        DomainMatch: {
            domain_exact?: components["schemas"]["DomainName"][];
            domain_suffix?: components["schemas"]["DomainName"][];
            rule_set_ids?: components["schemas"]["UUID"][];
        };
        /**
         * Format: hostname
         * @description Normalized lowercase IDNA ASCII domain. Suffix match includes the domain itself and dot-delimited subdomains.
         */
        DomainName: string;
        /** @enum {string} */
        DomainResolutionMode: "preserve_domain" | "resolve_for_ip_rules";
        DomainRuleSetEntry: {
            domain: components["schemas"]["DomainName"];
            /** @constant */
            kind: "domain";
            /** @enum {string} */
            match: "exact" | "suffix";
        };
        Endpoint: {
            host: components["schemas"]["Host"];
            port: number;
        };
        EnumFieldConstraint: {
            allowed_values: components["schemas"]["ShortText"][];
            field_path: components["schemas"]["SafeFieldPath"];
            /** @constant */
            kind: "enum";
        };
        /** @enum {string} */
        ErrorCode: "MALFORMED_REQUEST" | "UNKNOWN_FIELD" | "DUPLICATE_FIELD" | "AUTH_REQUIRED" | "SESSION_EXPIRED" | "PERMISSION_DENIED" | "REAUTH_REQUIRED" | "RESOURCE_NOT_FOUND" | "STATE_CONFLICT" | "IDEMPOTENCY_CONFLICT" | "LEASE_LOST" | "REVISION_MISMATCH" | "INPUT_LIMIT_EXCEEDED" | "VALIDATION_FAILED" | "PRECONDITION_REQUIRED" | "RATE_LIMITED" | "SERVICE_UNAVAILABLE" | "INTERNAL_ERROR" | "COMPILE_OBSOLETE" | "CAPABILITY_UNSUPPORTED" | "CAPABILITY_UNVERIFIED" | "DEPENDENCY_EXCLUDED" | "DEPENDENCY_CYCLE" | "DNS_CYCLE" | "EMPTY_GROUP" | "PUBLICATION_BLOCKED" | "PUBLICATION_CONFIRMATION_REQUIRED" | "SUBSCRIPTION_NOT_READY" | "SUBSCRIPTION_BLOCKED" | "RUNNER_UNAVAILABLE" | "BUDGET_EXCEEDED" | "INVALID_CONFIG" | "CORE_CONFIG_INVALID" | "PROXY_CONNECT_FAILED" | "AUTH_FAILED" | "TARGET_TLS_FAILED" | "HTTP_EXPECTATION_FAILED" | "CORE_BINARY_MISMATCH" | "PORT_BUSY" | "RUNNER_RESOURCE_LIMIT" | "CANCELED" | "JOB_TIMEOUT" | "TEST_TARGET_UNAVAILABLE" | "INSUFFICIENT_SAMPLE";
        ErrorDetail: {
            field_path?: components["schemas"]["SafeFieldPath"];
            resource_id?: components["schemas"]["UUID"];
        };
        ErrorResponse: {
            error: components["schemas"]["SafeError"];
            request_id: components["schemas"]["RequestID"];
        };
        ExportData: {
            artifacts: components["schemas"]["ExportedArtifact"][];
            created_at: components["schemas"]["Timestamp"];
        };
        ExportedArtifact: {
            contains_secrets: boolean;
            /** @description Explicit export bytes serialized as UTF-8 text. Secret exports require recent authentication; redacted exports cannot be republished as working configurations. */
            content: string;
            /** @description Server-selected attachment label, never a client-controlled path. */
            filename: string;
            /** @enum {string} */
            media_type: "application/json" | "application/yaml" | "text/plain";
        };
        ExportRequest: components["schemas"]["ResourceExportRequest"] | components["schemas"]["PublicationExportRequest"];
        ExportResourceRef: {
            /** @enum {string} */
            kind: "node" | "chain" | "policy_group" | "routing_profile" | "dns_profile" | "rule_set" | "subscription_profile";
            resource_id: components["schemas"]["UUID"];
            revision: components["schemas"]["Revision"];
        };
        ExportResponse: {
            data: components["schemas"]["ExportData"];
            request_id: components["schemas"]["RequestID"];
        };
        FieldConstraint: components["schemas"]["EnumFieldConstraint"] | components["schemas"]["IntegerFieldConstraint"] | components["schemas"]["BooleanFieldConstraint"];
        Fingerprint: string;
        /** @description Omitted preserves, string replaces, null clears optional TLS fingerprint. Clearing required REALITY fingerprint fails final node validation. */
        FingerprintPatch: components["schemas"]["Fingerprint"] | null;
        FrozenTestSubject: {
            id: components["schemas"]["UUID"];
            /** @enum {string} */
            kind: "node" | "chain";
            revision: components["schemas"]["Revision"];
            security_epoch: components["schemas"]["Revision"];
        };
        /** @description Immutable approved registry target for this attempt. Enforce pinned validated addresses while retaining Host/SNI and certificate verification. Revalidate safety at execution; never accept replacement URLs from a probe caller. */
        FrozenTestTarget: {
            /** @constant */
            compression: "disabled";
            expected_response: components["schemas"]["HTTPExpectation"];
            /** @constant */
            redirect_policy: "deny";
            revision: components["schemas"]["Revision"];
            test_target_id: components["schemas"]["UUID"];
            url: components["schemas"]["HTTPSURL"];
            validated_ips: components["schemas"]["IPAddress"][];
            /** @constant */
            verify_certificate: true;
        };
        /** @description Normalized lowercase ASCII hostname or unbracketed IP; no URI, port or path. Format assertions are required. */
        Host: string;
        /** @description No arbitrary script, expression or response template. Optional SHA-256 checks an exact controlled response; egress IP parsing uses a fixed built-in format. */
        HTTPExpectation: {
            body_sha256?: components["schemas"]["SHA256"];
            egress_ip_response: boolean;
            max_response_bytes: number;
            status_codes: number[];
        };
        HTTPSDNSResolver: {
            bootstrap_resolver_id: components["schemas"]["TargetKey"];
            /** @constant */
            kind: "https";
            outbound: components["schemas"]["TargetRef"];
            resolver_id: components["schemas"]["TargetKey"];
            url: components["schemas"]["HTTPSURL"];
        };
        /**
         * Format: uri
         * @description HTTPS only; URL parser must additionally reject userinfo, fragments, invalid ports and unsafe addresses. Every resolution, redirect and connection requires SafeFetcher validation. Not a standalone SSRF guarantee.
         */
        HTTPSURL: string;
        /** @description Allowlisted replay metadata only. The fixed route determines operation_id semantics (job, batch, publication or token). No raw response bytes, token, URL, auth value or arbitrary metadata are persistable. Replay repeats current authorization and does not advance the catalog revision. */
        IdempotencyReceipt: {
            /** @enum {integer} */
            http_status: 200 | 201 | 202 | 204;
            operation_id?: components["schemas"]["UUID"];
            replayed: boolean;
            resource_id?: components["schemas"]["UUID"];
            revision?: components["schemas"]["Revision"];
            /** @enum {string} */
            status: "created" | "updated" | "deleted" | "revoked" | "accepted";
        };
        IdempotencyReceiptResponse: {
            data: components["schemas"]["IdempotencyReceipt"];
            request_id: components["schemas"]["RequestID"];
        };
        ImportAccepted: {
            batch_id: components["schemas"]["UUID"];
            job_id: components["schemas"]["UUID"];
            revision: components["schemas"]["Revision"];
            /** @enum {string} */
            state: "queued" | "parsing";
        };
        ImportAcceptedResponse: {
            data: components["schemas"]["ImportAccepted"];
            request_id: components["schemas"]["RequestID"];
        };
        ImportBatch: {
            batch_id: components["schemas"]["UUID"];
            binding_revision?: components["schemas"]["Revision"];
            candidate_count: number;
            candidates: components["schemas"]["ImportCandidate"][];
            created_at: components["schemas"]["Timestamp"];
            diagnostics: components["schemas"]["Diagnostics"];
            expires_at: components["schemas"]["Timestamp"];
            job_id: components["schemas"]["UUID"];
            revision: components["schemas"]["Revision"];
            source_id?: components["schemas"]["UUID"];
            source_revision?: components["schemas"]["Revision"];
            /** @enum {string} */
            state: "queued" | "parsing" | "ready" | "failed" | "committed" | "expired";
        };
        ImportCandidate: {
            candidate_id: components["schemas"]["UUID"];
            changed_fields?: components["schemas"]["SafeFieldPath"][];
            diagnostics: components["schemas"]["Diagnostics"];
            existing_resource_id?: components["schemas"]["UUID"];
            existing_revision?: components["schemas"]["Revision"];
            index: number;
            /** @enum {string} */
            match_method?: "stable_external_key" | "manual_binding" | "exact_fingerprint" | "suggestion";
            name?: components["schemas"]["Name"];
            node?: components["schemas"]["NodeRedacted"];
            /** @enum {string} */
            state: "new" | "matched" | "conflict" | "invalid";
        };
        ImportCommit: {
            batch_id: components["schemas"]["UUID"];
            items: components["schemas"]["ImportCommitItem"][];
            revision: components["schemas"]["Revision"];
        };
        ImportCommitItem: {
            candidate_id: components["schemas"]["UUID"];
            resource_id?: components["schemas"]["UUID"];
            revision?: components["schemas"]["Revision"];
            /** @enum {string} */
            status: "created" | "updated" | "skipped" | "bound";
        };
        /** @description The preview ETag is supplied in If-Match. Source-backed commits additionally require matching source_revision and binding_revision. Selected writes and binding changes are one transaction; conflicts never silently overwrite. */
        ImportCommitRequest: {
            binding_revision?: components["schemas"]["Revision"];
            decisions: components["schemas"]["ImportDecision"][];
            source_revision?: components["schemas"]["Revision"];
        };
        ImportCommitResponse: {
            data: components["schemas"]["ImportCommit"];
            request_id: components["schemas"]["RequestID"];
        };
        ImportCreateRequest: {
            format: components["schemas"]["ImportFormat"];
            source_id?: components["schemas"]["UUID"];
            /** @description Bounded source text; never logged. Parsing and decoded-size limits also apply. */
            text: string;
        };
        ImportDecision: {
            /** @enum {string} */
            action: "create" | "update" | "skip" | "bind";
            candidate_id: components["schemas"]["UUID"];
            expected_revision?: components["schemas"]["Revision"];
            override?: components["schemas"]["NodePatchRequest"];
            resource_id?: components["schemas"]["UUID"];
        } & (unknown & unknown);
        ImportFileRequest: {
            /**
             * Format: binary
             * @description Restricted file bytes only; the client filename is never a server filesystem path.
             */
            file: string;
            format: components["schemas"]["ImportFormat"];
            source_id?: components["schemas"]["UUID"];
        };
        /** @enum {string} */
        ImportFormat: "auto" | "uri_list" | "base64_uri_list" | "xray_json" | "singbox_json" | "mihomo_yaml";
        ImportResponse: {
            data: components["schemas"]["ImportBatch"];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        IntegerFieldConstraint: {
            field_path: components["schemas"]["SafeFieldPath"];
            /** @constant */
            kind: "integer_range";
            maximum: number;
            minimum: number;
        };
        IPAddress: string;
        /** @description Persistent execution state is independent of verdict. A complete negative network probe is succeeded+fail; infrastructure failure is failed. Missing verdict means no conclusion yet. No raw input or output payload is a job field. */
        Job: {
            attempt: number;
            batch_id?: components["schemas"]["UUID"];
            cancel_requested: boolean;
            core_build_id?: components["schemas"]["UUID"];
            created_at: components["schemas"]["Timestamp"];
            error?: components["schemas"]["SafeError"];
            /** @enum {string} */
            executor: "api_worker" | "runner";
            finished_at?: components["schemas"]["Timestamp"];
            job_id: components["schemas"]["UUID"];
            lease_seq: components["schemas"]["Counter"];
            revision: components["schemas"]["Revision"];
            started_at?: components["schemas"]["Timestamp"];
            state: components["schemas"]["JobState"];
            subject?: components["schemas"]["FrozenTestSubject"];
            test_target_id?: components["schemas"]["UUID"];
            type: components["schemas"]["JobType"];
            verdict?: components["schemas"]["Verdict"];
        } & (unknown & unknown);
        /** @description SSE event=job_event; SSE id is decimal seq. Sequence is server-assigned, persisted and monotonically increasing across attempts. completed<=total. No raw stdout/stderr, arbitrary JSON data or unbounded log text. */
        JobEvent: {
            completed: number;
            error?: components["schemas"]["SafeError"];
            job_id: components["schemas"]["UUID"];
            phase: components["schemas"]["JobPhase"];
            seq: components["schemas"]["Revision"];
            total: number;
            verdict?: components["schemas"]["Verdict"];
        };
        JobListResponse: {
            data: components["schemas"]["Job"][];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        /** @enum {string} */
        JobPhase: "queued" | "leased" | "fetching" | "parsing" | "compiling" | "preparing" | "validating" | "starting" | "running" | "probing" | "downloading" | "collecting" | "settling" | "canceling" | "completed";
        JobResponse: {
            data: components["schemas"]["Job"];
            request_id: components["schemas"]["RequestID"];
        };
        /** @enum {string} */
        JobState: "queued" | "leased" | "running" | "succeeded" | "failed" | "canceled" | "timed_out";
        /** @enum {string} */
        JobType: "source_refresh" | "import_parse" | "compile" | "config_validate" | "connectivity" | "download_throughput";
        LocalBootstrapResolver: {
            /** @constant */
            kind: "local";
            resolver_id: components["schemas"]["TargetKey"];
        };
        LocalDNSResolver: {
            /** @constant */
            kind: "local";
            resolver_id: components["schemas"]["TargetKey"];
        };
        LocalListener: {
            /** @enum {string} */
            listen: "127.0.0.1" | "::1";
            port: number;
            /** @enum {string} */
            protocol: "socks5" | "http" | "mixed";
        };
        LoginRequest: {
            password: components["schemas"]["AuthPassword"];
            username: components["schemas"]["AuthUsername"];
        };
        LogoutRequest: Record<string, never>;
        MemberRef: {
            /** @enum {string} */
            kind: "node" | "chain";
            resource_id: components["schemas"]["UUID"];
            /** @constant */
            type: "resource_ref";
        };
        MethodPasswordAuth: {
            /** @constant */
            kind: "method_password";
            method: components["schemas"]["ShadowsocksMethod"];
            password: components["schemas"]["Secret"];
        };
        MethodPasswordAuthPatch: {
            /** @constant */
            kind: "method_password";
            method?: components["schemas"]["ShadowsocksMethod"];
            password?: components["schemas"]["SecretPatch"];
        };
        MethodPasswordAuthRedacted: {
            has_password: boolean;
            /** @constant */
            kind: "method_password";
            method: components["schemas"]["ShadowsocksMethod"];
        };
        MutationReceipt: {
            resource_id: components["schemas"]["UUID"];
            revision: components["schemas"]["Revision"];
            security_epoch: components["schemas"]["Revision"];
            /** @enum {string} */
            status: "updated" | "deleted" | "revoked";
        };
        MutationResponse: {
            data: components["schemas"]["MutationReceipt"];
            request_id: components["schemas"]["RequestID"];
        };
        Name: string;
        NativeTCPTransport: {
            /** @constant */
            kind: "native_tcp";
        };
        NoAuth: {
            /** @constant */
            kind: "none";
        };
        NodeAuth: components["schemas"]["MethodPasswordAuth"] | components["schemas"]["VMessAuth"] | components["schemas"]["UUIDAuth"] | components["schemas"]["PasswordAuth"] | components["schemas"]["UsernamePasswordAuth"] | components["schemas"]["NoAuth"];
        /** @description Discriminated typed merge. Absent preserves, null clears, string replaces. Revalidate the merged protocol; required-secret removal is 422. A changed kind never inherits unrelated secret fields. */
        NodeAuthPatch: components["schemas"]["MethodPasswordAuthPatch"] | components["schemas"]["VMessAuthPatch"] | components["schemas"]["UUIDAuthPatch"] | components["schemas"]["PasswordAuthPatch"] | components["schemas"]["UsernamePasswordAuthPatch"] | components["schemas"]["NoAuth"];
        NodeAuthRedacted: components["schemas"]["MethodPasswordAuthRedacted"] | components["schemas"]["VMessAuthRedacted"] | components["schemas"]["UUIDAuthRedacted"] | components["schemas"]["PasswordAuthRedacted"] | components["schemas"]["UsernamePasswordAuthRedacted"] | components["schemas"]["NoAuth"];
        NodeBatchItem: {
            error?: components["schemas"]["SafeError"];
            /** @enum {integer} */
            http_status: 200 | 404 | 409 | 412 | 422 | 428;
            node_id: components["schemas"]["UUID"];
            revision?: components["schemas"]["Revision"];
        } & unknown;
        NodeBatchRequest: {
            enabled?: boolean;
            node_ids: components["schemas"]["UUID"][];
            /** @enum {string} */
            operation: "add_tags" | "remove_tags" | "set_enabled";
            preconditions: components["schemas"]["NodePrecondition"][];
            tags?: components["schemas"]["Tags"];
        } & (unknown & unknown);
        NodeBatchResponse: {
            data: components["schemas"]["NodeBatchItem"][];
            request_id: components["schemas"]["RequestID"];
        };
        /** @description Clone the If-Match source revision on the server with a new stable ID and revision 1; secrets never pass through a read response. */
        NodeCloneRequest: {
            name: components["schemas"]["Name"];
        };
        /** @description Management creation DTO for typed IR v1. Optional features/extensions default to empty objects at the validated domain boundary. Structural acceptance never certifies a kernel combination. */
        NodeCreate: {
            auth: components["schemas"]["NodeAuth"];
            endpoint: components["schemas"]["Endpoint"];
            extensions?: components["schemas"]["NodeExtensions"];
            features?: components["schemas"]["NodeFeatures"];
            origin?: components["schemas"]["NodeOrigin"];
            protocol: components["schemas"]["Protocol"];
            schema_version: components["schemas"]["SchemaVersion"];
            security: components["schemas"]["NodeSecurity"];
            transport: components["schemas"]["NodeTransport"];
        } & (unknown & unknown & unknown & unknown & unknown & unknown & unknown);
        NodeCreateRequest: {
            enabled?: boolean;
            name: components["schemas"]["Name"];
            node: components["schemas"]["NodeCreate"];
            tags?: components["schemas"]["Tags"];
        };
        /** @description IR v1 extension whitelist is empty. Native namespaces, dependency fields and execution settings are not accepted. */
        NodeExtensions: Record<string, never>;
        NodeFeatures: {
            multiplex?: boolean;
            /** @constant */
            protocol_variant?: "xtls-rprx-vision";
            udp?: boolean;
        };
        NodeListResponse: {
            data: components["schemas"]["NodeResource"][];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        NodeOrigin: {
            /** @enum {string} */
            match_method: "stable_external_key" | "manual_binding" | "exact_fingerprint";
            source_item_id: components["schemas"]["UUID"];
            source_resource_id: components["schemas"]["UUID"];
        };
        /** @description Only these typed fields are writable. Endpoint/transport/features are whole-object replacements; auth/security use the documented typed merge. No arbitrary merge-patch, scope, revision, epoch, origin or native dependency fields. */
        NodePatch: {
            auth?: components["schemas"]["NodeAuthPatch"];
            endpoint?: components["schemas"]["Endpoint"];
            features?: components["schemas"]["NodeFeatures"];
            protocol?: components["schemas"]["Protocol"];
            security?: components["schemas"]["NodeSecurityPatch"];
            transport?: components["schemas"]["NodeTransport"];
        };
        NodePatchRequest: {
            enabled?: boolean;
            name?: components["schemas"]["Name"];
            node?: components["schemas"]["NodePatch"];
            tags?: components["schemas"]["Tags"];
        };
        NodePrecondition: {
            node_id: components["schemas"]["UUID"];
            revision: components["schemas"]["Revision"];
        };
        NodeReadResponse: {
            data: components["schemas"]["NodeResource"];
            request_id: components["schemas"]["RequestID"];
        };
        /** @description Dedicated management read DTO, never a serialized IR Node or Resource. Password, UUID, username, REALITY public_key and short_id are absent rather than masked. */
        NodeRedacted: {
            auth: components["schemas"]["NodeAuthRedacted"];
            endpoint: components["schemas"]["Endpoint"];
            extensions: components["schemas"]["NodeExtensions"];
            features: components["schemas"]["NodeFeatures"];
            origin?: components["schemas"]["NodeOrigin"];
            protocol: components["schemas"]["Protocol"];
            schema_version: components["schemas"]["SchemaVersion"];
            security: components["schemas"]["NodeSecurityRedacted"];
            transport: components["schemas"]["NodeTransport"];
        } & (unknown & unknown & unknown & unknown & unknown & unknown & unknown);
        NodeRef: {
            node_id: components["schemas"]["UUID"];
        };
        NodeResource: {
            metadata: components["schemas"]["ResourceMetadata"];
            node: components["schemas"]["NodeRedacted"];
        };
        /** @description Explicit privileged one-time secret response. Excluded from ordinary read, audit, logging and idempotency storage. Not a complete IR Resource. */
        NodeReveal: {
            auth: components["schemas"]["NodeAuth"];
            resource_id: components["schemas"]["UUID"];
            revision: components["schemas"]["Revision"];
            security: components["schemas"]["NodeSecurity"];
        };
        NodeRevealResponse: {
            data: components["schemas"]["NodeReveal"];
            request_id: components["schemas"]["RequestID"];
        };
        NodeSecurity: components["schemas"]["NoSecurity"] | components["schemas"]["TLSSecurity"] | components["schemas"]["RealitySecurity"];
        /** @description Same-mode typed merge preserves absent fields; mode changes require the final merged security object to be complete. REALITY secrets support absent/null/string; required clear is 422. */
        NodeSecurityPatch: components["schemas"]["NoSecurity"] | components["schemas"]["TLSSecurityPatch"] | components["schemas"]["RealitySecurityPatch"];
        NodeSecurityRedacted: components["schemas"]["NoSecurity"] | components["schemas"]["TLSSecurity"] | components["schemas"]["RealitySecurityRedacted"];
        NodeTransport: components["schemas"]["NativeTCPTransport"] | components["schemas"]["WebSocketTransport"];
        NoSecurity: {
            /** @constant */
            mode: "none";
        };
        /** @enum {string} */
        OutputFormat: "xray_json" | "singbox_json" | "mihomo_yaml";
        PageInfo: {
            /** @default 50 */
            limit: number;
            /** @description Opaque authenticated cursor; omitted at end. Bound to scope, collection, ordering and normalized filter digest. Cursor is not authorization. */
            next_cursor?: string;
        };
        PasswordAuth: {
            /** @constant */
            kind: "password";
            password: components["schemas"]["Secret"];
        };
        PasswordAuthPatch: {
            /** @constant */
            kind: "password";
            password?: components["schemas"]["SecretPatch"];
        };
        PasswordAuthRedacted: {
            has_password: boolean;
            /** @constant */
            kind: "password";
        };
        /** @enum {string} */
        Platform: "linux" | "windows" | "macos" | "android" | "ios";
        /** @description default_member must be one of members. P0 never nests groups or inserts direct. fixed selects at publication time; manual_select selects in the client. */
        PolicyGroup: {
            default_member: components["schemas"]["MemberRef"];
            health_check: components["schemas"]["PolicyHealthCheck"];
            members: components["schemas"]["MemberRef"][];
            /** @constant */
            on_unavailable: "fail_closed";
            schema_version: components["schemas"]["SchemaVersion"];
            strategy: components["schemas"]["PolicyStrategy"];
        };
        PolicyGroupCreateRequest: {
            enabled?: boolean;
            name: components["schemas"]["Name"];
            policy_group: components["schemas"]["PolicyGroup"];
            tags?: components["schemas"]["Tags"];
        };
        PolicyGroupListResponse: {
            data: components["schemas"]["PolicyGroupResource"][];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        PolicyGroupPatch: {
            default_member?: components["schemas"]["MemberRef"];
            health_check?: components["schemas"]["PolicyHealthCheck"];
            members?: components["schemas"]["MemberRef"][];
            /** @constant */
            on_unavailable?: "fail_closed";
            strategy?: components["schemas"]["PolicyStrategy"];
        };
        PolicyGroupPatchRequest: {
            enabled?: boolean;
            name?: components["schemas"]["Name"];
            policy_group?: components["schemas"]["PolicyGroupPatch"];
            tags?: components["schemas"]["Tags"];
        };
        PolicyGroupResource: {
            diagnostics: components["schemas"]["Diagnostics"];
            metadata: components["schemas"]["ResourceMetadata"];
            policy_group: components["schemas"]["PolicyGroup"];
        };
        PolicyGroupResponse: {
            data: components["schemas"]["PolicyGroupResource"];
            request_id: components["schemas"]["RequestID"];
        };
        /** @description Explicit client runtime observation, independent of platform background tests. If enabled, URL, interval, timeout and tolerance are all required. Unsupported target behavior blocks publication. */
        PolicyHealthCheck: {
            enabled: boolean;
            interval_ms?: number;
            timeout_ms?: number;
            tolerance_ms?: number;
            url?: components["schemas"]["HTTPSURL"];
        } & unknown;
        /** @description Only reviewed strategy fields; never override member identity, authentication, dependency checks or fail_closed. */
        PolicyOverride: {
            default_member?: components["schemas"]["MemberRef"];
            health_check?: components["schemas"]["PolicyHealthCheck"];
            policy_group_id: components["schemas"]["UUID"];
            strategy?: components["schemas"]["PolicyStrategy"];
        };
        /** @enum {string} */
        PolicyStrategy: "fixed" | "manual_select" | "latency_best" | "round_robin";
        /** @description Inclusive range; from must be <= to. */
        PortRange: {
            from: number;
            to: number;
        };
        /** @enum {string} */
        Protocol: "shadowsocks" | "vmess" | "vless" | "trojan" | "socks5" | "http";
        /** @description Immutable publish record; state and blocking reasons are current safe projections. Rollback adds a new generation and source_publication_id instead of mutating old output. */
        Publication: {
            batch_id: components["schemas"]["UUID"];
            blocking_reasons: components["schemas"]["Diagnostics"];
            created_at: components["schemas"]["Timestamp"];
            created_by: components["schemas"]["UUID"];
            dependencies: components["schemas"]["PublicationDependency"][];
            generation: components["schemas"]["Revision"];
            publication_id: components["schemas"]["UUID"];
            source_publication_id?: components["schemas"]["UUID"];
            /** @enum {string} */
            state: "active" | "historical" | "blocked";
            subscription_id: components["schemas"]["UUID"];
            targets: components["schemas"]["PublishedTarget"][];
        };
        /** @description Explicit user acknowledgement. The server must also possess and verify a confirmation tied to its authenticated current actor, the request batch_id and effective_preview_hash. This field alone is not proof or authorization; actor ID cannot be nominated by the client. */
        PublicationConfirmation: {
            /** @constant */
            acknowledged: true;
        };
        PublicationDependency: {
            credential_categories: ("password" | "uuid" | "username" | "reality_public_key" | "reality_short_id")[];
            /** @enum {string} */
            inclusion: "explicit" | "automatic";
            kind: components["schemas"]["ResourceKind"];
            required_by: components["schemas"]["UUID"][];
            resource_id: components["schemas"]["UUID"];
            revision: components["schemas"]["Revision"];
            security_epoch: components["schemas"]["Revision"];
        };
        PublicationExportRequest: {
            /** @enum {string} */
            format: "xray_json" | "singbox_json" | "mihomo_yaml";
            include_secrets: boolean;
            publication_id: components["schemas"]["UUID"];
            target_keys: components["schemas"]["TargetKey"][];
            /** @constant */
            type: "publication";
        };
        PublicationHead: {
            blocking_reasons: components["schemas"]["Diagnostics"];
            generation: components["schemas"]["Counter"];
            publication_id?: components["schemas"]["UUID"];
            /** @enum {string} */
            state: "not_ready" | "active" | "blocked";
        };
        PublicationListResponse: {
            data: components["schemas"]["Publication"][];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        PublicationResponse: {
            data: components["schemas"]["Publication"];
            request_id: components["schemas"]["RequestID"];
        };
        PublishedTarget: {
            artifact_id: components["schemas"]["UUID"];
            client_preset_id: components["schemas"]["UUID"];
            client_preset_revision: components["schemas"]["Revision"];
            core_build_id: components["schemas"]["UUID"];
            core_build_sha256: components["schemas"]["SHA256"];
            format: components["schemas"]["OutputFormat"];
            target_key: components["schemas"]["TargetKey"];
            validation_job_id: components["schemas"]["UUID"];
        };
        PublishRequest: {
            batch_id: components["schemas"]["UUID"];
            confirmation: components["schemas"]["PublicationConfirmation"];
            effective_preview_hash: components["schemas"]["SHA256"];
            expected_generation: components["schemas"]["Counter"];
        };
        /** @description UTC daily bucket; reservations lock before scheduling. Requested values cannot raise immutable host-level resource isolation limits. Retry attempts reserve independently; unknown throughput usage is conservatively settled at its reserved upper bound. */
        QuotaSettings: {
            connectivity_concurrency: number;
            daily_download_bytes: number;
            max_test_bytes: number;
            max_test_duration_ms: number;
            minimum_throughput_sample_bytes: number;
            throughput_concurrency: number;
        };
        QuotaSettingsPatch: {
            connectivity_concurrency?: number;
            daily_download_bytes?: number;
            max_test_bytes?: number;
            max_test_duration_ms?: number;
            minimum_throughput_sample_bytes?: number;
            throughput_concurrency?: number;
        };
        RealityPublicKey: string;
        RealitySecurity: {
            alpn?: components["schemas"]["ALPN"];
            client_fingerprint: components["schemas"]["Fingerprint"];
            /** @constant */
            mode: "reality";
            public_key: components["schemas"]["RealityPublicKey"];
            server_name: components["schemas"]["Host"];
            short_id: components["schemas"]["RealityShortID"];
        };
        RealitySecurityPatch: {
            alpn?: components["schemas"]["ALPNPatch"];
            client_fingerprint?: components["schemas"]["FingerprintPatch"];
            /** @constant */
            mode: "reality";
            public_key?: components["schemas"]["SecretPatch"];
            server_name?: components["schemas"]["Host"];
            short_id?: components["schemas"]["SecretPatch"];
        };
        RealitySecurityRedacted: {
            alpn?: components["schemas"]["ALPN"];
            client_fingerprint: components["schemas"]["Fingerprint"];
            has_public_key: boolean;
            has_short_id: boolean;
            /** @constant */
            mode: "reality";
            server_name: components["schemas"]["Host"];
        };
        RealityShortID: string;
        ReasonRequest: {
            /** @description Administrative reason; exclude credentials and configuration. */
            reason: string;
        };
        ReauthenticationRequest: {
            password: components["schemas"]["AuthPassword"];
        };
        ReferenceListResponse: {
            data: components["schemas"]["ResourceReference"][];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        RequestID: string;
        ResourceExportRequest: {
            /** @enum {string} */
            format: "proxyloom_json" | "uri_list";
            include_secrets: boolean;
            resources: components["schemas"]["ExportResourceRef"][];
            /** @constant */
            type: "resources";
        };
        /** @enum {string} */
        ResourceKind: "node" | "chain" | "policy_group" | "routing_profile" | "dns_profile" | "rule_set" | "client_preset" | "subscription_profile" | "source";
        ResourceMetadata: {
            enabled: boolean;
            kind: components["schemas"]["ResourceKind"];
            name: components["schemas"]["Name"];
            resource_id: components["schemas"]["UUID"];
            revision: components["schemas"]["Revision"];
            schema_version: components["schemas"]["SchemaVersion"];
            scope_id: components["schemas"]["UUID"];
            security_epoch: components["schemas"]["Revision"];
            tags: components["schemas"]["Tags"];
        };
        ResourceReference: {
            field_path: components["schemas"]["SafeFieldPath"];
            source_kind: components["schemas"]["ResourceKind"];
            source_resource_id: components["schemas"]["UUID"];
            source_revision: components["schemas"]["Revision"];
            /** @enum {string} */
            state: "active" | "historical";
            target_resource_id: components["schemas"]["UUID"];
            target_revision?: components["schemas"]["Revision"];
        };
        RetentionSettings: {
            audit_days: number;
            idempotency_hours: number;
            job_event_days: number;
            test_result_days: number;
        };
        RetentionSettingsPatch: {
            audit_days?: number;
            idempotency_hours?: number;
            job_event_days?: number;
            test_result_days?: number;
        };
        /** @description Canonical positive decimal int64 string, 1..9223372036854775807. String encoding preserves exact values in browser clients. No signs or leading zeros. */
        Revision: string;
        RollbackRequest: {
            expected_generation: components["schemas"]["Counter"];
            publication_id: components["schemas"]["UUID"];
        };
        /** @description Different fields AND; entries in the same field OR. No arbitrary expression, regular-expression engine, native rule string or general NOT. Rule-set revisions freeze during compilation. */
        RouteMatch: {
            destination_ports?: components["schemas"]["PortRange"][];
            domain_exact?: components["schemas"]["DomainName"][];
            domain_suffix?: components["schemas"]["DomainName"][];
            ip_cidrs?: components["schemas"]["CIDR"][];
            network?: ("tcp" | "udp")[];
            rule_set_ids?: components["schemas"]["UUID"][];
        };
        /** @description Rules keep array order and first match wins. preserve_domain never resolves a domain just for IP matching; resolve_for_ip_rules resolves through the explicit DNS profile. Non-equivalent target semantics fail compilation. */
        RoutingProfile: {
            domain_resolution_mode: components["schemas"]["DomainResolutionMode"];
            final: components["schemas"]["TargetRef"];
            rules: components["schemas"]["RoutingRule"][];
            schema_version: components["schemas"]["SchemaVersion"];
        };
        RoutingProfileCreateRequest: {
            enabled?: boolean;
            name: components["schemas"]["Name"];
            routing_profile: components["schemas"]["RoutingProfile"];
            tags?: components["schemas"]["Tags"];
        };
        RoutingProfileListResponse: {
            data: components["schemas"]["RoutingProfileResource"][];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        RoutingProfilePatch: {
            domain_resolution_mode?: components["schemas"]["DomainResolutionMode"];
            final?: components["schemas"]["TargetRef"];
            rules?: components["schemas"]["RoutingRule"][];
        };
        RoutingProfilePatchRequest: {
            enabled?: boolean;
            name?: components["schemas"]["Name"];
            routing_profile?: components["schemas"]["RoutingProfilePatch"];
            tags?: components["schemas"]["Tags"];
        };
        RoutingProfileResource: {
            metadata: components["schemas"]["ResourceMetadata"];
            routing_profile: components["schemas"]["RoutingProfile"];
        };
        RoutingProfileResponse: {
            data: components["schemas"]["RoutingProfileResource"];
            request_id: components["schemas"]["RequestID"];
        };
        RoutingResourceRef: {
            /** @enum {string} */
            kind: "node" | "chain" | "policy_group";
            resource_id: components["schemas"]["UUID"];
            /** @constant */
            type: "resource_ref";
        };
        RoutingRule: {
            action: components["schemas"]["TargetRef"];
            comment: string;
            enabled: boolean;
            match: components["schemas"]["RouteMatch"];
        };
        RuleSet: {
            content_hash: components["schemas"]["SHA256"];
            entries: components["schemas"]["RuleSetEntry"][];
            /** @constant */
            format: "domain_cidr_text";
            schema_version: components["schemas"]["SchemaVersion"];
        };
        RuleSetCreateRequest: {
            enabled?: boolean;
            name: components["schemas"]["Name"];
            rule_set: components["schemas"]["RuleSetWrite"];
            tags?: components["schemas"]["Tags"];
        };
        RuleSetEntry: components["schemas"]["DomainRuleSetEntry"] | components["schemas"]["CIDRRuleSetEntry"];
        RuleSetListResponse: {
            data: components["schemas"]["RuleSetResource"][];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        RuleSetPatch: {
            entries?: components["schemas"]["RuleSetEntry"][];
        };
        RuleSetPatchRequest: {
            enabled?: boolean;
            name?: components["schemas"]["Name"];
            rule_set?: components["schemas"]["RuleSetPatch"];
            tags?: components["schemas"]["Tags"];
        };
        RuleSetResource: {
            metadata: components["schemas"]["ResourceMetadata"];
            rule_set: components["schemas"]["RuleSet"];
        };
        RuleSetResponse: {
            data: components["schemas"]["RuleSetResource"];
            request_id: components["schemas"]["RequestID"];
        };
        /** @description Normalized standard domain/CIDR entries; no native rules, remote provider, geodata download or arbitrary file. Invalid entries use safe /entries/N pointers; N+1 identifies the entry line. */
        RuleSetWrite: {
            entries: components["schemas"]["RuleSetEntry"][];
            /** @constant */
            format: "domain_cidr_text";
            schema_version: components["schemas"]["SchemaVersion"];
        };
        /** @description Server-generated frozen compiled bytes supplied only on the internal mTLS response. Runner verifies decoded length, digest and matching fixed core build before creating its own 0700 directory/0600 file. Never accepted as a caller-supplied native config, shell, argv or path; never logged. */
        RunnerArtifact: {
            artifact_id: components["schemas"]["UUID"];
            byte_length: number;
            content_base64: string;
            format: components["schemas"]["OutputFormat"];
            sha256: components["schemas"]["SHA256"];
        };
        RunnerBuildReport: {
            build_sha256: components["schemas"]["SHA256"];
            core_build_id: components["schemas"]["UUID"];
        };
        RunnerExecutionPolicy: {
            /** @constant */
            allow_core_downloads: false;
            /** @constant */
            allow_environment_proxy: false;
            /** @constant */
            allow_shell: false;
            memory_limit_bytes: number;
            /** @enum {string} */
            network: "none" | "controlled_target_only";
            process_limit: number;
            termination_grace_ms: number;
        };
        RunnerHeartbeatData: {
            accepted_build_ids: components["schemas"]["UUID"][];
            heartbeat_interval_ms: number;
            lease_duration_ms: number;
            runner_id: components["schemas"]["UUID"];
            server_time: components["schemas"]["Timestamp"];
        };
        /** @description Certificate-bound identity and server registration are authoritative. No capabilities, location or binaries may be installed merely because this request advertises them. */
        RunnerHeartbeatRequest: {
            architecture: components["schemas"]["Architecture"];
            available_slots: components["schemas"]["RunnerSlots"];
            builds: components["schemas"]["RunnerBuildReport"][];
            load: {
                active_jobs: number;
                cpu_throttled: boolean;
                memory_available_bytes: number;
            };
            platform: components["schemas"]["Platform"];
            runner_id: components["schemas"]["UUID"];
        };
        RunnerHeartbeatResponse: {
            data: components["schemas"]["RunnerHeartbeatData"];
            request_id: components["schemas"]["RequestID"];
        };
        RunnerJobEventReceipt: {
            attempt: number;
            event_id: components["schemas"]["UUID"];
            job_id: components["schemas"]["UUID"];
            lease_seq: components["schemas"]["Revision"];
            replayed: boolean;
            seq: components["schemas"]["Revision"];
        };
        /** @description completed<=total is also checked by the service. Error summaries are fixed safe messages, not arbitrary Runner output. event_id deduplicates delivery inside the valid attempt. */
        RunnerJobEventRequest: {
            attempt: number;
            completed: number;
            error?: components["schemas"]["SafeError"];
            event_id: components["schemas"]["UUID"];
            job_id: components["schemas"]["UUID"];
            lease_seq: components["schemas"]["Revision"];
            phase: components["schemas"]["RunnerPhase"];
            total: number;
            verdict?: components["schemas"]["Verdict"];
        };
        RunnerJobEventResponse: {
            data: components["schemas"]["RunnerJobEventReceipt"];
            request_id: components["schemas"]["RequestID"];
        };
        RunnerJobHeartbeatData: {
            attempt: number;
            cancel_requested: boolean;
            job_id: components["schemas"]["UUID"];
            lease_expires_at: components["schemas"]["Timestamp"];
            lease_seq: components["schemas"]["Revision"];
        };
        RunnerJobHeartbeatRequest: {
            attempt: number;
            job_id: components["schemas"]["UUID"];
            lease_seq: components["schemas"]["Revision"];
        };
        RunnerJobHeartbeatResponse: {
            data: components["schemas"]["RunnerJobHeartbeatData"];
            request_id: components["schemas"]["RequestID"];
        };
        RunnerJobResultReceipt: {
            attempt: number;
            job_id: components["schemas"]["UUID"];
            lease_seq: components["schemas"]["Revision"];
            replayed: boolean;
            result_hash: components["schemas"]["SHA256"];
            result_id: components["schemas"]["UUID"];
            settled_bytes: number;
        };
        /** @description Only fenced current-attempt results are accepted; server verifies canonical result_hash and settles the reservation once. Same accepted identity/hash replays metadata, different hash conflicts. A completed failing probe uses succeeded+fail. */
        RunnerJobResultRequest: {
            attempt: number;
            error?: components["schemas"]["SafeError"];
            job_id: components["schemas"]["UUID"];
            lease_seq: components["schemas"]["Revision"];
            metrics: components["schemas"]["TestMetrics"];
            result_hash: components["schemas"]["SHA256"];
            state: components["schemas"]["TerminalJobState"];
            verdict?: components["schemas"]["Verdict"];
        } & unknown;
        RunnerJobResultResponse: {
            data: components["schemas"]["RunnerJobResultReceipt"];
            request_id: components["schemas"]["RequestID"];
        };
        /** @enum {string} */
        RunnerJobType: "config_validate" | "connectivity" | "download_throughput";
        /** @description Server-assigned bounded lease. payload_sha256 binds the canonical frozen payload. Paths, argv and controlled local listener ports are allocated by the registered Runner adapter, never supplied by users. Expired renewal requires local stop before expiry. */
        RunnerLease: {
            artifact: components["schemas"]["RunnerArtifact"];
            attempt: number;
            core: components["schemas"]["CoreIdentity"];
            execution_policy: components["schemas"]["RunnerExecutionPolicy"];
            job_id: components["schemas"]["UUID"];
            lease_expires_at: components["schemas"]["Timestamp"];
            lease_seq: components["schemas"]["Revision"];
            limits: components["schemas"]["TestLimits"];
            payload_sha256: components["schemas"]["SHA256"];
            quota_reservation_id?: components["schemas"]["UUID"];
            schema_version: components["schemas"]["SchemaVersion"];
            subject?: components["schemas"]["FrozenTestSubject"];
            test_target?: components["schemas"]["FrozenTestTarget"];
            type: components["schemas"]["RunnerJobType"];
        } & (unknown & unknown);
        RunnerLeaseData: {
            lease: components["schemas"]["RunnerLease"] | null;
        };
        /** @description Only capacity is requested. The server matches its registered certificate capabilities to queue types and exact builds. A client cannot nominate a job, URL, command, path or user configuration. */
        RunnerLeaseRequest: {
            available_slots: components["schemas"]["RunnerSlots"];
            runner_id: components["schemas"]["UUID"];
        };
        RunnerLeaseResponse: {
            data: components["schemas"]["RunnerLeaseData"];
            request_id: components["schemas"]["RequestID"];
        };
        /** @enum {string} */
        RunnerPhase: "leased" | "preparing" | "validating" | "starting" | "running" | "probing" | "downloading" | "collecting" | "settling" | "canceling" | "completed";
        RunnerSlots: {
            config_validate: number;
            connectivity: number;
            download_throughput: number;
        };
        SafeError: {
            code: components["schemas"]["ErrorCode"];
            details: components["schemas"]["ErrorDetail"][];
            /** @description Bounded fixed safe explanation; no input values, SQL, ciphertext, URLs, raw schema messages or core stdout/stderr. */
            message: string;
        };
        /** @description Safe RFC 6901 pointer using only fixed, validated contract fields and array indices; root is empty. Unknown or duplicate input keys are represented by their safe parent, never echoed. Syntax alone is not authorization to output a supplied path. */
        SafeFieldPath: string;
        /** @constant */
        SchemaVersion: 1;
        /** @description Real secret, never a display mask. Only allowed in explicit secret input or privileged one-time reveal responses; ordinary read schemas are separate. */
        Secret: string;
        /** @description Patch tri-state: absent preserves, null clears and a string replaces. String content (including empty, masks and protocol format) is checked only after typed merge; domain-invalid credentials return 422. This structural DTO never makes the merged secret valid by itself. */
        SecretPatch: string | null;
        SecretUUID: components["schemas"]["UUID"];
        SecretUUIDPatch: components["schemas"]["SecretPatch"];
        SessionResponse: {
            data: components["schemas"]["CurrentUser"];
            request_id: components["schemas"]["RequestID"];
        };
        SettingsPatchRequest: {
            catalog_limits?: components["schemas"]["CatalogLimitsPatch"];
            quota?: components["schemas"]["QuotaSettingsPatch"];
            retention?: components["schemas"]["RetentionSettingsPatch"];
        };
        SettingsResponse: {
            data: components["schemas"]["SystemSettings"];
            request_id: components["schemas"]["RequestID"];
        };
        SetupRequest: {
            password: components["schemas"]["AuthPassword"];
            setup_token: components["schemas"]["SetupToken"];
            username: components["schemas"]["AuthUsername"];
        };
        /** @description Canonical unpadded base64url encoding of 32 random bytes from the controlled host command. */
        SetupToken: string;
        SHA256: string;
        /** @enum {string} */
        ShadowsocksMethod: "aes-128-gcm" | "aes-256-gcm" | "chacha20-ietf-poly1305";
        ShortText: string;
        /** @description SSE event=snapshot_reset. Requested history is outside the retained window; close the stream and reread this same job's authorized snapshot. snapshot_url UUID must equal job_id. */
        SnapshotResetEvent: {
            job_id: components["schemas"]["UUID"];
            latest_seq: components["schemas"]["Counter"];
            snapshot_url: string;
        };
        SourceAuth: components["schemas"]["SourceNoAuth"] | components["schemas"]["SourceBearerAuth"] | components["schemas"]["SourceBasicAuth"];
        SourceAuthPatch: components["schemas"]["SourceNoAuth"] | components["schemas"]["SourceBearerAuthPatch"] | components["schemas"]["SourceBasicAuthPatch"];
        SourceAuthRedacted: components["schemas"]["SourceNoAuth"] | components["schemas"]["SourceBearerAuthRedacted"] | components["schemas"]["SourceBasicAuthRedacted"];
        SourceBasicAuth: {
            /** @constant */
            kind: "basic";
            password: components["schemas"]["Secret"];
            username: components["schemas"]["Secret"];
        };
        SourceBasicAuthPatch: {
            /** @constant */
            kind: "basic";
            password?: components["schemas"]["SecretPatch"];
            username?: components["schemas"]["SecretPatch"];
        };
        SourceBasicAuthRedacted: {
            has_password: boolean;
            has_username: boolean;
            /** @constant */
            kind: "basic";
        };
        SourceBearerAuth: {
            /** @constant */
            kind: "bearer";
            token: components["schemas"]["Secret"];
        };
        SourceBearerAuthPatch: {
            /** @constant */
            kind: "bearer";
            token?: components["schemas"]["SecretPatch"];
        };
        SourceBearerAuthRedacted: {
            has_token: boolean;
            /** @constant */
            kind: "bearer";
        };
        SourceCreateRequest: {
            enabled?: boolean;
            name: components["schemas"]["Name"];
            source: components["schemas"]["SourceWrite"];
            tags?: components["schemas"]["Tags"];
        };
        SourceFetchLimits: {
            max_compressed_bytes: number;
            max_decoded_bytes: number;
            max_redirects: number;
            timeout_ms: number;
        };
        SourceListResponse: {
            data: components["schemas"]["SourceResource"][];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        SourceNoAuth: {
            /** @constant */
            kind: "none";
        };
        /** @description Absent secrets preserve, null clears and strings replace. A source still requires a URL and complete selected authentication; clearing required values is 422 after typed merge. */
        SourcePatch: {
            auth?: components["schemas"]["SourceAuthPatch"];
            fetch_limits?: components["schemas"]["SourceFetchLimits"];
            format?: components["schemas"]["ImportFormat"];
            refresh_policy?: components["schemas"]["SourceRefreshPolicy"];
            url?: components["schemas"]["SourceURL"] | null;
        };
        SourcePatchRequest: {
            enabled?: boolean;
            name?: components["schemas"]["Name"];
            source?: components["schemas"]["SourcePatch"];
            tags?: components["schemas"]["Tags"];
        };
        SourceRedacted: {
            auth: components["schemas"]["SourceAuthRedacted"];
            binding_revision: components["schemas"]["Revision"];
            fetch_limits: components["schemas"]["SourceFetchLimits"];
            format: components["schemas"]["ImportFormat"];
            has_url: boolean;
            last_error?: components["schemas"]["SafeError"];
            last_job_id?: components["schemas"]["UUID"];
            last_success_at?: components["schemas"]["Timestamp"];
            refresh_policy: components["schemas"]["SourceRefreshPolicy"];
            schema_version: components["schemas"]["SchemaVersion"];
            /**
             * Format: uri
             * @description Only scheme and authority (host and optional port). Every path, query, userinfo and fragment is omitted because an unrecognized path segment may itself be a subscription credential. Never the original source URL.
             */
            url_display: string;
        };
        SourceRefreshPolicy: {
            /** @enum {string} */
            commit_mode: "manual" | "safe_updates";
            enabled: boolean;
            interval_seconds: number;
            /** @enum {string} */
            missing_policy: "retain" | "disable";
        };
        SourceResource: {
            metadata: components["schemas"]["ResourceMetadata"];
            source: components["schemas"]["SourceRedacted"];
        };
        SourceResponse: {
            data: components["schemas"]["SourceResource"];
            request_id: components["schemas"]["RequestID"];
        };
        /**
         * Format: uri
         * @description Raw source URL may contain subscription credentials. Encrypt it and return only the sanitized display form; never log it.
         */
        SourceURL: string;
        SourceWrite: {
            auth: components["schemas"]["SourceAuth"];
            fetch_limits: components["schemas"]["SourceFetchLimits"];
            format: components["schemas"]["ImportFormat"];
            refresh_policy: components["schemas"]["SourceRefreshPolicy"];
            schema_version: components["schemas"]["SchemaVersion"];
            url: components["schemas"]["SourceURL"];
        };
        SubscriptionCreateRequest: {
            enabled?: boolean;
            name: components["schemas"]["Name"];
            subscription: components["schemas"]["SubscriptionProfile"];
            tags?: components["schemas"]["Tags"];
        };
        SubscriptionListResponse: {
            data: components["schemas"]["SubscriptionResource"][];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        /** @description Base=(include_ids union tag selection) minus exclude_ids. Compile expands explicit dependencies; excluding any necessary dependency returns DEPENDENCY_EXCLUDED. */
        SubscriptionMembers: {
            exclude_ids: components["schemas"]["UUID"][];
            include_ids: components["schemas"]["UUID"][];
            selector: components["schemas"]["TagSelector"];
        };
        SubscriptionPatchRequest: {
            enabled?: boolean;
            name?: components["schemas"]["Name"];
            subscription?: components["schemas"]["SubscriptionProfilePatch"];
            tags?: components["schemas"]["Tags"];
        };
        SubscriptionProfile: {
            connectivity_gate?: components["schemas"]["ConnectivityPublishGate"];
            dns_profile_id: components["schemas"]["UUID"];
            members: components["schemas"]["SubscriptionMembers"];
            /** @constant */
            publish_policy: "strict_all_targets";
            routing_profile_id: components["schemas"]["UUID"];
            schema_version: components["schemas"]["SchemaVersion"];
            targets: components["schemas"]["SubscriptionTarget"][];
        };
        SubscriptionProfilePatch: {
            connectivity_gate?: components["schemas"]["ConnectivityPublishGate"];
            dns_profile_id?: components["schemas"]["UUID"];
            members?: components["schemas"]["SubscriptionMembers"];
            /** @constant */
            publish_policy?: "strict_all_targets";
            routing_profile_id?: components["schemas"]["UUID"];
            targets?: components["schemas"]["SubscriptionTarget"][];
        };
        SubscriptionResource: {
            metadata: components["schemas"]["ResourceMetadata"];
            publication_head: components["schemas"]["PublicationHead"];
            subscription: components["schemas"]["SubscriptionProfile"];
        };
        SubscriptionResponse: {
            data: components["schemas"]["SubscriptionResource"];
            request_id: components["schemas"]["RequestID"];
        };
        /** @description Unique stable key. Omitted enabled means true. Compilation freezes the exact registered core digest and preset revision. Every enabled target must pass real native validation before atomic publication. */
        SubscriptionTarget: {
            client_parameters?: components["schemas"]["ClientParameterOverride"];
            client_preset_id: components["schemas"]["UUID"];
            core_build_id: components["schemas"]["UUID"];
            enabled?: boolean;
            format: components["schemas"]["OutputFormat"];
            key: components["schemas"]["TargetKey"];
            policy_overrides: components["schemas"]["PolicyOverride"][];
        };
        /** @description sub_<public_id>.<base64url 32 random bytes>. Public ID only locates a verification record; all authority comes from live database checks. Never log this path value. */
        SubscriptionToken: string;
        SystemSettings: {
            catalog_limits: components["schemas"]["CatalogLimits"];
            quota: components["schemas"]["QuotaSettings"];
            retention: components["schemas"]["RetentionSettings"];
            revision: components["schemas"]["Revision"];
        };
        Tag: string;
        Tags: components["schemas"]["Tag"][];
        TagSelector: {
            all_tags: components["schemas"]["Tags"];
            any_tags: components["schemas"]["Tags"];
            none_tags: components["schemas"]["Tags"];
        };
        TargetKey: string;
        TargetRef: components["schemas"]["RoutingResourceRef"] | components["schemas"]["BuiltinRef"];
        /** @enum {string} */
        TerminalJobState: "succeeded" | "failed" | "canceled" | "timed_out";
        /** @description Parent snapshot. job_ids exactly match children; total equals child count and completed counts terminal children. All completed normally => succeeded, even if verdict=fail. Once children settle, infrastructure failure => failed; otherwise timeout => timed_out; otherwise cancellation => canceled. Aggregate verdict is fail if any conclusive child fails, pass if all pass, otherwise inconclusive. Unfinished batches omit aggregate verdict. Cancellation persists on every unfinished child and does not rewrite terminal results; completed may never exceed total. */
        TestBatch: {
            batch_id: components["schemas"]["UUID"];
            cancel_requested: boolean;
            children: components["schemas"]["Job"][];
            completed: number;
            created_at: components["schemas"]["Timestamp"];
            effective_limits: components["schemas"]["TestLimits"];
            job_ids: components["schemas"]["UUID"][];
            revision: components["schemas"]["Revision"];
            /** @enum {string} */
            state: "queued" | "running" | "succeeded" | "failed" | "canceled" | "timed_out";
            total: number;
            verdict?: components["schemas"]["Verdict"];
        };
        TestBatchResponse: {
            data: components["schemas"]["TestBatch"];
            request_id: components["schemas"]["RequestID"];
        };
        /** @description Only enabled registered target IDs are accepted for network tests; no arbitrary URL, native config, executable, shell or filesystem path. The service freezes current subject revisions, epochs, approved target revision and exact build. */
        TestCreateRequest: {
            core_build_id: components["schemas"]["UUID"];
            limits: components["schemas"]["TestLimits"];
            subjects: components["schemas"]["TestSubject"][];
            test_target_id?: components["schemas"]["UUID"];
            type: components["schemas"]["RunnerJobType"];
        } & unknown;
        /** @description Requested or server-adopted upper bounds. Server clamps to configured system/target quotas and atomically reserves the attempt budget. Byte counts are application bytes. Integers remain within JavaScript safe range. */
        TestLimits: {
            duration_ms: number;
            max_bytes: number;
        };
        /** @description Only measured fields are present. Throughput=body_bytes*8/body_duration_seconds/1,000,000; omit when duration<1 second or bytes below the configured threshold and report inconclusive/INSUFFICIENT_SAMPLE. Timing is monotonic and explicitly scoped; HTTP timings are not ICMP Ping. Three small independent samples use a median, never a synthetic P95. */
        TestMetrics: {
            body_bytes?: number;
            body_duration_ms?: number;
            config_check_ms?: number;
            core_start_ms?: number;
            egress_ip?: components["schemas"]["IPAddress"];
            failure_count?: number;
            http_total_ms?: number;
            http_ttfb_ms?: number;
            proxy_dial_ms?: number;
            sample_count?: number;
            target_tls_ms?: number;
            throughput_mbps?: number;
        };
        /** @description Frozen evidence uses the tested subject revision; stale is a live comparison with the current subject. Missing/expired network samples never become publication gates automatically. */
        TestResult: {
            attempt: number;
            batch_id: components["schemas"]["UUID"];
            completed_at: components["schemas"]["Timestamp"];
            core_build_id: components["schemas"]["UUID"];
            core_build_sha256: components["schemas"]["SHA256"];
            cpu_throttled: boolean;
            current_subject_revision?: components["schemas"]["Revision"];
            effective_limits: components["schemas"]["TestLimits"];
            error?: components["schemas"]["SafeError"];
            job_id: components["schemas"]["UUID"];
            location: components["schemas"]["ShortText"];
            metrics: components["schemas"]["TestMetrics"];
            result_id: components["schemas"]["UUID"];
            runner_id: components["schemas"]["UUID"];
            stale: boolean;
            state: components["schemas"]["TerminalJobState"];
            subject: components["schemas"]["FrozenTestSubject"];
            test_target_id?: components["schemas"]["UUID"];
            test_target_revision?: components["schemas"]["Revision"];
            /** @enum {string} */
            truncated_by: "none" | "duration" | "bytes";
            type: components["schemas"]["RunnerJobType"];
            verdict?: components["schemas"]["Verdict"];
        };
        TestResultListResponse: {
            data: components["schemas"]["TestResult"][];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        TestSubject: {
            id: components["schemas"]["UUID"];
            /** @enum {string} */
            kind: "node" | "chain";
        };
        TestTarget: {
            created_at: components["schemas"]["Timestamp"];
            diagnostics: components["schemas"]["Diagnostics"];
            enabled: boolean;
            name: components["schemas"]["Name"];
            revision: components["schemas"]["Revision"];
            safety_checked_at?: components["schemas"]["Timestamp"];
            /** @enum {string} */
            safety_state: "pending" | "approved" | "rejected";
            target: components["schemas"]["TestTargetConfig"];
            test_target_id: components["schemas"]["UUID"];
        };
        TestTargetConfig: {
            allowed_types: ("connectivity" | "download_throughput")[];
            /** @constant */
            compression: "disabled";
            expected_response: components["schemas"]["HTTPExpectation"];
            limits: components["schemas"]["TestLimits"];
            /** @enum {string} */
            permission_basis: "self_owned" | "explicitly_authorized";
            /** @constant */
            redirect_policy: "deny";
            url: components["schemas"]["HTTPSURL"];
            /** @constant */
            verify_certificate: true;
        };
        TestTargetCreateRequest: {
            enabled?: boolean;
            name: components["schemas"]["Name"];
            target: components["schemas"]["TestTargetConfig"];
        };
        TestTargetListResponse: {
            data: components["schemas"]["TestTarget"][];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        TestTargetPatch: {
            allowed_types?: ("connectivity" | "download_throughput")[];
            /** @constant */
            compression?: "disabled";
            expected_response?: components["schemas"]["HTTPExpectation"];
            limits?: components["schemas"]["TestLimits"];
            /** @enum {string} */
            permission_basis?: "self_owned" | "explicitly_authorized";
            /** @constant */
            redirect_policy?: "deny";
            url?: components["schemas"]["HTTPSURL"];
            /** @constant */
            verify_certificate?: true;
        };
        TestTargetPatchRequest: {
            enabled?: boolean;
            name?: components["schemas"]["Name"];
            target?: components["schemas"]["TestTargetPatch"];
        };
        TestTargetResponse: {
            data: components["schemas"]["TestTarget"];
            request_id: components["schemas"]["RequestID"];
        };
        /** Format: date-time */
        Timestamp: string;
        TLSSecurity: {
            alpn?: components["schemas"]["ALPN"];
            client_fingerprint?: components["schemas"]["Fingerprint"];
            /** @constant */
            mode: "tls";
            server_name: components["schemas"]["Host"];
            verify_certificate: boolean;
        };
        TLSSecurityPatch: {
            alpn?: components["schemas"]["ALPNPatch"];
            client_fingerprint?: components["schemas"]["FingerprintPatch"];
            /** @constant */
            mode: "tls";
            server_name?: components["schemas"]["Host"];
            verify_certificate?: boolean;
        };
        /** @description Expiry omitted means no configured expiry, subject to system policy. Target keys are checked at issuance and on every download. Token creation has no resource If-Match. */
        TokenCreateRequest: {
            allowed_targets: components["schemas"]["TargetKey"][];
            expires_at?: components["schemas"]["Timestamp"];
            name: components["schemas"]["Name"];
        };
        TokenIssueData: {
            metadata: components["schemas"]["TokenMetadata"];
            replayed: boolean;
            token?: components["schemas"]["SubscriptionToken"];
        } & unknown;
        TokenIssueResponse: {
            data: components["schemas"]["TokenIssueData"];
            request_id: components["schemas"]["RequestID"];
        };
        TokenListResponse: {
            data: components["schemas"]["TokenMetadata"][];
            page: components["schemas"]["PageInfo"];
            request_id: components["schemas"]["RequestID"];
        };
        TokenMetadata: {
            allowed_targets: components["schemas"]["TargetKey"][];
            created_at: components["schemas"]["Timestamp"];
            expires_at?: components["schemas"]["Timestamp"];
            last_accessed_at?: components["schemas"]["Timestamp"];
            name: components["schemas"]["Name"];
            revision: components["schemas"]["Revision"];
            revoked_at?: components["schemas"]["Timestamp"];
            /** @enum {string} */
            state: "active" | "expired" | "revoked";
            subscription_id: components["schemas"]["UUID"];
            token_id: components["schemas"]["UUID"];
        };
        TokenResponse: {
            data: components["schemas"]["TokenMetadata"];
            request_id: components["schemas"]["RequestID"];
        };
        UDPBootstrapResolver: {
            address: components["schemas"]["IPAddress"];
            /** @constant */
            kind: "udp";
            port: number;
            resolver_id: components["schemas"]["TargetKey"];
        };
        UDPDNSResolver: {
            address: components["schemas"]["IPAddress"];
            /** @constant */
            kind: "udp";
            outbound: components["schemas"]["TargetRef"];
            port: number;
            resolver_id: components["schemas"]["TargetKey"];
        };
        UsernamePasswordAuth: {
            /** @constant */
            kind: "username_password";
            password: components["schemas"]["Secret"];
            username: components["schemas"]["Secret"];
        };
        UsernamePasswordAuthPatch: {
            /** @constant */
            kind: "username_password";
            password?: components["schemas"]["SecretPatch"];
            username?: components["schemas"]["SecretPatch"];
        };
        UsernamePasswordAuthRedacted: {
            has_password: boolean;
            has_username: boolean;
            /** @constant */
            kind: "username_password";
        };
        UUID: string;
        UUIDAuth: {
            /** @constant */
            kind: "uuid";
            uuid: components["schemas"]["SecretUUID"];
        };
        UUIDAuthPatch: {
            /** @constant */
            kind: "uuid";
            uuid?: components["schemas"]["SecretUUIDPatch"];
        };
        UUIDAuthRedacted: {
            has_uuid: boolean;
            /** @constant */
            kind: "uuid";
        };
        /** @enum {string} */
        Verdict: "pass" | "fail" | "inconclusive";
        VMessAuth: {
            cipher: components["schemas"]["VMessCipher"];
            /** @constant */
            kind: "vmess_aead";
            uuid: components["schemas"]["SecretUUID"];
        };
        VMessAuthPatch: {
            cipher?: components["schemas"]["VMessCipher"];
            /** @constant */
            kind: "vmess_aead";
            uuid?: components["schemas"]["SecretUUIDPatch"];
        };
        VMessAuthRedacted: {
            cipher: components["schemas"]["VMessCipher"];
            has_uuid: boolean;
            /** @constant */
            kind: "vmess_aead";
        };
        /** @enum {string} */
        VMessCipher: "auto" | "aes-128-gcm" | "chacha20-poly1305" | "none" | "zero";
        WebSocketTransport: {
            host?: components["schemas"]["Host"];
            /** @constant */
            kind: "websocket";
            path: string;
        };
    };
    responses: {
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        Acknowledgement: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["AcknowledgementResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        AuditEventListResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["AuditEventListResponse"];
            };
        };
        /** @description Malformed JSON, duplicate/unknown field, unsafe shape, invalid path/query/header, invalid Unicode or cursor. No submitted key or value is echoed. */
        BadRequest: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        CapabilityListResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["CapabilityListResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        ChainListResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ChainListResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        ChainResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ChainReadResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        ClientPresetListResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ClientPresetListResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        CompileBatchResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["CompileBatchResponse"];
            };
        };
        /** @description Business state, generation, idempotency or lease conflict, including COMPILE_OBSOLETE, IDEMPOTENCY_CONFLICT and LEASE_LOST. A stale resource revision is 412 instead. */
        Conflict: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        CoreListResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["CoreListResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        CoreResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["CoreResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        DNSProfileListResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["DNSProfileListResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        DNSProfileResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["DNSProfileResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        ExportResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ExportResponse"];
            };
        };
        /** @description Authenticated actor lacks permission, recent authentication, a valid current-actor confirmation or required same-origin protection. */
        Forbidden: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        ImportAccepted: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ImportAcceptedResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        ImportCommitResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ImportCommitResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        ImportResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ImportResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        JobListResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["JobListResponse"];
            };
        };
        /** @description Persistent child JobResponse or aggregate parent TestBatchResponse, selected by the UUID kind. */
        JobOrBatchResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["JobResponse"] | components["schemas"]["TestBatchResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        JobResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["JobResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        MutationResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["MutationResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        NodeBatchResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["NodeBatchResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        NodeListResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["NodeListResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        NodeResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["NodeReadResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        NodeRevealResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["NodeRevealResponse"];
            };
        };
        /** @description Resource does not exist in the authorized scope, or existence must not be disclosed. */
        NotFound: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        PolicyGroupListResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["PolicyGroupListResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        PolicyGroupResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["PolicyGroupResponse"];
            };
        };
        /** @description A syntactically valid If-Match names an outdated resource revision; no mutation was applied. */
        PreconditionFailed: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"];
            };
        };
        /** @description The versioned resource operation requires If-Match but it was omitted. */
        PreconditionRequired: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        PublicationListResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["PublicationListResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        PublicationResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["PublicationResponse"];
            };
        };
        /** @description Identical generic 404 for invalid, expired, revoked or target-unauthorized token; never reveals whether the token or profile exists. */
        PublicNotFound: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Content-Type-Options"?: "nosniff";
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"];
                "text/plain": string;
            };
        };
        /** @description Public request limit exceeded; response is an error, never proxy configuration. */
        PublicRateLimited: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Content-Type-Options"?: "nosniff";
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"];
                "text/plain": string;
            };
        };
        /** @description Authorized token has no safe publication, publication is blocked, or primary database is unavailable. No cached authorization, empty config or direct fallback. */
        PublicUnavailable: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Content-Type-Options"?: "nosniff";
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"];
                "text/plain": string;
            };
        };
        /** @description Rate, slot, concurrent execution or reserved budget limit exceeded. */
        RateLimited: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "Retry-After": components["headers"]["RetryAfter"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        ReferenceListResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ReferenceListResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        RoutingProfileListResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["RoutingProfileListResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        RoutingProfileResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["RoutingProfileResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        RuleSetListResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["RuleSetListResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        RuleSetResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["RuleSetResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        RunnerHeartbeatResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["RunnerHeartbeatResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        RunnerJobEventResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["RunnerJobEventResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        RunnerJobHeartbeatResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["RunnerJobHeartbeatResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        RunnerJobResultResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["RunnerJobResultResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        RunnerLeaseResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["RunnerLeaseResponse"];
            };
        };
        /** @description Authenticated current user and CSRF token; no-store. Session cookie is set on setup/login and refreshed without extending absolute expiry. */
        SessionResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                /** @description Set or rotate proxyloom_session with Secure; HttpOnly; SameSite=Lax. Session identifiers are never response-body fields. */
                "Set-Cookie"?: string;
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["SessionResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        SettingsResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["SettingsResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        SourceListResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["SourceListResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        SourceResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["SourceResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        SubscriptionListResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["SubscriptionListResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        SubscriptionResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["SubscriptionResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        TestBatchResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["TestBatchResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        TestResultListResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["TestResultListResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        TestTargetListResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["TestTargetListResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        TestTargetResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["TestTargetResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        TokenIssueResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["TokenIssueResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        TokenListResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["TokenListResponse"];
            };
        };
        /** @description Typed contract response. This operation is not registered by the foundation contract implementation. */
        TokenResponse: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                ETag: components["headers"]["ETag"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["TokenResponse"];
            };
        };
        /** @description Input, decompressed data, item count or event size exceeds the configured bound. */
        TooLarge: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"];
            };
        };
        /** @description Required management session or registered internal identity is missing, invalid or expired; surfaces are not interchangeable. */
        Unauthorized: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"];
            };
        };
        /** @description Primary database, execution capability or safe publication is unavailable. Fail closed; never serve cached authorization or substitute a direct/empty configuration. */
        Unavailable: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "Retry-After": components["headers"]["RetryAfter"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"];
            };
        };
        /** @description Shape is understood but merged domain semantics are invalid, including required secret clear, unsupported capability, invalid references, excluded dependencies and DNS cycles. */
        UnprocessableEntity: {
            headers: {
                "Cache-Control": components["headers"]["NoStore"];
                "X-Request-ID": components["headers"]["RequestID"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"];
            };
        };
    };
    parameters: {
        CoreBuildID: components["schemas"]["UUID"];
        CoreFamily: components["schemas"]["CoreFamily"];
        /** @description Session-bound CSRF token returned by setup/login/me/reauth. Used together with an exact Origin check. */
        CSRFToken: string;
        /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
        Cursor: string;
        Enabled: boolean;
        /** @description Inclusive UTC time boundary; bound into the cursor filter digest. */
        FromTime: components["schemas"]["Timestamp"];
        ID: components["schemas"]["UUID"];
        /** @description Supported on asynchronous creation, publication, token issuance and import commit. One key per authenticated actor + fixed route; request HMAC also binds scope and canonical body. Same key/body waits then replays permitted metadata; changed body is 409. Mutations and idempotency registration commit in the same transaction. No arbitrary response bytes or one-time secrets are replayed. */
        IdempotencyKey: string;
        /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
        IfMatch: string;
        /** @description One canonical decimal sequence from the prior SSE id. Zero starts at the beginning. The server replays greater seq values; an expired retention window emits snapshot_reset and requires GET /jobs/{id}. */
        LastEventID: components["schemas"]["Counter"];
        /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
        Limit: number;
        /** @description Case-insensitive node name search; secrets are never searched or echoed. */
        NodeSearch: string;
        Protocol: components["schemas"]["Protocol"];
        Tag: components["schemas"]["Tag"];
        /** @description Exclusive UTC time boundary; must follow from when both are supplied. */
        UntilTime: components["schemas"]["Timestamp"];
    };
    requestBodies: never;
    headers: {
        /** @description Strong resource revision tag "r<N>". Exactly one tag, no wildcard, weak tag, list, sign, whitespace or leading zero. N fits positive int64. */
        ETag: string;
        NoStore: "private, no-store";
        /** @description Server-generated safe request correlation ID. */
        RequestID: components["schemas"]["RequestID"];
        /** @description Bounded retry delay in seconds. */
        RetryAfter: number;
    };
    pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
    listAuditEvents: {
        parameters: {
            query?: {
                action?: components["schemas"]["AuditAction"];
                actor_id?: components["schemas"]["UUID"];
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Inclusive UTC time boundary; bound into the cursor filter digest. */
                from?: components["parameters"]["FromTime"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
                resource_id?: components["schemas"]["UUID"];
                /** @description Exclusive UTC time boundary; must follow from when both are supplied. */
                until?: components["parameters"]["UntilTime"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["AuditEventListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            503: components["responses"]["Unavailable"];
        };
    };
    login: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["LoginRequest"];
            };
        };
        responses: {
            200: components["responses"]["SessionResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            413: components["responses"]["TooLarge"];
            422: components["responses"]["UnprocessableEntity"];
            429: components["responses"]["RateLimited"];
            503: components["responses"]["Unavailable"];
        };
    };
    logout: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound CSRF token returned by setup/login/me/reauth. Used together with an exact Origin check. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody?: {
            content: {
                "application/json": components["schemas"]["LogoutRequest"];
            };
        };
        responses: {
            200: components["responses"]["Acknowledgement"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            413: components["responses"]["TooLarge"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["Unavailable"];
        };
    };
    getCurrentUser: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["SessionResponse"];
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["Unavailable"];
        };
    };
    reauthenticate: {
        parameters: {
            query?: never;
            header: {
                /** @description Session-bound CSRF token returned by setup/login/me/reauth. Used together with an exact Origin check. */
                "X-CSRF-Token": components["parameters"]["CSRFToken"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReauthenticationRequest"];
            };
        };
        responses: {
            200: components["responses"]["SessionResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            413: components["responses"]["TooLarge"];
            422: components["responses"]["UnprocessableEntity"];
            429: components["responses"]["RateLimited"];
            503: components["responses"]["Unavailable"];
        };
    };
    listCapabilities: {
        parameters: {
            query?: {
                client_preset_id?: components["schemas"]["UUID"];
                core_build_id?: components["parameters"]["CoreBuildID"];
                core_family?: components["parameters"]["CoreFamily"];
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
                protocol?: components["parameters"]["Protocol"];
                status?: components["schemas"]["CapabilityStatus"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["CapabilityListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["Unavailable"];
        };
    };
    listChains: {
        parameters: {
            query?: {
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                enabled?: components["parameters"]["Enabled"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
                tag?: components["parameters"]["Tag"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["ChainListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["Unavailable"];
        };
    };
    createChain: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ChainCreateRequest"];
            };
        };
        responses: {
            201: components["responses"]["ChainResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["Unavailable"];
        };
    };
    getChain: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["ChainResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    deleteChain: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["MutationResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    updateChain: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ChainPatchRequest"];
            };
        };
        responses: {
            200: components["responses"]["ChainResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    listClientPresets: {
        parameters: {
            query?: {
                core_family?: components["parameters"]["CoreFamily"];
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
                platform?: components["schemas"]["Platform"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["ClientPresetListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["Unavailable"];
        };
    };
    getCompileBatch: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["CompileBatchResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    listCores: {
        parameters: {
            query?: {
                core_family?: components["parameters"]["CoreFamily"];
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                enabled?: components["parameters"]["Enabled"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["CoreListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["Unavailable"];
        };
    };
    disableCore: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReasonRequest"];
            };
        };
        responses: {
            200: components["responses"]["CoreResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    listDNSProfiles: {
        parameters: {
            query?: {
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                enabled?: components["parameters"]["Enabled"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
                tag?: components["parameters"]["Tag"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["DNSProfileListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["Unavailable"];
        };
    };
    createDNSProfile: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["DNSProfileCreateRequest"];
            };
        };
        responses: {
            201: components["responses"]["DNSProfileResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["Unavailable"];
        };
    };
    getDNSProfile: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["DNSProfileResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    deleteDNSProfile: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["MutationResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    updateDNSProfile: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["DNSProfilePatchRequest"];
            };
        };
        responses: {
            200: components["responses"]["DNSProfileResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    createExport: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ExportRequest"];
            };
        };
        responses: {
            200: components["responses"]["ExportResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            429: components["responses"]["RateLimited"];
            503: components["responses"]["Unavailable"];
        };
    };
    createImport: {
        parameters: {
            query?: never;
            header?: {
                /** @description Supported on asynchronous creation, publication, token issuance and import commit. One key per authenticated actor + fixed route; request HMAC also binds scope and canonical body. Same key/body waits then replays permitted metadata; changed body is 409. Mutations and idempotency registration commit in the same transaction. No arbitrary response bytes or one-time secrets are replayed. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ImportCreateRequest"];
                "multipart/form-data": components["schemas"]["ImportFileRequest"];
            };
        };
        responses: {
            202: components["responses"]["ImportAccepted"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            413: components["responses"]["TooLarge"];
            422: components["responses"]["UnprocessableEntity"];
            429: components["responses"]["RateLimited"];
            503: components["responses"]["Unavailable"];
        };
    };
    getImport: {
        parameters: {
            query?: {
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["ImportResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    commitImport: {
        parameters: {
            query?: never;
            header: {
                /** @description Supported on asynchronous creation, publication, token issuance and import commit. One key per authenticated actor + fixed route; request HMAC also binds scope and canonical body. Same key/body waits then replays permitted metadata; changed body is 409. Mutations and idempotency registration commit in the same transaction. No arbitrary response bytes or one-time secrets are replayed. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ImportCommitRequest"];
            };
        };
        responses: {
            200: components["responses"]["ImportCommitResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    listJobs: {
        parameters: {
            query?: {
                batch_id?: components["schemas"]["UUID"];
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                executor?: "api_worker" | "runner";
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
                state?: components["schemas"]["JobState"];
                type?: components["schemas"]["JobType"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["JobListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["Unavailable"];
        };
    };
    getJob: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["JobOrBatchResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    cancelJob: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReasonRequest"];
            };
        };
        responses: {
            202: components["responses"]["JobOrBatchResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    streamJobEvents: {
        parameters: {
            query?: never;
            header?: {
                /** @description One canonical decimal sequence from the prior SSE id. Zero starts at the beginning. The server replays greater seq values; an expired retention window emits snapshot_reset and requires GET /jobs/{id}. */
                "Last-Event-ID"?: components["parameters"]["LastEventID"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Authenticated event stream; typed JSON data is framed by SSE event and id fields. */
            200: {
                headers: {
                    "Cache-Control"?: "private, no-store";
                    [name: string]: unknown;
                };
                content: {
                    "text/event-stream": string;
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            429: components["responses"]["RateLimited"];
            503: components["responses"]["Unavailable"];
        };
    };
    listNodes: {
        parameters: {
            query?: {
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                enabled?: components["parameters"]["Enabled"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
                protocol?: components["parameters"]["Protocol"];
                /** @description Case-insensitive node name search; secrets are never searched or echoed. */
                q?: components["parameters"]["NodeSearch"];
                tag?: components["parameters"]["Tag"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["NodeListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["Unavailable"];
        };
    };
    createNode: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["NodeCreateRequest"];
            };
        };
        responses: {
            201: components["responses"]["NodeResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            413: components["responses"]["TooLarge"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["Unavailable"];
        };
    };
    getNode: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["NodeResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    deleteNode: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["MutationResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    updateNode: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["NodePatchRequest"];
            };
        };
        responses: {
            200: components["responses"]["NodeResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    cloneNode: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["NodeCloneRequest"];
            };
        };
        responses: {
            201: components["responses"]["NodeResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    listNodeReferences: {
        parameters: {
            query?: {
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
                reference_state?: "active" | "historical" | "all";
            };
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["ReferenceListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    revealNodeSecrets: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["NodeRevealResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
            429: components["responses"]["RateLimited"];
            503: components["responses"]["Unavailable"];
        };
    };
    listNodeRevisions: {
        parameters: {
            query?: {
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["NodeListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    updateNodesBatch: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["NodeBatchRequest"];
            };
        };
        responses: {
            200: components["responses"]["NodeBatchResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            413: components["responses"]["TooLarge"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["Unavailable"];
        };
    };
    listPolicyGroups: {
        parameters: {
            query?: {
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                enabled?: components["parameters"]["Enabled"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
                tag?: components["parameters"]["Tag"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["PolicyGroupListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["Unavailable"];
        };
    };
    createPolicyGroup: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["PolicyGroupCreateRequest"];
            };
        };
        responses: {
            201: components["responses"]["PolicyGroupResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["Unavailable"];
        };
    };
    getPolicyGroup: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["PolicyGroupResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    deletePolicyGroup: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["MutationResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    updatePolicyGroup: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["PolicyGroupPatchRequest"];
            };
        };
        responses: {
            200: components["responses"]["PolicyGroupResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    listRoutingProfiles: {
        parameters: {
            query?: {
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                enabled?: components["parameters"]["Enabled"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
                tag?: components["parameters"]["Tag"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RoutingProfileListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["Unavailable"];
        };
    };
    createRoutingProfile: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RoutingProfileCreateRequest"];
            };
        };
        responses: {
            201: components["responses"]["RoutingProfileResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["Unavailable"];
        };
    };
    getRoutingProfile: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RoutingProfileResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    deleteRoutingProfile: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["MutationResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    updateRoutingProfile: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RoutingProfilePatchRequest"];
            };
        };
        responses: {
            200: components["responses"]["RoutingProfileResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    listRuleSets: {
        parameters: {
            query?: {
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                enabled?: components["parameters"]["Enabled"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
                tag?: components["parameters"]["Tag"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RuleSetListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["Unavailable"];
        };
    };
    createRuleSet: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RuleSetCreateRequest"];
            };
        };
        responses: {
            201: components["responses"]["RuleSetResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            413: components["responses"]["TooLarge"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["Unavailable"];
        };
    };
    getRuleSet: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["RuleSetResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    deleteRuleSet: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["MutationResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    updateRuleSet: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RuleSetPatchRequest"];
            };
        };
        responses: {
            200: components["responses"]["RuleSetResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            413: components["responses"]["TooLarge"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    setup: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["SetupRequest"];
            };
        };
        responses: {
            201: components["responses"]["SessionResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            413: components["responses"]["TooLarge"];
            422: components["responses"]["UnprocessableEntity"];
            429: components["responses"]["RateLimited"];
            503: components["responses"]["Unavailable"];
        };
    };
    listSources: {
        parameters: {
            query?: {
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                enabled?: components["parameters"]["Enabled"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
                tag?: components["parameters"]["Tag"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["SourceListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["Unavailable"];
        };
    };
    createSource: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["SourceCreateRequest"];
            };
        };
        responses: {
            201: components["responses"]["SourceResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["Unavailable"];
        };
    };
    getSource: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["SourceResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    deleteSource: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["MutationResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    updateSource: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["SourcePatchRequest"];
            };
        };
        responses: {
            200: components["responses"]["SourceResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    refreshSource: {
        parameters: {
            query?: never;
            header: {
                /** @description Supported on asynchronous creation, publication, token issuance and import commit. One key per authenticated actor + fixed route; request HMAC also binds scope and canonical body. Same key/body waits then replays permitted metadata; changed body is 409. Mutations and idempotency registration commit in the same transaction. No arbitrary response bytes or one-time secrets are replayed. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            202: components["responses"]["JobResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
            429: components["responses"]["RateLimited"];
            503: components["responses"]["Unavailable"];
        };
    };
    listSubscriptions: {
        parameters: {
            query?: {
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                enabled?: components["parameters"]["Enabled"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
                tag?: components["parameters"]["Tag"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["SubscriptionListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["Unavailable"];
        };
    };
    createSubscription: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["SubscriptionCreateRequest"];
            };
        };
        responses: {
            201: components["responses"]["SubscriptionResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["Unavailable"];
        };
    };
    getSubscription: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["SubscriptionResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    deleteSubscription: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["MutationResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    updateSubscription: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["SubscriptionPatchRequest"];
            };
        };
        responses: {
            200: components["responses"]["SubscriptionResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    compileSubscription: {
        parameters: {
            query?: never;
            header: {
                /** @description Supported on asynchronous creation, publication, token issuance and import commit. One key per authenticated actor + fixed route; request HMAC also binds scope and canonical body. Same key/body waits then replays permitted metadata; changed body is 409. Mutations and idempotency registration commit in the same transaction. No arbitrary response bytes or one-time secrets are replayed. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CompileRequest"];
            };
        };
        responses: {
            202: components["responses"]["CompileBatchResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
            429: components["responses"]["RateLimited"];
            503: components["responses"]["Unavailable"];
        };
    };
    listPublications: {
        parameters: {
            query?: {
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["PublicationListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    publishSubscription: {
        parameters: {
            query?: never;
            header: {
                /** @description Supported on asynchronous creation, publication, token issuance and import commit. One key per authenticated actor + fixed route; request HMAC also binds scope and canonical body. Same key/body waits then replays permitted metadata; changed body is 409. Mutations and idempotency registration commit in the same transaction. No arbitrary response bytes or one-time secrets are replayed. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["PublishRequest"];
            };
        };
        responses: {
            201: components["responses"]["PublicationResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    rollbackSubscription: {
        parameters: {
            query?: never;
            header: {
                /** @description Supported on asynchronous creation, publication, token issuance and import commit. One key per authenticated actor + fixed route; request HMAC also binds scope and canonical body. Same key/body waits then replays permitted metadata; changed body is 409. Mutations and idempotency registration commit in the same transaction. No arbitrary response bytes or one-time secrets are replayed. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RollbackRequest"];
            };
        };
        responses: {
            201: components["responses"]["PublicationResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    listSubscriptionTokens: {
        parameters: {
            query?: {
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["TokenListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    issueSubscriptionToken: {
        parameters: {
            query?: never;
            header?: {
                /** @description Supported on asynchronous creation, publication, token issuance and import commit. One key per authenticated actor + fixed route; request HMAC also binds scope and canonical body. Same key/body waits then replays permitted metadata; changed body is 409. Mutations and idempotency registration commit in the same transaction. No arbitrary response bytes or one-time secrets are replayed. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["TokenCreateRequest"];
            };
        };
        responses: {
            201: components["responses"]["TokenIssueResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            429: components["responses"]["RateLimited"];
            503: components["responses"]["Unavailable"];
        };
    };
    getSystemSettings: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["SettingsResponse"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            503: components["responses"]["Unavailable"];
        };
    };
    updateSystemSettings: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["SettingsPatchRequest"];
            };
        };
        responses: {
            200: components["responses"]["SettingsResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    listTestResults: {
        parameters: {
            query?: {
                core_build_id?: components["parameters"]["CoreBuildID"];
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Inclusive UTC time boundary; bound into the cursor filter digest. */
                from?: components["parameters"]["FromTime"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
                location?: components["schemas"]["ShortText"];
                subject_id?: components["schemas"]["UUID"];
                subject_revision?: components["schemas"]["Revision"];
                /** @description Exclusive UTC time boundary; must follow from when both are supplied. */
                until?: components["parameters"]["UntilTime"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["TestResultListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["Unavailable"];
        };
    };
    listTestTargets: {
        parameters: {
            query?: {
                /** @description Opaque authenticated continuation tied to scope, collection, order and normalized filter digest. A changed filter or invalid cursor is 400; authorization is checked separately on every request. */
                cursor?: components["parameters"]["Cursor"];
                enabled?: components["parameters"]["Enabled"];
                /** @description Bounded page size. Stable ascending (created_at, id) order unless a collection documents its immutable equivalent. */
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["TestTargetListResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["Unavailable"];
        };
    };
    createTestTarget: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["TestTargetCreateRequest"];
            };
        };
        responses: {
            201: components["responses"]["TestTargetResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["Unavailable"];
        };
    };
    getTestTarget: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["TestTargetResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["Unavailable"];
        };
    };
    deleteTestTarget: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: components["responses"]["MutationResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    updateTestTarget: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["TestTargetPatchRequest"];
            };
        };
        responses: {
            200: components["responses"]["TestTargetResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    createTestBatch: {
        parameters: {
            query?: never;
            header?: {
                /** @description Supported on asynchronous creation, publication, token issuance and import commit. One key per authenticated actor + fixed route; request HMAC also binds scope and canonical body. Same key/body waits then replays permitted metadata; changed body is 409. Mutations and idempotency registration commit in the same transaction. No arbitrary response bytes or one-time secrets are replayed. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["TestCreateRequest"];
            };
        };
        responses: {
            202: components["responses"]["TestBatchResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            429: components["responses"]["RateLimited"];
            503: components["responses"]["Unavailable"];
        };
    };
    revokeToken: {
        parameters: {
            query?: never;
            header: {
                /** @description Exactly one strong tag "r<N>" for the resource being changed. Missing => 428; malformed, weak, wildcard, multi-tag or overflow => 400; valid stale revision => 412. Business/generation/lease/idempotency conflicts => 409. Creates and authentication operations have no resource precondition. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReasonRequest"];
            };
        };
        responses: {
            200: components["responses"]["TokenResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
            503: components["responses"]["Unavailable"];
        };
    };
    submitRunnerJobEvent: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RunnerJobEventRequest"];
            };
        };
        responses: {
            200: components["responses"]["RunnerJobEventResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            413: components["responses"]["TooLarge"];
            422: components["responses"]["UnprocessableEntity"];
            429: components["responses"]["RateLimited"];
            503: components["responses"]["Unavailable"];
        };
    };
    heartbeatRunnerJob: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RunnerJobHeartbeatRequest"];
            };
        };
        responses: {
            200: components["responses"]["RunnerJobHeartbeatResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            503: components["responses"]["Unavailable"];
        };
    };
    submitRunnerJobResult: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RunnerJobResultRequest"];
            };
        };
        responses: {
            200: components["responses"]["RunnerJobResultResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            413: components["responses"]["TooLarge"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["Unavailable"];
        };
    };
    leaseRunnerJob: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RunnerLeaseRequest"];
            };
        };
        responses: {
            200: components["responses"]["RunnerLeaseResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            429: components["responses"]["RateLimited"];
            503: components["responses"]["Unavailable"];
        };
    };
    heartbeatRunner: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RunnerHeartbeatRequest"];
            };
        };
        responses: {
            200: components["responses"]["RunnerHeartbeatResponse"];
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            429: components["responses"]["RateLimited"];
            503: components["responses"]["Unavailable"];
        };
    };
    getPublishedSubscription: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                target_key: components["schemas"]["TargetKey"];
                token: components["schemas"]["SubscriptionToken"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Exact frozen artifact bytes for one authorized target; no JSON response envelope. */
            200: {
                headers: {
                    "Cache-Control"?: "private, no-store";
                    "X-Content-Type-Options"?: "nosniff";
                    "X-Request-ID": components["headers"]["RequestID"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": string;
                    "application/yaml": string;
                    "text/plain": string;
                };
            };
            404: components["responses"]["PublicNotFound"];
            429: components["responses"]["PublicRateLimited"];
            503: components["responses"]["PublicUnavailable"];
        };
    };
}
