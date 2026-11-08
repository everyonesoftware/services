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

### Notifications

Set `SERVICES_WEBHOOK_URL` to an HTTP(S) endpoint that accepts a JSON POST to receive problem and recovery events even when the dashboard is closed. An event has `id`, `project`, `name`, `kind`, `message`, and `time` fields. This is a generic webhook, not a Discord/Slack-specific payload; use a compatible receiver or an automation bridge.

Unchanged incidents are deduplicated. Failed deliveries are retried within the running process. No webhook is configured by default; without one, alerts are available in the dashboard only. Delivery history is not durable across restarts. Keep token-bearing webhook URLs secret and prefer HTTPS outside a trusted network.

## Image updates

Services checks public Docker Hub and GHCR image tags at startup and periodically (six hours; failed checks retry after fifteen minutes). **Check for updates** requests an immediate check. It compares the configured tag's remote manifest digest with the running image's repository digest—not version numbers.

Unsupported registries, digest-pinned images, missing repository digests, and registry failures are explicitly distinguished from an up-to-date image. Private-registry authentication is not supported. Updates to a different tag, such as moving from `1` to `2`, require editing your Compose configuration yourself.

Choose **Review update** on a Compose group to see the changed services and digests, then explicitly confirm the update. The server deduplicates replicas and updates changed Compose services together:

1. Validate the project's paths and selected service configuration.
2. Pull the selected services' current tags.
3. Run `docker compose up -d --no-deps --pull never --no-build --wait --wait-timeout 300` for those services.
4. Report success only when Compose reports completion; retain failures and command progress in the shared operation activity.

This recreates containers and can cause downtime. It uses the Compose files and `.env` values on disk at execution time. Mutable tags may change between review and pull. No builds, dependency startup, volume removal, automatic updates, or automatic rollback are performed. Back up application data before updating. Compose operations are serialized to prevent overlapping updates. Closing a browser does not roll an update back. The Services project is protected from self-update; update Services externally with Compose instead. Set `SERVICES_PROJECT_NAME` if its Compose project is not named `services`.

### Shared Compose operation queue

`POST /api/projects/{name}/update` returns **202 Accepted** with a JSON operation instead of a progress stream. Project updates run in FIFO submission order, one Compose mutation at a time, across all tabs and dashboard server instances in the process. A duplicate submission for an already queued or running project update returns **202 with the same operation ID**, without starting another update. Up to **32 waiting operations** are accepted in addition to the active action; further submissions return **429 Too Many Requests** (duplicates are still accepted).

- `GET /api/operations` returns `{ "operations": [...] }`, newest submitted first, including active legacy container actions.
- `GET /api/operations/{id}` returns one operation, or **404** for an unknown or evicted ID. Both read endpoints disable caching.
- An operation contains `id`, `project`, `action` (`update project`, `pull`, or `update`), `state` (`queued`, `running`, `complete`, or `error`), ISO-8601 UTC `queuedAt`, `messages`, and `queuePosition` (1-based while waiting, otherwise 0). `startedAt`, `finishedAt`, and `error` appear when applicable. Poll the list about every two seconds and an individual active operation about every second; only a terminal state confirms the result.

The server rechecks live Docker inventory, registry update status, and self-update protection **when execution starts**, not from the inventory shown when submitted. A project that disappeared, no longer has an available update, or cannot be checked against Docker becomes an `error` operation. Failure releases the queue so later projects can continue. Each executing project operation has a 15-minute background context, independent of browser disconnection; there is no browser cancellation or rollback.

Legacy container pull/update endpoints retain their NDJSON progress and explicit terminal records. They share the same serialization, appear in operation activity once execution begins, and return **409 Conflict** while the queue is busy; they cannot jump ahead of queued projects.

Queue and activity are **in memory, not durable across process restarts**. Keep at most 100 terminal operations plus queued/running operations. Each operation retains its newest 200 progress messages, with each message and terminal error limited to 4,096 UTF-8 bytes. Older output and terminal history are discarded. This is bounded operational visibility, not an audit log or a guarantee that every command line is retained.

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

Tests use local Docker/registry HTTP fixtures and fake Compose runners; they do not update your real applications. Real socket permissions, mounted project paths, registry credentials/availability, and actual Compose recreation must also be verified on the target host.

## Versioning and publishing

CI tests the frontend and Go packages and builds the container. Default-branch and `v*` tag pushes publish `ghcr.io/everyonesoftware/services`. The numeric build version is the Git commit count; `APP_VERSION` defaults to `0` for local builds. The Compose image tag defaults to `latest`.
