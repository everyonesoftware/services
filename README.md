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

Project and container names have small, locally bundled SVG icons. Recognized applications use the existing Simple Icons brand artwork and colors; each icon's upstream source and any brand guidelines are recorded in that dependency's metadata. No external image requests or runtime logo-search service are used.

The curated mapping in `web/src/brandIcons.js` matches image repository basenames and explicit aliases, ignoring registry hosts, namespaces, tags, and digests. It does not guess from container names or treat a brand string anywhere in an image reference as a match. Custom images and applications outside the mapping receive a neutral container icon rather than an invented logo.

A Compose project uses its single recognized application brand, ignoring Tailscale sidecars. Tailscale-only projects use Tailscale; mixed application brands, unknown applications with only a recognized VPN sidecar, and the Standalone group use a neutral stack icon. Container rows always keep their individual image's icon, and health indicators remain separate.

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

### Restart-resilient execution and self-update

The supplied deployment stores operations in SQLite under `/data`, backed by the `services-data` named volume by default. A writable local host-folder bind at `/data` is also supported. Accepted work, submission order, recent output, and results survive replacement of the Services container. This does **not** persist monitoring incidents, registry-check caches, or webhook delivery history.

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
cd web
npm ci
npm test
npm run build
cd ..
go test ./...
go vet ./...
go build ./cmd/services
```

The Go server serves `web/dist`. Run `go run ./cmd/services` after building the frontend. Native Windows development can exercise API and UI behavior, but the production Docker connector uses a Unix socket inside the Linux container.

Tests use local Docker/registry HTTP fixtures and fake Compose runners; they do not update your real applications. Storage tests reopen real temporary databases. Worker and recovery tests must cover duplicate submission, dashboard restart, missing/stopped workers, and uncertain results as well as normal completion. Mount tests cover both named volumes and exact daemon-reported host paths, read-only/unsupported/ambiguous storage rejection, and bind-capability checks before self-update. Real socket permissions, named-volume or host-folder ownership, cross-process SQLite locking, mounted project paths, registry credentials/availability, and actual Compose recreation must also be verified on the target host.

## Versioning and publishing

CI tests the frontend and Go packages and builds the container. Default-branch and `v*` tag pushes publish `ghcr.io/everyonesoftware/services`. The numeric build version is the Git commit count; `APP_VERSION` defaults to `0` for local builds. The Compose image tag defaults to `latest`.
