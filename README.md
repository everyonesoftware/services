# Services

A home-server monitoring console for one Docker Engine. Problems are shown first; applications are grouped by Compose project; image updates are always opt-in.

## Monitoring

Monitoring runs on the server, not in a browser tab. The dashboard shows active incidents, container diagnostics, and recent changes.

- Docker health checks are used when configured. A failed check is a problem even while the process is running.
- Without a health check, Services monitors container state and restart activity. **Running** means the process is up; it does not claim the application is responding correctly.
- Repeated restarts and observed container replacements are tracked over a five-minute window for applications without health checks, including containers that happen to be running when sampled. A configured Docker health check remains authoritative while running; restart counts remain visible as diagnostics.
- Stopped, restarting, dead, paused, and out-of-memory-killed applications need attention. Successful Compose one-off jobs are distinguished from failed applications.
- Non-Compose containers appear under **Standalone** and cannot be updated through Compose actions.
- Docker outages are shown separately from application failures. The last successful inventory stays visible and is marked stale; it is not presented as a live healthy result.
- Active incidents and recent history are in memory. They reset when Services restarts. Polling is not an exhaustive Docker event log: containers created and removed entirely between polls may not be observed.

### Service icons

Project and container names have small, locally bundled official GitHub organization avatars or application artwork, with light gray circular backgrounds. Asset provenance is recorded in `web/public/icons/sources.json`. No external image requests or runtime logo-search service are used while displaying icons.

The curated mapping in `web/src/brandIcons.js` matches image repository basenames and explicit aliases, ignoring registry hosts, namespaces, tags, and digests. It does not guess from container names or treat a brand string anywhere in an image reference as a match. Custom images and applications outside the mapping receive a neutral container icon rather than an invented logo.

A Compose project uses its single recognized application brand, ignoring Tailscale sidecars. Tailscale-only projects use Tailscale; mixed application brands, unknown applications with only a recognized VPN sidecar, and the Standalone group use a neutral stack icon. Container rows always keep their individual image's icon, and health indicators remain separate.

#### Custom project and container icons

Hover over a project or container icon to reveal its pencil, or focus it with the keyboard, then click to open **Edit icon**. Choose **Image URL** to paste a direct image link and **Download & save**, or **Upload image** to select a file from your device and **Upload & save**. Selecting a file shows its name without uploading it; nothing is sent until you save. Services validates the image, normalizes it to PNG, and stores a local copy under `SERVICES_DATA_DIR/icons` (`/data/icons` in the supplied Compose deployment). Reloading the dashboard serves that stored copy; it does not contact the original image host. **Use default icon** removes the override and restores automatic artwork. Cancel and rejected images leave the current icon unchanged.

Project overrides and individual container overrides are independent. Projects are identified by project name; container overrides use the full container name, not the replaceable Docker ID, so ordinary recreation with the same name preserves the override. Renaming a project or container gives it a different identity. No Compose or container changes are made by editing an icon.

Both uploaded files and URL imports support PNG, JPEG, or GIF (the first frame is used), with a 2 MiB file limit and 4 million pixels, with neither side exceeding 4096 pixels. The server validates the actual image bytes rather than trusting the filename or reported MIME type. Uploaded filenames are not used as storage paths, and uploaded icons do not retain a remote source URL. SVG and HTML are rejected. URL imports additionally require public HTTP(S) destinations: credentials in URLs and private, loopback, link-local, or other non-public network destinations are rejected, including redirect destinations. To use an image from your LAN or Tailscale network, save it to your device first and use **Upload image**. Uploading an existing local file does not require internet access.

Custom icon editing requires an explicitly configured absolute, writable persistent `SERVICES_DATA_DIR`. If storage cannot be initialized, editing is disabled without disabling monitoring. If a metadata write cannot be confirmed durable, editing is disabled until Services restarts and reloads the visible metadata; a failed request is not proof that no disk change occurred. Resolve storage errors before restarting or retrying. Keep the entire icons subfolder with your data backup. Like existing dashboard actions, icon mutations use the configured Origin/Host checks and rely on your trusted network or authenticated proxy for access control; they do not add a login system.

### Notifications

Set `SERVICES_WEBHOOK_URL` to an HTTP(S) endpoint that accepts a JSON POST to receive problem and recovery events even when the dashboard is closed. An event has `id`, `project`, `name`, `kind`, `message`, and `time` fields. This is a generic webhook, not a Discord/Slack-specific payload; use a compatible receiver or an automation bridge.

Unchanged incidents are deduplicated. Failed deliveries are retried within the running process. No webhook is configured by default; without one, alerts are available in the dashboard only. Delivery history is not durable across restarts. Keep token-bearing webhook URLs secret and prefer HTTPS outside a trusted network.

## Image updates

Services checks public Docker Hub and GHCR image tags at startup and periodically (six hours; failed checks retry after fifteen minutes). **Check for updates** requests an immediate check. It compares the configured tag's remote manifest digest with the running image's repository digest—not version numbers.

Unsupported registries, digest-pinned images, missing repository digests, and registry failures are explicitly distinguished from an up-to-date image. Private-registry authentication is not supported. Updates to a different tag, such as moving from `1` to `2`, require editing your Compose configuration yourself.

Choose **Review update** on a Compose group to see the changed services and digests, then explicitly confirm the update. The server plans one durable operation for the changed running Compose services and any network-sharing dependents:

1. Validate the project's paths, privately resolve Compose configuration, inspect current Engine identities and namespace relationships, and reject ambiguous or unsafe plans.
2. Pull only image-update targets. Unchanged network-sharing dependents must still resolve to their installed immutable image IDs, both before and after pulling; tag drift fails before any container is stopped.
3. Persist an execution plan with old container/image IDs, expected image IDs, direct namespace dependencies, running state, and reasons. Stop affected running consumers in reverse dependency order.
4. Recreate affected running services in provider-first order using `docker compose up -d --no-deps --force-recreate --pull never --no-build --wait --wait-timeout 300`, verifying each replacement's identity, image, readiness, and direct namespace attachment before advancing. Unrelated and intentionally stopped containers are not recreated or started.
5. Report success only after all replacements pass verification; retain the plan, failures, and command progress in the shared operation activity. A failure after container mutation begins pauses the queue for operator attention rather than replaying the plan.

This recreates containers and can cause downtime. It uses the Compose files and `.env` values on disk at execution time. Mutable tags may change between review and pull. No builds, dependency startup, volume removal, automatic updates, or automatic rollback are performed. Back up application data before updating. Compose operations are serialized to prevent overlapping updates. Closing a browser does not roll an update back.

### Image publication notifications (optional)

GitHub Actions can announce an image immediately after a successful GHCR push using `POST /api/images/published`. This is **metadata ingestion only**: accepting a publication does not pull images, enqueue updates, run Compose, or restart containers. Continue to use **Review update** and explicit confirmation to install updates.

The notification supplies the published repository, actual tags, root manifest/index digest, version, source repository, commit, Git ref, workflow run and attempt, workflow URL, and UTC publication time. Services compares that digest with the running image's local repository digest for the exact repository **and tag**. A notification for `:main` does not change an application tracking `:latest`; pinned images remain pinned. Missing local repository digests remain unknown rather than implying an update. Notification ingestion and immediate dashboard display do not query GHCR. The existing background checks and **Check for updates** remain reconciliation paths, and a manually confirmed installation still checks/pulls from the registry.

Configure `SERVICES_PUBLISH_TOKEN` with a high-entropy secret of at least 32 printable non-space ASCII characters (maximum 512), and `SERVICES_PUBLISH_REPOSITORIES` with a comma-separated list of exact untagged lowercase repositories, for example `ghcr.io/everyonesoftware/services`. Wildcards and tag suffixes are rejected. Both settings are required to enable the endpoint; neither is enabled by default. Store the same token in the publishing repository's GitHub Actions secret, never in committed files or workflow output. The credential authorizes notifications for its allowlist only; it does not grant dashboard or container-action permissions. This first implementation uses one notification credential per Services installation.

Keep the endpoint behind Tailscale ACLs or an authenticated trusted private proxy. It requires an allowed request hostname (the configured public origin is automatically allowed), a bearer token, unencoded `application/json`, and at most 64 KiB. Browser `Origin` and `Sec-Fetch-Site` headers are rejected; existing browser-action origin protections remain unchanged. The Services environment requires `/data` on a verified writable persistent named volume or local bind. Configuration/storage failure disables notifications without disabling monitoring. Worker containers do not inherit the notification credential.

Accepted publication metadata is committed to a separate `/data/publications.db` before acknowledgement, survives Services replacement, and is independent of the operation journal. Treat its SQLite database and any WAL/SHM files with the same stopped-copy or SQLite-aware backup precautions as the operation journal. HTTP 200 returns `{ "result": "accepted" }`, `duplicate`, or `stale`; only accepted newer metadata replaces the appropriate tag's known publication. Conflicting retained event identities return 409. Malformed metadata returns 400, invalid credentials 401, out-of-scope repositories or browser/untrusted-host requests 403, oversized bodies 413, unsupported media 415, and disabled/unavailable persistence 503. No acknowledgement claims an image has been installed.

#### Enable the GitHub Actions sender

The included workflow captures metadata immediately after `docker/build-push-action` succeeds, using its digest output and the actual Docker metadata tag list. It saves the original payload as an artifact for 30 days. A separate, opt-in job downloads that payload, joins the private tailnet, and sends it. The publish job remains successful if notification delivery fails; the notification job and its failure-report job make the delivery failure visible. **Re-run failed jobs**, not all jobs, to resend without rebuilding or republishing. Missing/expired artifacts fail closed rather than manufacturing a new publication timestamp.

1. Deploy this version of Services with the persistent data mount and both `SERVICES_PUBLISH_*` environment settings above. Existing deployments must add those environment entries to their own Compose file; the supplied `compose.yaml` includes them. Keep the workflow notification variable disabled until the receiver is deployed.
2. In Tailscale, create a dedicated CI tag and federated OIDC identity with **Auth Keys > Write** (`auth_keys`) and permission to use that tag. Restrict the identity's GitHub trust to the intended repository and publishing refs. For notification-only network access, expose a private proxy listener that forwards **only** `POST /api/images/published`, rejects every other method/path, and allow the CI tag only to that listener's port. Do not grant CI general access to the home server or dashboard port. Tailscale ACLs restrict network destinations/ports, not HTTP paths. See the official [Tailscale GitHub Action setup](https://tailscale.com/docs/integrations/github/github-action).
3. Set these **GitHub repository variables**:

   | Variable | Value |
   | --- | --- |
   | `SERVICES_PUBLISH_URL` | Full private HTTPS endpoint, e.g. `https://services.tail1a7c8f.ts.net/api/images/published`. No query, fragment, credentials, or alternate path. |
   | `TAILSCALE_CLIENT_ID` | Federated OIDC identity client ID. |
   | `TAILSCALE_AUDIENCE` | Federated identity audience. |
   | `TAILSCALE_TAGS` | Authorized comma-separated CI tag(s), e.g. `tag:services-publisher`. |
   | `SERVICES_PUBLISH_NOTIFICATIONS` | Set to the literal `true` only after the receiver and tailnet permissions are ready. |

4. Set the **GitHub Actions secret** `SERVICES_PUBLISH_TOKEN` to the same notification-only secret configured on Services. Do not put the secret in repository variables.
5. After the next successful publish, confirm the notification job reports an acknowledged result. A matching running application's update status appears on the next dashboard services poll (normally within 30 seconds); open **Review update** to see publication version/time, commit, and workflow links. Installation still needs confirmation.

**Network trust matters:** Services' existing dashboard action routes do not have built-in login authentication. Their Origin/Host checks are browser-CSRF defenses, not authorization against a server-side client that can supply its own headers. The notification token does not grant those routes access, but direct CI access to the dashboard listener would still trust that runner as a dashboard network client. A notification-only proxy listener and destination-port ACL are required if the runner must not have that pre-existing dashboard capability. Add the proxy's forwarded hostname to `SERVICES_ALLOWED_HOSTS` as necessary, and use its HTTPS URL (including a dedicated port if used) for `SERVICES_PUBLISH_URL`.

The sender is dependency-free Node 24: `node scripts/publish-notification.mjs capture` creates the artifact once; `node scripts/publish-notification.mjs send` sends the file at `SERVICES_PUBLISH_PAYLOAD_PATH`. Capture uses `PUBLISHED_DIGEST`, newline-separated fully qualified `PUBLISHED_TAGS`, the positive integer `APP_VERSION`, and GitHub's repository/SHA/ref/run/attempt environment. The image name may differ from the source repository name. The send command requires `SERVICES_PUBLISH_URL` and `SERVICES_PUBLISH_TOKEN`. It serializes once and makes at most four attempts, with a ten-second timeout per request and exponential delays. It retries transient network failures and HTTP 408/429/5xx, does not follow redirects, and never logs credentials or untrusted response bodies.

To enable another application, add its exact image repository to Services' allowlist and add the same capture/artifact/notification lifecycle to that application's publish workflow. Supply that workflow's actual tags, root digest, and version; do not infer them from a current GHCR lookup or assume the image name equals the GitHub repository. Only the Services repository's workflow is wired here; other repositories are not modified.

The workflow serializes non-PR publish-and-notify runs in a shared concurrency group, with no cancellation of running work and up to 100 queued runs (`queue: max`). Overflow runs are canceled; ordering is by entry into the concurrency queue, not guaranteed commit or dispatch order. PRs use separate groups. Other publishers must use equivalent serialization for shared mutable tags. Background/manual checks remain necessary reconciliation for external tag changes, missed delivery, clock skew, or unavailable Services. See [GitHub's concurrency documentation](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency).

#### Notification contract and retention

Schema version 1 accepts these JSON fields (unknown fields are rejected):

| Field | Meaning |
| --- | --- |
| `schemaVersion` | Integer `1`. |
| `repository`, `tags`, `digest` | Untagged lowercase GHCR image repository, 1–64 distinct bare tags, and published root manifest/index `sha256:<64 lowercase hex>` digest. |
| `version` | Optional bounded human-readable version string; the included sender uses the commit-count build number. |
| `sourceRepository`, `commit`, `ref` | GitHub `owner/repository`, full lowercase commit SHA, and full Git ref. |
| `runId`, `runAttempt`, `runUrl` | Positive decimal run ID **as a string**, positive integer attempt, and exact `https://github.com/<sourceRepository>/actions/runs/<runId>`. |
| `publishedAt` | RFC3339 UTC publication timestamp ending in `Z`, captured after successful push, not when delivery is retried. |

Repositories bind to their first accepted source repository. Duplicate retained event identities (source, image repository, run ID, attempt) must have identical metadata; conflicting payloads are rejected. Publication time determines freshness; run/attempt is only an equal-time tie-breaker. Notifications more than five minutes in the future are rejected. Coordinators sharing the data directory observe committed metadata and fence registry requests that started before a notification.

The cache supports 128 source-bound repositories, retains at most 256 recently updated tag mappings per repository, and retains 1,024 recent event identities plus older events still referenced by retained tags. Older tag mappings are evicted so unique version/SHA tags do not exhaust a lifetime quota; an evicted tag falls back to ordinary registry checking. A compact replay floor prevents pruned identities from being reused to replace newer data. Rerunning a sufficiently old workflow below that floor is treated as stale; trigger a new workflow run instead. This is bounded update-discovery provenance, not a complete release audit log. A later registry reconciliation that finds a different digest clears mismatched publication details rather than attributing that image to the old notification.

### Restart-resilient execution and self-update

The supplied deployment stores operations in SQLite under `/data`, backed by the `services-data` named volume by default. A writable local host-folder bind at `/data` is also supported. Accepted work, submission order, recent output, and results survive replacement of the Services container. This does **not** persist monitoring incidents or webhook delivery history. General registry-check caches remain in memory; the separate publication database preserves registry reconciliations for retained announced tags so a restart cannot resurrect superseded publication metadata.

Coordinators sharing this storage serialize the entire helper reconciliation and launch interval using a separate SQLite reservation file, `operations.db.coordinator-lock`, alongside the journal. A process crash releases the reservation automatically. Keep all files in the data directory together; do not remove or replace the coordination file while any Services coordinator is running.

Compose updates execute in temporary worker containers, independently of the dashboard process. Each worker uses the exact installed Services image, the Docker socket, the same read-only project mount, and the same writable operation storage. For a named volume, the worker reuses its actual Docker name; for a host-folder bind, it reuses the exact source path reported by Docker inspection, not a relative `./data` or the container's `/data` path. Workers have no published ports and do not inherit the dashboard's webhook configuration. Their identity and progress are tied to one durable operation, not to a browser connection.

Set `SERVICES_PROJECT_NAME` if Services' own Compose project is not named `services`. Self-updates use the same worker handoff: validate and pull before replacing the dashboard, reject an image without the compatible `io.services.worker.protocol=1` label before applying, then verify the replacement image and application readiness before reporting completion. The dashboard reconnects automatically after the interruption; a lost connection is not evidence of success or failure. The `/api/ready` endpoint is used by the supplied image's Docker health check. Worker readiness probes currently require the internal HTTP port to remain **8080**; the externally published port may be changed.

On startup, Services reconciles saved work with its Docker workers before starting more updates. Waiting jobs retain their order. A surviving worker is not launched again. Interrupted work with an uncertain result must not be blindly replayed or reported as successful. Docker or storage failures must not silently create a new empty queue or authorize overlapping mutations.

If a helper disappears or exits without a durable result, or application/verification fails after applying begins, the operation is marked **Interrupted** and the queue pauses. Check the affected project in Docker, review the recorded output and expected image IDs, and resolve any application problem before choosing **Acknowledge interruption**. The server checks that the operation's worker is absent or stopped before releasing this gate. Acknowledgement preserves the failed/uncertain result and does **not** retry the operation, mark it successful, or perform rollback; it permits other queued work to resume.

**Initial installation:** installing this feature still requires an external Compose update, including the updated volume configuration. Subsequent self-updates require compatible durable-operation and worker protocols. Keep the data volume when replacing or updating Services: **do not use `docker compose down -v`** unless you deliberately want to delete the operation database. Back up the data volume when Services and its workers are stopped, or use a SQLite-aware online backup procedure; copying only the live database file is not a reliable backup.

### Optional host-folder storage

Named volumes remain the default; they are not required. To use a host folder, create `data` beside your Compose file and replace the `services-data:/data` mount with `./data:/data`. Keep `SERVICES_DATA_DIR=/data` and the existing project-root and Docker-socket mounts. The included permanent override uses the explicit form and refuses to create a missing host folder automatically:

```yaml
services:
  services:
    volumes:
      - type: bind
        source: ./data
        target: /data
        read_only: false
        bind:
          create_host_path: false
```

For the supplied deployment, apply it with `docker compose -f compose.yaml -f compose.bind.yaml up --build -d`. If your existing main file is `docker-compose.yml`, use that filename instead. Keep both Compose files permanently in the mounted application directory: Compose records their paths for subsequent updates. You may instead edit the main file directly and omit the override. An unused top-level named-volume declaration does not migrate or delete any existing data.

The host folder must already exist and be writable by the non-root container user. On Linux, check that user's UID/GID (for example with `docker exec services id`) and set the directory ownership appropriately; do not solve this by making the worker privileged or running it as root. Docker Desktop on Windows also requires access to the shared drive/folder. A bind mount hides the image's preconfigured `/data` directory and its ownership. SQLite and the coordination reservation require reliable local filesystem locking; do not place this directory on SMB/NFS/NAS or use it from Docker Engines on different machines. Read-only, temporary, ambiguous, and nested mounts beneath `/data` are rejected.

Bind-backed self-updates additionally require the target image's `io.services.storage.bind=1` capability label. An older image without host-folder support is rejected before replacement; named-volume deployments retain the existing worker-protocol compatibility rules. Installing host-folder support on an older deployment requires an external update before enabling the bind mount.

#### Moving an existing queue to a host folder

There is no automatic storage migration and no database-schema change. Do **not** change mounts during an in-app update: the running helper would still write to the previous storage.

1. Let accepted work finish, resolve any uncertain outcome, and confirm no Services worker containers are running. Stop every Services coordinator that shares the old storage; keep them stopped throughout the copy.
2. Back up the old storage. Find its actual volume name with `docker inspect services --format '{{json .Mounts}}'`; do not assume it equals the short Compose name.
3. Copy the **entire stopped `/data` directory**, including hidden files, `operations.db`, any `operations.db-wal`/`operations.db-shm` files, and `operations.db.coordinator-lock`, into an empty host folder. Preserve writable permissions for the Services user. For example, using the new locally built image and explicit replacement paths:

   ```sh
   docker run --rm --entrypoint sh --mount type=volume,source=ACTUAL_OLD_VOLUME,target=/old,readonly --mount type=bind,source=ABSOLUTE_EXISTING_HOST_DATA_FOLDER,target=/new services:host-storage -c 'cp -R /old/. /new/'
   ```

   `services:host-storage` is an example local tag; substitute your new compatible image. Replace both uppercase placeholders before running. Do not copy a live database or merge independent queues.

4. Apply the new image and storage configuration externally, retaining the same Compose project identity. Start Services, check `/api/ready` for `ready: true` and `mutationsEnabled: true`, and confirm the saved operation IDs and history are present.
5. Keep the old volume as a backup until the migration is verified. Do not run coordinators against both old and new copies: they are independent queues and no longer share the same lock.

### Deployments with a networking sidecar

Networking providers are detected from resolved Compose configuration and actual Engine `HostConfig.NetworkMode`, not service names. An in-app update of a namespace provider (Tailscale or otherwise) also recreates its running same-project namespace consumers, transitively, even when their images have no update. Consumers are stopped leaf-first; each provider must be ready before its consumers are recreated. Each replacement must reference its **direct provider's new container ID**, including intermediate namespace-sharing providers. Updating only Services does not pull or replace its unchanged provider.

Supported references are `network_mode: service:<same-project-service>` and `network_mode: container:<stable-same-project-container-name>`. Missing or external providers, literal container IDs, cycles, multiple containers for one service, configuration drift, and stale running namespace attachments fail closed before applying. The updater does not manage general `depends_on` rollouts, external namespace providers, builds, or application-specific VPN authentication. A provider with no Docker health check is verified as running; configure an appropriate health check if consumers must wait for stronger application readiness.

Intentionally stopped consumers retain their IDs and stopped state, even if their old namespace is later retired. Reconcile them with an explicit external Compose `up` when you intend to start them; Services does not start them as a side effect of a provider update. Their preserved state is recorded in the plan. A running consumer of a stopped provider is rejected. Unchanged consumers whose local image tags drifted must be explicitly updated or have their tags restored before retrying. No temporary Compose override files are created or recorded in persistent Compose labels.

The durable worker uses an independent bridge network, so it survives replacement of both a networking sidecar and the Services dashboard. Services readiness is checked even when it is recreated only for namespace reattachment. Worker-protocol and bind-storage capability labels are required only when introducing a different Services image, not for reattaching its installed immutable image.

Keep the internal Services port at 8080 for readiness checks; a sidecar may publish a different external port. The temporary worker does not need Tailnet credentials or a shared network namespace: replacement readiness is checked by executing a probe inside the replacement Services container through Docker.

### Shared Compose operation queue

`POST /api/projects/{name}/update` returns **202 Accepted** only after the operation is committed, with a JSON operation instead of a progress stream. Project updates run in FIFO submission order, one Compose mutation at a time, across all tabs and dashboard server instances using the **same operation volume**. Separate installations/volumes are not coordinated. A duplicate submission for an already queued or running project update returns **202 with the same operation ID**, without starting another update. Up to **32 waiting operations** are accepted in addition to the active action; further submissions return **429 Too Many Requests** (duplicates are still accepted).

- `GET /api/operations` returns `{ "operations": [...] }`, newest submitted first, including active legacy container actions.
- `GET /api/operations/{id}` returns one operation, or **404** for an unknown or evicted ID. Both read endpoints disable caching.
- `POST /api/operations/{id}/acknowledge` acknowledges an operator-reviewed interrupted result. It requires the same origin/allowed-host checks as update submission and verifies the worker is no longer active. It does not resubmit the action.
- An optional `Idempotency-Key` header coalesces retries of the same request across restarts, including a completed request while retained. Reusing a key for a different action returns **409**.
- Idempotency keys are retained as replay-prevention tombstones even after their operation history is evicted; a key for an evicted operation returns **409** rather than starting the action again. This ledger is not currently pruned, so bounded operation history does not imply a fixed-size database. Monitor free space on the data volume.
- An operation contains `id`, `project`, `action` (`update project`, `pull`, or `update`), `state` (`queued`, `running`, `complete`, or `error`), ISO-8601 UTC `queuedAt`, `messages`, and `queuePosition` (1-based while waiting, otherwise 0). `startedAt`, `finishedAt`, and `error` appear when applicable. Recovery metadata includes `phase`, `selfUpdate`, `needsAttention`, a temporary `worker` identity, `expectedImages` (planned service names mapped to immutable image IDs), and an optional `plan.services` array. Each planned service records `service`, `oldId`, `oldImage`, `expectedImage`, `provider`/`providerOldId` when applicable, `running`, and `reason` (`image update`, `network reattachment`, or `preserve stopped`). Resolved Compose configuration is never stored in the operation or progress log. Poll the list about every two seconds and an individual active operation about every second; only a terminal state confirms the recorded result. `error` with `needsAttention` means the outcome remains uncertain and the queue is paused.

Execution rechecks live Docker inventory, registry update status, and project identity **when execution starts**, not from the inventory shown when submitted. A project that disappeared, no longer has an available update, or cannot be checked against Docker becomes an `error` operation. Each executing operation has a bounded worker-owned lifetime, independent of browser or dashboard disconnection; there is no browser cancellation or automatic rollback. Uncertain execution outcomes require reconciliation rather than an automatic retry.

Legacy container pull/update endpoints now submit through the same durable FIFO and retain NDJSON progress with explicit terminal records. The first `accepted` record carries an `operationId`; only `complete` confirms success, and `error` reports a recorded failure. A stream lost during a dashboard restart is not a terminal result: look up the saved operation. These actions cannot jump ahead of queued projects.

Queue and activity are **durable in the configured data volume**. Keep at most 100 resolved terminal operations plus queued/running operations and unresolved interruptions. Each operation retains its newest 200 progress messages, with each message and terminal error limited to 4,096 UTF-8 bytes. Older output and terminal history are discarded. This is bounded operational visibility, not an audit log or a guarantee that every command line is retained. Deleting the volume deletes this history and pending work.

## Run

With Docker running, from this directory:

```sh
docker compose up --build -d
```

Open `http://localhost:8080`. The supplied Compose file targets Docker Desktop on Windows with application projects under `E:/Containers`.

Configure these environment variables (for example in a local `.env` file):

| Variable | Purpose |
| --- | --- |
| `SERVICES_COMPOSE_HOST_ROOT` | Root as written in container Compose labels; defaults to `E:/Containers`. |
| `SERVICES_COMPOSE_MOUNT_ROOT` | Matching path visible to both Services and the Docker daemon; defaults to `/run/desktop/mnt/host/e/Containers`. |
| `SERVICES_ALLOWED_HOSTS` | Additional exact hostnames allowed to perform actions, comma separated. Add your LAN hostname/IP if needed. |
| `SERVICES_PUBLIC_ORIGIN` | Exact browser origin when using a TLS-terminating proxy, e.g. `https://services.example.net` (no trailing slash). |
| `SERVICES_WEBHOOK_URL` | Optional generic JSON notification receiver. |
| `SERVICES_PUBLISH_TOKEN` | Optional notification-only bearer secret, 32–512 printable non-space ASCII characters. Requires an explicit repository allowlist. |
| `SERVICES_PUBLISH_REPOSITORIES` | Exact untagged GHCR repositories allowed to submit publication metadata, comma separated. No wildcards. |
| `SERVICES_PROJECT_NAME` | Services' own Compose project name; defaults to `services`. |
| `SERVICES_DATA_DIR` | Must be `/data` backed by a writable named volume or local host-folder bind. Named volumes use the image directory's initial ownership; a host folder needs compatible permissions of its own. |
| `SERVICES_SERVICE_NAME` | Optional Services Compose service identity; defaults to `services`. Configure custom deployments explicitly. |
| `SERVICES_CONTAINER_NAME` | Optional container identity used for inspection; otherwise uses Docker's `HOSTNAME`. Needed if a custom hostname prevents self-discovery. |
| `DOCKER_SOCKET` | Unix socket path inside the Services container; defaults to `/var/run/docker.sock`. |
| `LISTEN_ADDR` | HTTP listen address; defaults to `:8080`. |

For Linux, override the two roots with your real application root and adjust the Compose bind mount accordingly, e.g. both `/srv/containers`. Docker Desktop paths must be verified on the target installation. Top-level working directories and Compose files outside the configured root are rejected. On Docker Desktop, labels may use either the Windows host root or its configured Unix mount root; both resolve to the same mounted project. This supports projects whose services were created from both Windows and inside Services without broadening the allowed filesystem boundary.

### Security boundary

There is no built-in login. Keep Services behind Tailnet ACLs or an authenticated trusted proxy; do not expose port 8080 to the public internet. Mutation endpoints require a matching browser Origin and an allowed request hostname. Loopback and Tailnet IPs are allowed by default. These checks are not a replacement for authentication.

**Docker socket access is effectively host-control access.** A read-only socket bind does not make API calls read-only. The application must be able to pull images and recreate containers. Anyone compromising Services could control Docker.

The application-project bind is read-only, but Services must read trusted Compose and `.env` files. Referenced `include`, `extends`, `env_file`, configs, secrets, and Compose provider commands are not a separate sandbox. Restrict who can modify your project files and keep references inside the mounted root. Compose subprocesses use a fixed environment instead of inheriting application secrets. Configuration changes on the host are not frozen during an update.

## Development and verification

Requires Go 1.24+ and Node.js with npm.

```sh
node --test scripts/*.test.mjs
cd web
npm ci
npm test
npm run build
cd ..
go test ./...
go vet ./...
go build ./cmd/services
```

Notification tests use local HTTP/registry fixtures and temporary SQLite databases, not a live GHCR publish or an application update. The workflow uses GitHub's documented `concurrency.queue: max`; actionlint 1.7.12 does not yet recognize that property. If using that linter version, suppress only its specific `unexpected key "queue" for "concurrency" section` diagnostic, not other syntax or expression checks.

The Go server serves `web/dist`. Run `go run ./cmd/services` after building the frontend. Native Windows development can exercise API and UI behavior, but the production Docker connector uses a Unix socket inside the Linux container.

Tests use local Docker/registry HTTP fixtures and fake Compose runners; they do not update your real applications. Storage tests reopen real temporary databases. Worker and recovery tests must cover duplicate submission, dashboard restart, missing/stopped workers, and uncertain results as well as normal completion. Mount tests cover both named volumes and exact daemon-reported host paths, read-only/unsupported/ambiguous storage rejection, and bind-capability checks before self-update. Real socket permissions, named-volume or host-folder ownership, cross-process SQLite locking, mounted project paths, registry credentials/availability, and actual Compose recreation must also be verified on the target host.

## Versioning and publishing

CI tests the frontend and Go packages and builds the container. Default-branch and `v*` tag pushes publish `ghcr.io/everyonesoftware/services`. The numeric build version is the Git commit count; `APP_VERSION` defaults to `0` for local builds. The Compose image tag defaults to `latest`.
