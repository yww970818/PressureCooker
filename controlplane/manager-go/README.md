# Go manager

The manager is implemented in Go and uses MySQL as its only live database. It does not invoke the previous Python backend. Controller and worker execution agents are implemented in Go under `../execution`; Apache JMeter runs on its official JVM runtime, with the thread-observation listener as a JMeter extension. Custom frontend code is built separately with Vue 3. The separate Go MCP proxy runs the upstream npm package as a child process.

Build with Go 1.25.5:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o jmeter-manager .
go test -race ./...
```

The default command starts a supervisor. It restarts the manager child on exit or a stale scheduler heartbeat. With `JMETER_MCP_ENABLED=1`, it independently supervises `/app/bin/jmeter-mcp`. Shutdown terminates each process group. `--serve` runs the manager child directly and is intended for the supervisor.

## Configuration

Workflow capacity is configurable with `JMETER_WORKFLOW_MAX_NODES` (default 5000, maximum 100000), `JMETER_WORKFLOW_MAX_EDGES` (default 10000, maximum 200000), and `JMETER_WORKFLOW_BODY_MIB` (default 32, maximum 64). The workflow API body, encrypted workspace input, JMX import, compilation, publication and snapshot preview use this authoring budget. Invalid values fail startup. Other API bodies stay at 6 MiB and direct standalone plan uploads keep their 5 MiB cap. The Vue canvas uses LogicFlow viewport rendering while retaining every graph node. Higher settings are admission limits, not a claim that the browser or load generators have passed those scales. An independently configured reverse proxy must allow the chosen request size. The source tree does not include a bundled gateway service.

Existing `JMETER_STATE_DIR`, `JMETER_PLANS_DIR`, `JMETER_RESULTS_DIR`, agent URL maps, admin/session/agent secret file settings, scheduler timeouts, archive settings, and frontend directory retain their previous names. The HTTP listener defaults to `0.0.0.0:8080` and honors `JMETER_MANAGER_HOST` / `JMETER_MANAGER_PORT`.

Authenticated `GET /api/mcp/tools` exposes connection metadata and standard MCP tool descriptors for the frontend information page. `JMETER_MCP_PUBLIC_URL` controls the displayed public endpoint. Discovery only dials `127.0.0.1:${JMETER_MCP_PORT:-8081}/mcp`, with a five second overall deadline, a 2 MiB response budget, and redirect following disabled. Discovery failures return HTTP 200 with `available: false`, an empty tool list, and a safe Chinese error. Credentials and raw upstream errors are never returned.

MySQL settings:

| Variable | Default |
| --- | --- |
| `JMETER_MYSQL_HOST` | `mysql` |
| `JMETER_MYSQL_PORT` | `3306` |
| `JMETER_MYSQL_DATABASE` | `jmeter` |
| `JMETER_MYSQL_USER` | `jmeter` |
| `JMETER_MYSQL_PASSWORD_FILE` | `/run/platform/mysql-password` |
| `JMETER_MYSQL_TIMEOUT` | `15` seconds |

The database must be provisioned in advance. The manager creates missing core and workspace InnoDB tables. IDs use binary collation; idempotency keys use `VARBINARY(800)` so trailing spaces and every UTF-8 byte remain significant. JSON data remains an opaque JSON object in `LONGTEXT`, preserving historical metadata fields. The connection pool has at most 12 open / 4 idle connections and a five minute connection lifetime. SQL transactions lock run rows before changing state; deadlock and unique-key races are retried.

## Commands

Workflow editor reads are opt-in: `GET /api/workflows/{id}?view=outline` and `POST /api/workflows/import?view=outline` return graph shells with `properties.editor_lazy=true`. `GET /api/workflows/{id}/nodes?revision=N&ids=ID,...` reads only the requested nodes from that exact revision; `GET /api/workflows/{id}/versions/N?view=editor` supplies complete revision content before save, preview or export. Detail responses use the existing `editor-text-v1` envelope. All reads retain project authorization and immutable revision semantics. Partial graphs are rejected on save/compile; normal full APIs retain their existing contract. These projections reduce browser transfer and rendering work; the server still decrypts the stored full revision to produce them.

- `health`: check local manager `/healthz` and, when enabled, local MCP `/healthz`.
- `db-check`: connect, ping, and print only current MySQL user, server version, and database.
- `init-minio`: create the configured private bucket using provisioned file credentials.
- `api health|cluster|plans|runs|run|artifacts|upload|start|stop|openapi`: manager and task operations; `api` defaults to localhost manager port, while `--url` and `--token-file` override it. `upload plans/foo.jmx` maps to `/plans/foo.jmx` in the container. The legacy `report-status` command reads historical report metadata; legacy `report` and `compare` return HTTP 410. Current structured reports use `/api/pressure-reports` and the `pressure_report` MCP tool family described in [the report contract](../../PRESSURE-REPORTS.md).
- `import-history <directory> --project PROJECT_ID`: copy original results/logs and existing HTML into the mounted data roots, register metadata in the explicitly selected active project, and skip already known run IDs. Historical engine IDs accept generic executor identities with an optional valid RMI port; they do not depend on the current registered topology.
- `export-state <file>`: consistent transactional JSON snapshot, excluding sessions and instance heartbeats.
- `import-state <file>`: transactional migration into empty business tables; refuses to overwrite existing rows.
- `backup <new-or-empty-directory>`: JSON state plus immutable plan snapshots, terminal run raw files, and READY historical reports, with a SHA-256 manifest. Credentials, sessions, heartbeats, and lock files are excluded.
- `restore <archive-directory> <new-or-empty-directory>`: validate all manifest checksums before copying. Prepare restored JSON with old in-flight runs marked `INTERRUPTED`, and raw receipts requeued as `PENDING`. Configure a separate empty MySQL database and run `import-state <restored-directory>/state/manager.json`; point the runtime at the restored plan/results directories.

SQLite is supported only as a migration source outside the runtime. Export its rows as JSON with top-level arrays `plans`, `runs`, `reports`, `idempotency`, and `audit`. Each item contains the original table columns. A `data` column may be either its original JSON string or a JSON object. Preserve array order for plans. Do not export sessions or live instances. Existing raw files, plan snapshots, and READY HTML stay in their mounted volumes.

## Behavior

Bearer auth and HMAC-hashed sessions retain the previous format; session writes require the CSRF header. Run idempotency hashes use Python-compatible canonical UTF-8 JSON, including literal HTML characters and Unicode separators. Plan versions are immutable SHA-256 snapshots with XML validation. Standalone plan uploads have a 5 MiB cap; workflow publication uses the separately configured authoring budget.

A file lock in the shared state volume elects one scheduler. Current Redis executions retain signed task/attempt identity, frozen worker membership and lease fencing across manager restarts. Recovery verifies the selected workers and the expired execution lease before releasing the physical execution barrier; unavailable or busy nodes do not count as completed cleanup. Legacy local-run job IDs and reset intents remain available for historical recovery.

New terminal runs archive the main `results.jtl`, `jmeter.log`, and `console.log` to `yyyy-mm-dd/<run-id>/raw/`, using the run start time (or creation time) in Asia/Shanghai. Per-node periodic thread observations are separate immutable originals with task/attempt/node/generation ownership; their date comes from node preparation and may differ across midnight. Nine-digit Java timestamps are accepted. S3 PUTs use SigV4 plus `If-None-Match: *`; existing objects are streamed and compared by SHA-256 before reuse. `JMETER_ARCHIVE_TIMEOUT` bounds connection setup, response headers, and periods without socket progress; it does not cap the total duration of a progressing upload or verification download. Local JTL/log download is authenticated and terminal-only; live log views are bounded and redacted. Versioned structured report jobs parse archived evidence into JTL metrics, analysis, comparisons and offline HTML through `/api/pressure-reports`; report reads do not implicitly generate a version. The retired legacy report-generation/comparison endpoints return 410, while historical READY report files remain readable.

## Integration tests

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o jmeter-manager-tests .
JMETER_GO_INTEGRATION=1 ./jmeter-manager-tests -test.v
```

Run the test binary in a container with the provisioned MySQL environment and secret mounts. Every fixture creates a random `jt_<uuid>_` prefix for its own business and runtime tables and drops only those prefixed tables on completion. Live platform tables are never read, changed, or scheduled. Tests use HTTP peers for agents, API debug and S3, not external load targets. CREATE/ALTER/DROP table privileges in the provisioned database are needed. On macOS, use a canonical temporary root such as `TMPDIR=/private/tmp` to avoid the `/var` symlink intentionally rejected by file-serving checks.

## Projects and collection operations

`GET/POST /api/projects`, `GET/PUT /api/projects/{id}` and `POST /api/projects/{id}/archive` manage logical projects. PUT and archive require `revision`; stale updates return 409. The default is `project-default`. Startup adds indexed `project_id` columns with that default to existing plans, runs, reports, workflows and collections, without rewriting their IDs, JSON, ciphertext or immutable JMX bytes. Project resources require explicit `X-Project-ID`, including `project-default`; missing scope is rejected. Platform inventory/session endpoints are unscoped. Each HTTP request has a separate scoped platform value, while the scheduler continues to see every project. Native GET/HEAD file links may use `?project_id=...` for raw artifact downloads, plan content, workflow/collection exports and legacy report files/ZIPs. Metadata and mutation APIs reject that query parameter; duplicate, empty or conflicting header/query contexts return 400. Authentication and ownership checks still apply, including on archived projects. Historical HTML entry links redirect to `/api/projects/{project_id}/reports/{id}/html/{path}` so relative CSS/JS/images/navigation preserve project scope. These read-only paths authenticate every request, reject conflicting header/query scope, and serve original bytes without rewriting saved files or ZIPs. No fallback infers a project from an object ID.

All business reads, writes, versions, relationship lookups, stop requests and downloads check ownership on the server; objects outside the selected project return 404. Lists filter before their existing limit/pagination. Every business object includes `project_id` (downloads also return `X-Project-ID`). Plan/run and workflow/collection relations must stay within one project. Run idempotency keys are scoped by project while existing default-project keys stay byte-for-byte compatible. Archiving retains historical data and permits stopping existing runs; it blocks new runs, debug, publication and draft mutations. The MCP authoring boundary rechecks manager project existence on every call; archived project drafts remain listable/readable/exportable, while create/import/edit operations are rejected before invoking the official authoring process.

Collection endpoints retain ordinary manager Bearer authentication or session + `X-CSRF-Token`:

| Endpoint | Request / result |
| --- | --- |
| `POST /api/api-collections/import` | `{format:'auto'\|'curl'\|'postman'\|'apifox'\|'openapi',content,name?}` → new encrypted collection; content is text or an object |
| `GET /api/api-collections/{id}/export?format=openapi\|postman` | JSON download; exact document semantics, Scalar credentials and environments are preserved |
| `PUT /api/api-collections/{id}/requests` | `{revision,path,method,operation,previous_path?,previous_method?}` → revised collection; rename collisions return 409 |
| `PUT /api/api-collections/{id}/environments` | `{revision,environment_id,variables:[{name,value}]}` → revised collection; replaces the selected same-project environment override and inherits project values when the array is empty |
| `PUT /api/api-collections/{id}/settings` | `{revision,name?,description?,servers?,client_state?}` → revised collection |
| `POST /api/api-collections/{id}/debug` | `{path,method,target_source:"project_environment"\|"document_server",environment_id?,variables?}` → `{status,headers,body,duration_ms,size,project_id,collection_id,revision,target:{source,collection_revision,environment_id?,environment_revision?}}` |

cURL is lexed as text and never executed. Quoted strings, repeated query/header values, raw/JSON/form bodies, Basic/Bearer auth and variables are preserved. Unsupported flags, file reads, multiple commands and shell operators are rejected. Postman v2/v2.1 nested folders, variables, HTTP requests, raw/urlencoded bodies and Basic/Bearer/API Key auth are supported; scripts, external files, proxy/certificate settings, duplicate method/path requests and unconvertible content are rejected explicitly. Apifox exports in Postman or OpenAPI format use the same conversion; native Apifox project exports return a clear unsupported-format error. The new import API takes OpenAPI 3.0/3.1 JSON; YAML should be parsed by the client, and Swagger 2 must be converted first. Existing full collection create/update contracts still preserve their input document.

Postman exports carry inactive environments in `x-jmeter-scalar-environments` and Scalar client state in `x-jmeter-client-state`, in addition to normal active collection variables and request auth. OpenAPI exports carry `x-jmeter-client-state`. These extensions are restored on import. Debug resolves the explicitly selected target and stored auth without forwarding manager authentication, cookies or project headers. Scalar environment declarations remain authoring data and do not choose execution targets. It shares the existing 30-second, 6 MiB request / 4 MiB response, DNS-pinned, redirect-forbidden transport. Schema-only bodies, unresolved variables and unsupported serialization fail explicitly instead of sending guessed values.

HTTP workflow nodes may carry `properties.api_reference={collection_id,revision,path,method}`. The server validates ownership and the exact source revision on save, preview and publication. Execution uses the node's saved configuration; later collection changes cannot rewrite that snapshot or any published JMX.

Portable backups include project rows and ownership columns, without decrypting old payloads. Restore accepts old snapshots without project metadata as default-project data, and validates all restored project/plan/run/report/workflow relations transactionally. A newly initialized destination's sole seeded default-project row does not block restoration. Keep the original session key separately, as before.


## Workflow and API workspaces

`GET/POST /api/workflows`, `GET/PUT/DELETE /api/workflows/{id}`, import, raw JMX export, and revision-checked publication manage LogicFlow drafts. Publication creates an immutable manager plan and never queues or executes a run. Graph validation enforces the single TestPlan root, parent scopes, one incoming edge, acyclicity, sibling order, typed component configuration, and JMX component/hashTree pairing. Imported known components retain their XML and patch only edited fields; unknown plugins retain their whole opaque subtree as a leaf. Imported variable/header rows carry hidden `_source_id` values tied to their original XML entries; preserve those fields when deleting, reordering or renaming rows, and omit the field for new rows.

`GET/POST /api/api-collections`, `GET/PUT/DELETE /api/api-collections/{id}`, and read-only `/versions` and `/versions/{revision}` keep complete OpenAPI objects and Scalar `client_state` (auth, active environment, direct/master mode). PUT requires the current revision; conflicts return 409. Deletion is soft. OpenAPI extensions, schemas, examples, authentication data, and client fields are kept without flattening.

Both domains and version snapshots are AES-GCM encrypted with a domain-separated key derived from the existing private session key. **Preserve the original session-key secret separately when restoring a backup onto another host.** The backup includes workflow, collection and environment rows, revision snapshots and script/workflow relationship metadata; encrypted payloads remain opaque and the key is excluded. Old state/backup files without the new arrays import them as empty tables. Startup applies the versioned, recoverable schema catalog and rebuilds derived workflow projections; it preserves source payloads, immutable JMX and run snapshots.

Scalar may explicitly send through `METHOD /api/api-proxy?scalar_url=<absolute-http-url>`. This proxy requires the platform HttpOnly session cookie and **`X-JMeter-CSRF-Token` on every method**, including GET. Business `Authorization` and `X-CSRF-Token` are forwarded independently; the platform cookie and namespaced CSRF header are stripped. The namespaced `X-JMeter-Project-ID` page context is required exactly once and must identify an existing, active project (including explicit `project-default`). Missing, duplicate or invalid values are rejected before contacting the target; nonexistent and archived projects are rejected as well. The header is stripped; an authored target `X-Project-ID` stays a business header. A platform admin bearer token is never forwarded to the target, even with an otherwise valid cookie session. Native `x-scalar-cookie`, user-agent, date, dnt, and referer aliases map to target headers. Upstream Set-Cookie is exposed only as `x-scalar-set-cookie`, never as a platform-origin Set-Cookie.

Proxy requests have a 30 second deadline, 6 MiB request body and 4 MiB response body caps. Only HTTP(S) is allowed. RFC1918 and Docker DNS targets work; every resolved address is validated, actual connections dial a pinned approved IP, and loopback/link-local/unspecified/multicast/known metadata targets are rejected. Redirects are rejected without exposing Location. No proxy or target request occurs merely by saving a document or workflow.


## Unified script workspace

- `GET /api/plans/{id}/content` remains the exact immutable JMX source. It never substitutes a workflow's current export.
- `POST /api/plans/{id}/archive` accepts exactly a boolean `archived` value (`true` to archive, `false` to restore). It uses the active `X-Project-ID` scope, rejects archived projects and missing/cross-project plans, and requires the existing CSRF token for cookie sessions. Repeating the current state succeeds without another state change or audit entry. The response is `{id,project_id,archived,updated_at}`; an already visible plan without a state record has an empty `updated_at`.
- `GET /api/plans` and `GET /api/plans?view=summary` exclude archived entries by default; `include_archived=true` includes them with an `archived` boolean on each item. The summary view retains `limit/offset`; the full view also accepts optional `limit/offset` (otherwise it retains the full-list response). `total` applies the project/archive filter before pagination. The flag accepts one literal `true` or `false`; invalid or repeated flags return 400.
- Archive state lives in the separate `plan_library_state` table. A state change and its `plan.archive` / `plan.restore` audit entry commit together. It does not update plan JSON, encrypted summaries, JMX files, SHA-256, source links, tasks, dispatch payloads, reports or original artifacts. Exact plan metadata/content/graph/source reads, historical task details and task copies keep resolving their immutable plan IDs. Same-name canonical versions remain independent entries unless their exact IDs are explicitly archived. Logical backups include this state; pre-archive backups import with all plans visible.
- `GET /api/plans/{id}/graph` returns `{plan_id,sha256,graph,read_only:true}` by reading that snapshot and importing it only in memory. Node and edge IDs are deterministic for the same plan/snapshot. No draft, version, plan, audit record, or file is created. Unsupported conversion returns `422 graph_preview_unavailable`, while the source remains accessible.
- `POST /api/workflows/preview {graph}` validates and compiles the current unsaved canvas into an `application/xml` response. It performs no database/file writes and uses ordinary manager authentication/CSRF rules. The platform proxy's separate CSRF header does not apply here.
- `GET /api/plans/{id}/workflows` returns all persisted exact-plan relationships. Each item includes `plan_id`, `workflow_id`, `workflow_revision`, `relation`, `created_at`, `workflow_current_revision`, `workflow_deleted`, and optional decrypted `workflow_name`.
- `GET /api/workflows/{id}/plans` returns the same relationships with a complete nested `plan` metadata object. Soft-deleted workflows' read-only relationships remain available; their ordinary editable workflow endpoint still returns 404.
- Importing an exact `plan_id` creates a new draft and atomically records an `imported` relationship. The server-controlled creation origin is also returned as `source_plan_id`; create/update request fields cannot forge it. Publishing records a `published` relationship in the same transaction as the new plan row and keeps the existing revision conflict guard.

`plan_workflow_links` is indexed in both directions and uniquely identifies `(plan_id,workflow_id,workflow_revision,relation)`. Relationships are never deduplicated by content hash. Existing behavior remains: each upload/publication creates a fresh plan ID, while identical JMX bytes share a physical SHA-256 snapshot. Multiple workflows, revisions, and imported drafts keep every relationship.

Historical scripts without provenance remain previewable and can seed a new draft. Names, timestamps, and incomplete old audit events are not used to guess an origin. A historical plan's preview always uses its actual immutable content, even when a linked workflow has since advanced or been deleted.

## Project environments

`GET/POST /api/environments` and `GET/PUT/DELETE /api/environments/{id}` use the current `X-Project-ID`, authentication and CSRF rules. A resource contains `{id,project_id,name,base_host,variables:[{name,value}],revision,created_at,updated_at}`. PUT requires the current revision (409 when stale); DELETE is soft; environment names are trimmed before validation and are unique within each project, including concurrent creates and renames; legacy names are compared after trimming. Archived projects permit reads only. Environment payloads use the existing AES-GCM workspace key, and the prefixed table participates in portable backup/restore. Preserve the session key separately.

`base_host` is a complete HTTP(S) URL, including an optional port, bracketed IPv6 host and base path; credentials, query strings and fragments are rejected. Custom variables default to an empty array and allow up to 100 unique names (1–200 characters, excluding `base_host`, control characters and JMeter expression delimiters) with string values of at most 4096 characters.

Variable values may be fixed strings or complete generation declarations: `{{$random.uuid}}`, `{{$random.hex(N)}}`, `{{$random.alnum(N)}}`, `{{$random.digits(N)}}` (N=1–256), `{{$auto.client_ip}}`, and `{{$auto.timestamp_ms}}`. Embedded or malformed declarations are rejected. Names carry no automatic meaning: a fixed `client_ip` or `profile_update_time` stays fixed. Saving project or collection definitions preserves the declaration; effective values resolve only after the operation's merge and explicit overrides.

Authenticated, project-scoped `GET /api/environment-runtime` returns `{client_ip:string,timestamp_ms:string}` for one operation. IP is the actual TCP request peer, normalized for IPv4/IPv6; untrusted `Forwarded` and `X-Forwarded-For` headers are ignored. The MCP adapter attributes its caller through signed internal metadata after normal platform-admin Bearer authentication. Invalid or partial metadata is rejected, and signed unknown IP remains unknown. An unavailable source returns `client_ip:""`; using its IP declaration fails with `environment_client_ip_unavailable` unless a fixed override removes the declaration. The timestamp is a positive decimal millisecond string shared by project, task and collection resolution in that operation. Internal attribution headers are removed from outgoing HTTP, Socket.IO handshakes and WebSocket connections.

Task submission freezes generated values and their `generation_rule` provenance in the existing encrypted snapshot and signed dispatch. Same-key retries reuse the original values. A normal copy resolves declarations again; an explicit historical environment copy preserves the original project rows, while newly supplied task declarations still resolve for the new operation, matching random-value copy behavior. Millisecond timestamps are not unique identifiers.

Scalar execution templates without a project binding may retain complete declarations only in their managed HTTP `Arguments` variable list. Compilation replaces these values and scoped sampler references with `jmeter.env.scalar.<node hash>.<name hash>` property lookups, retaining validated source metadata for import and publication. Submission resolves and freezes those opaque properties; explicit task overrides win. Identical names on different HTTP nodes remain scoped. Unmanaged or mismatched declaration metadata is rejected instead of passing rule literals to JMeter. The opaque values use the existing snapshot encryption, sensitive-name masking and signed task payload budget.

A graph opts in with `environment_binding:"base_host"` and `environment_variables:["token",...]` (names only). Compilation binds every HTTP sampler to `jmeter.env.protocol`, `host`, `port` and `base_path`; root user variables bind `base_host` to `${__P(jmeter.env.base_host,)}` and each custom name to `${__P(jmeter.env.var.<first-16-hex-of-SHA256(name)>,)}`. Requests keep normal `${token}` references so extractors can update JMeter variables. JMX metadata retains the binding, variable names and request-only paths across import/edit/publication. Old managed Scalar environment variables are converted to property references. Existing fixed scripts remain unchanged. Disabling a binding on imported runtime-property JMX is rejected with `invalid_environment_binding` when a fixed address cannot be recovered.

Published/uploaded plan metadata includes `requires_environment`. `POST /api/runs` must carry an `environment_id` for every new task, and the selected environment must contain every declared custom variable. The transaction freezes `environment_snapshot` (identity, revision, values and parsed address) and runtime properties. User properties cannot override `base_host` or platform runtime keys (including `jmeter.env.*`). Same-key retries use the original run even after environment updates/deletion; changing the selected environment with that key returns 409. The signed task snapshot is bounded to 4 MiB before queueing. Snapshots and all effective task properties are stored in an AES-GCM `environment_envelope` bound to the run/project identity; portable state preserves its ciphertext and internal reads fail closed on an incorrect key. Older plaintext/fixed runs remain compatible. Lists and run details expose that original snapshot, with sensitive variable values masked according to their original names; bounded log reads apply the same mask. CLI `api start PLAN --name NAME --key KEY --cluster CLUSTER_ID --environment ENV_ID` maps to the same submission contract.

Collection debug requires `target_source`. `project_environment` requires `environment_id`; `document_server` rejects that field and uses the authored OpenAPI server. Legacy `environment` execution input is rejected. Project defaults, collection overrides and one-operation variables resolve in that order, while preserving operation parameters, body and authentication. Responses include target provenance and revisions; Scalar active environments do not select a target.

### Collection overrides inherited from project environments

Collections store only `client_state.project_environment_overrides = { [environmentId]: { variables: [{name,value}] } }`. IDs must identify active environments in the same project. Rows are unique custom names, at most 100 per environment, string values including `""`, at most 4096 characters. `base_host` is reserved. Debug merges project variables, collection rows, and explicit request variables in that order, then locks `base_host` to the project address. The browser uses the same merge for both direct and Master transport. Project names, targets, and inherited values are read dynamically and are never copied into the override state.

`PUT /api/api-collections/{id}/environments` now accepts `{revision,environment_id,variables}` and replaces that ID's rows. It no longer accepts private named environment lists. Whole-collection writes validate every referenced ID. Deleting a project environment, cleaning all current collections in that project, and appending their incremented encrypted revisions happen in one transaction. Stale revisions fail OCC; even a historical body with a current revision cannot restore a deleted ID. Old history and submitted runs stay immutable. Legacy named Scalar environments are preserved as archived authoring data, with no name-based automatic mapping and no old server/list in the new collection environment UI. Custom legacy rows can be manually entered against the intended project ID.

Bound HTTP references retain `api_reference={collection_id,revision,path,method}` plus compiler-owned `api_environment_variables` names derived from that immutable collection revision. Only used override names are scoped. JMX carries `jmeter.api_environment` metadata with node identity, exact source and names, and direct `${__P(jmeter.env.api.<node hash>.<name hash>,)}` lookups in that node's subtree. Import/export restores the metadata and editable templates. The project environment selected at submission decides the collection override values; current collection edits/deletion cannot alter the referenced version. Missing/cross-project history fails closed. Scoped values and source details are frozen in `environment_snapshot.api_overrides`, encrypted with the existing run envelope; public responses and logs mask sensitive values using each original name. The signed Redis task limit applies to these effective properties.


### Redis tasks, datasets and the single result Webhook

`GET /api/cluster` returns global cluster inventory and SQL status counts across all projects. Task histories remain project scoped. A cluster binds one controller to selected workers; shared membership is supported. `PUT /api/clusters/{id}` accepts `{revision,name,worker_ids?,callback_url?}` with OCC. Membership changes are blocked while associated tasks own or await execution.

`callback_url` is the runner's console result endpoint. A bare console origin is normalized to `/api/runner/callback`; an empty setting uses `JMETER_RUNNER_CALLBACK_URL` (default `http://manager:8080/api/runner/callback`). This is the only Webhook path. The manager does not send a second business notification. Old webhook rows remain readable for historical compatibility; the retired notification sender and delivery loop have been removed. Historical rows are never replayed.

Every new task requires `cluster_id` and `environment_id`. Omitted workers are resolved after the idempotency lookup. The task freezes environment revision/values, cluster/controller/membership, actual global properties, dataset refs, plan ref, timeout and attempt ID. Environment custom values are native JMeter properties as well as compiler compatibility properties. User custom properties override environment defaults; `base_host` and runtime control keys are locked. JSON extractors affect subsequent samplers in their thread group. CSV variables apply within their parent scope. Automatic collection/environment binding preserves visible producer variables; explicit `${__P(name,)}` accesses a global property.

Task creation commits an encrypted `task_dispatches` outbox row with the run. A separate publisher sends signed version-1 Redis Stream messages after commit and retries safe errors. New tasks have `execution_mode=redis`; the legacy HTTP dispatcher and unowned recovery/cleanup paths have been removed. Historical non-Redis records are read-only; explicit retirement requires a fingerprint, global lease and fresh idle worker evidence. Redis failure leaves the run queued. Claims require HMAC auth, the frozen attempt/controller and the current Redis lease covering every selected worker. A duplicate claim returns `execute:false` and never authorizes a blind rerun. Heartbeats advance `PREPARING`, `RUNNING`, `STOPPING`, `UPLOADING`, `CALLBACK_PENDING`; the latter two do not count as physical execution. The runner reports one immutable completion event; matching duplicates return 200, changed content returns 409. Receipt objects are checked against the configured bucket, task key, SHA and byte count before committing terminal state. `callback_delivery` exposes safe delivery progress. Queued and physical execution states reserve the selected workers globally across projects and clusters. Overlapping submissions fail with `409/worker_busy`; disjoint sets can execute concurrently, including through the same Core. Uploading and pending callback delivery do not reserve workers.

CSV files belong to one stable workflow CSV node, with multiple immutable versions per node. APIs:

- `GET /api/workflows/{id}/datasets`: current nodes and all active file records, including historical nodes in `items`.
- `POST /api/workflows/{id}/datasets`: streamed multipart `node_id`, optional `name`, and `file`; temporary bytes stay under the private state volume.
- `POST /api/workflows/{id}/datasets/generate`: `{node_id,name?,rows?,columns:[{name,generator,start?,prefix?,value?,min?,max?}],seed?}`; rules are sequence, uuid, constant, random_int; default 100, maximum 100000 generated rows.
- `GET /api/datasets/{id}`, `/preview?limit=10`, `/content`; `DELETE /api/datasets/{id}` soft deletes only the record.
- `GET /api/plans/{id}/datasets`: frozen node/schema requirements and compatible active files from the published workflow origin.

CSV accepts UTF-8 with optional BOM, CRLF, quoted delimiters and multiline cells. Invalid UTF-8, repeated/invalid headers, mismatched columns and empty data are rejected. The original SHA covers original bytes. Actual headers (`properties.dataset_columns`) are separate from optional `config.variableNames` aliases. Publication records stable node markers in JMX, UTF-8 encoding, quoted data and first-header skipping. Conflicting CSV variable names in overlapping scopes are rejected; separate thread groups may reuse names.

`POST /api/runs` adds `dataset_selections:[{node_id,dataset_ids:[],distribution:"copy"|"shard"}]` and `timeout_seconds` (default 3600, range 10–86400). Missing/extra/foreign/deleted/incompatible files fail before queueing. Multiple selected files merge in request order. Defaults: one file 256 MiB (`JMETER_DATASET_MAX_BYTES`), all selected task inputs 1 GiB (`JMETER_TASK_INPUT_MAX_BYTES`). New plan JMX and dataset bytes are immutable MinIO objects; task datasets remain usable after later soft deletion.

Artifact APIs stream the main `results.jtl`, `jmeter.log`, `console.log`, periodic thread-observation originals and existing historical original-file receipts from verified MinIO references. Archive status is `ARCHIVED`, `PARTIAL` or `EMPTY`; missing files are explicit. The authenticated download proxy never takes an arbitrary endpoint. Running log tails come from the assigned runner and use existing redaction; finished tails stream into a bounded buffer. Legacy local downloads and read-only reports remain available.

CLI examples (project resources require `--project PROJECT_ID`):

```sh
jmeter-manager api start PLAN --project PROJECT_ID --name NAME --key STABLE_KEY --cluster CLUSTER --environment ENV --datasets-file selections.json --timeout 3600
jmeter-manager api dataset-list WORKFLOW --project PROJECT_ID
jmeter-manager api dataset-upload WORKFLOW --project PROJECT_ID --node CSV_NODE --file data.csv
jmeter-manager api dataset-generate WORKFLOW --project PROJECT_ID --node CSV_NODE --json-file generation.json
jmeter-manager api dataset-preview DATASET --project PROJECT_ID --limit 10
jmeter-manager api dataset-download DATASET --project PROJECT_ID --output data.csv
jmeter-manager api dataset-delete DATASET --project PROJECT_ID
```

Redis uses `JMETER_REDIS_ADDR`, `JMETER_REDIS_DB`, `JMETER_REDIS_PREFIX` and private `JMETER_REDIS_PASSWORD_FILE`. Existing archive secret files configure the shared object store. All three new tables participate in validated table prefixes and portable state; old backups may omit them. Preserve the original session key to decrypt run/dispatch snapshots. Restores interrupt old in-flight tasks and never republish terminal work.

### 集成审查补充

物理执行态丢失控制器后，manager在 `JMETER_RUNNER_STALE_SECONDS`（默认90秒）无心跳时尝试独立Redis recovery lease，每tick最多处理一个旧owner，并行检查其冻结worker地址。只有全部selected worker已完成自身租约失效清理、健康idle且无job时，才在原task/attempt/lease/heartbeat仍匹配的事务内转INTERRUPTED/STOPPED；未知、忙或Redis故障均不猜测完成。manager不向worker发reset，不能误重置新任务。迟到原callback仍须提交真实MinIO收据；持久化后200结束重试，并保持已确认的中断终态。

上传采用 `JMETER_UPLOAD_IDLE_TIMEOUT`（默认30秒）的无进度超时，持续有进度的multipart不受固定30秒总时长限制。生成数据先校验CSV节点，用0600私有seekable文件流式写入，每次写入检查配置字节上限，不在内存构建完整CSV。集合引用属性按项目环境 → task自定义defaults → 显式集合覆盖计算；动态producer仍优先于自动静态绑定。CSV变量冲突按相同父级/祖先父级的实际作用域重叠判定，互不相交的兄弟控制器允许同名。

便携备份仅包含MySQL状态、plans本地快照和legacy本地产物，不打包外部MinIO对象。异机恢复必须独立恢复或attach原MinIO卷/桶，保留原bucket/key；启用前按保存的size/SHA256定向读取plan、dataset和raw receipt对象核验。单独恢复本地备份不能据此宣称外部归档文件已恢复。


Project interface display plugins are project-owned encrypted `config` or JavaScript `code` definitions, with an independent CAS revision and install state. A new project starts with an empty catalog. Unknown historical installation references remain inert data; listing never migrates or rewrites project JSON. The manager stores code without evaluating it, and the browser sandbox may only parse and render existing messages.

Native WebSocket workflows preserve raw frames and generic JSON `frame` values. Connect/send/read/request/close, text/regex/JSONPath matching, bounded asynchronous queues and JMeter JSON extractors remain available. Legacy Socket.IO mode adds only a dynamic handshake and transport `2::` heartbeat. It does not interpret application command IDs, derive login/session fields, acknowledge application messages or determine heartbeat success from payload fields. `ser`, `now`, `message_id`, `last_ser` and the current legacy Socket.IO handshake `socket_id` are reserved runtime variables; the handshake ID is generated on connect and cleared on close/reconnect. All other variable names follow ordinary project and JMeter producer scope. A configured periodic heartbeat sends the authored message; application confirmation belongs in explicit receive nodes/assertions. `websocket_trace_events=true` enables bounded phase/counter/generated-ID diagnostics without message bodies or authored match values.

Task custom property names do not imply application-specific artifact requirements or stop behavior. Graceful stop uses JMeter's shutdown command with a bounded timeout; immediate stop and task cancellation terminate the owned process group. New node evidence includes generic JMeter/console previews, request samples and periodic thread observations. Retired application-specific evidence fields in stored task JSON are not interpreted or rewritten. Existing original-file receipts remain downloadable through project ownership, configured bucket, task raw-prefix and immutable hash/size checks.

已发布脚本的网页编辑复用当前精确预览，打开及取消不创建草稿。首次修改后保存通过 `POST /api/workflows/import` 提交 `{name,description,plan_id,graph}`；服务端校验当前项目的不可变来源和完整图，在同一事务创建首个修订及来源关联。不带 `graph` 的既有 JMX/计划导入保持原行为。
