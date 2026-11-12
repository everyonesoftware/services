# Services

A home-server monitoring console for one Docker Engine. Problems are shown first; applications are grouped by Compose project; image updates are always opt-in.

## Accounts and API keys

Services requires an administrator login for the dashboard and management API. On the first start, open the dashboard and create the initial administrator using the local token in `/data/admin-setup-token`. Read that file locally from the mounted data volume; do not put it in chat, GitHub, or logs. The token is generated automatically, survives restarts before setup, and cannot create another first administrator after setup. A later startup removes the obsolete file. No account password or API key needs to be added to Compose or an environment variable. Authentication storage failure prevents the HTTP server from starting rather than exposing an unprotected dashboard.

After login, open **Settings** using the gear beside the signed-in user to add/remove administrator accounts, change your password, and create/revoke integration keys. The page uses `#settings`; existing `#accounts` bookmarks still open Settings. All user accounts in this first version are administrators; API keys have separate, restricted permissions. You cannot delete your own account or the last administrator. Passwords require at least 15 Unicode characters and at most 1024 UTF-8 bytes. Browser sessions expire after 12 hours; logout revokes the current session, and password changes or account deletion invalidate that user's sessions. Cookies are HttpOnly and SameSite=Strict; HTTPS deployments must configure `SERVICES_PUBLIC_ORIGIN` so Secure cookies work behind TLS-terminating proxies. Login/setup requests are rate limited and browser mutations retain same-origin/allowed-host checks.

Login and setup share a limit of ten requests per minute per direct network peer, including malformed requests, with at most four concurrent password-derivation operations. Slow/incomplete request bodies do not reserve those derivation slots; the HTTP server bounds body-read time. Services does not trust `Forwarded`, `X-Forwarded-For`, or `X-Real-IP` for rate limits. Users behind the same reverse proxy (including a Tailscale HTTP proxy) share that proxy's limit. Configure additional per-client throttling at the trusted proxy when needed; do not assume adding forwarding headers gives users independent buckets.

| API-key scope | Allowed access |
| --- | --- |
| `services:read` | Read service inventory, monitoring, operation progress, icons, and version. No changes or account/key administration. |
| `publications:write` | `POST /api/images/published`, restricted to the key's explicit untagged lowercase GHCR image repositories. Integration-owned keys can also answer their own short-lived publisher connectivity challenge. No dashboard reads or container operations. |

API keys are shown only when created; save them directly in your secret manager or GitHub Actions secrets. Optional expiration and immediate revocation are supported. The list shows metadata, not the secret. Set the `Authorization` header to the `Bearer` scheme followed by your API key, and send it over HTTPS from server-side clients; browser Origin/Fetch-Site headers are rejected for API-key requests. Keys cannot create other keys or acquire administrator permissions. A dedicated key per integration makes revocation independent. The public exceptions are the static login page/assets, setup-status endpoint, login/setup submissions, and readiness probe; management data requires a session or a permitted key.

Credentials and session/key hashes are persisted in `/data/auth.db`; password hashes use a unique salt and PBKDF2-HMAC-SHA256 with 600,000 iterations. Treat the database, any SQLite sidecar files, and backups as sensitive. Keep the persistent data volume when replacing/updating Services, and use stopped-copy or SQLite-aware backups. Deleting `auth.db` is not a supported password-reset procedure. Local operator recovery is available without opening the HTTP API:

```sh
# Supply the new password through standard input, not a command-line argument.
# Run locally with a trusted input source; never commit a password file.
services auth reset-password --username admin --password-stdin
```

In Docker, use the existing container's `/app/services` executable via `docker exec -i` with password input piped from your local secret source. Recovery requires local access to the existing data volume, invalidates the account's sessions, and does not start a listener or perform Docker operations. This version does not include email recovery, MFA, or OAuth login. The separate GitHub publisher connection below configures an integration; it does not replace Services administrator login or provision Tailscale administration.

## Monitoring

Monitoring runs on the server, not in a browser tab. **Monitor** shows active incidents, container diagnostics, service controls, and image updates. **Activity** combines Services-managed Compose operations and recent monitor events in one feed.

Activity has an always-visible search field and icon dropdowns for application, activity type, and sorting. Hover a dropdown for its current selection, or use the accessible keyboard controls. Newest/oldest sorting uses the operation's finished time, otherwise its started or queued time; monitor events use their observed time. Longest/shortest compares execution runtime, not queue wait. Running operations show elapsed time so far; queued work, point-in-time monitor events, and uncertain interrupted results have no comparable runtime and are excluded from duration sorting with an explanation. Search and filters do not hide queue-pause warnings or interruption acknowledgement. Command output, raw output, and follow-output controls remain available by expanding an operation. Compose operations refresh every two seconds and monitor events every fifteen seconds.

- Docker health checks are used when configured. A failed check is a problem even while the process is running.
- Without a health check, Services monitors container state and restart activity. **Running** means the process is up; it does not claim the application is responding correctly.
- Repeated restarts and observed container replacements are tracked over a five-minute window for applications without health checks, including containers that happen to be running when sampled. A configured Docker health check remains authoritative while running; restart counts remain visible as diagnostics.
- Stopped, restarting, dead, paused, and out-of-memory-killed applications need attention. Successful Compose one-off jobs are distinguished from failed applications.
- Non-Compose containers appear under **Standalone** and cannot be updated through Compose actions.
- Docker outages are shown separately from application failures. The last successful inventory stays visible and is marked stale; it is not presented as a live healthy result.
- Active incidents and recent history are in memory. They reset when Services restarts. Polling is not an exhaustive Docker event log: containers created and removed entirely between polls may not be observed.

### Resolving missing services

When a previously observed service disappears, choose **Resolve…** on its missing incident or container row. The same dialog combines the disappearance details, observed history, and possible replacement suggestions with the resolution decision. Suggestions use current services in the same project with a matching image reference; they are not proof that a rename occurred.

- **Renamed or replaced:** confirm the current replacement. Services records the relationship in activity history and clears the old missing monitoring entry. It does not rename a Docker container.
- **Intentionally removed:** forget the old monitoring expectation and clear its incident. If that identity returns later, Services discovers and monitors it again.
- **Not sure yet:** snooze that entry's alerts for 15 minutes, one hour, or 24 hours. The service stays visibly missing, but does not count as an active unsnoozed alert. Other services' alerts are unaffected. Alerting resumes if it remains missing when the snooze expires.

**Resolve both/all missing entries…** opens the same experience for bulk handling. Apply one choice to all selected entries, override individual decisions, or exclude entries before applying. Each batch supports up to 100 selected entries. No changes are made until confirmation, and the entire batch is rejected if an entry's observed state has changed. The server checks fresh Docker inventory rather than trusting the browser's cached view.

After a successful decision, **Undo** can restore the previous monitoring expectations for up to five minutes, provided those identities have not returned or been changed by a later decision. Resolution and Undo require an administrator session and same-origin checks; read-only and publisher API keys cannot use them. These actions never pull images, run Compose, restart containers, or delete containers, files, or volumes.

Monitoring history, resolutions, snoozes, and Undo tokens are in memory, like the existing monitoring snapshot, and reset when Services restarts. They are not a durable incident journal.

### Service icons

Project and container names have small, locally bundled official GitHub organization avatars or application artwork, with light gray circular backgrounds. Asset provenance is recorded in `web/public/icons/sources.json`. No external image requests or runtime logo-search service are used while displaying icons.

The curated mapping in `web/src/brandIcons.js` matches image repository basenames and explicit aliases, ignoring registry hosts, namespaces, tags, and digests. It does not guess from container names or treat a brand string anywhere in an image reference as a match. Custom images and applications outside the mapping receive a neutral container icon rather than an invented logo.

A Compose project uses its single recognized application brand, ignoring Tailscale sidecars. Tailscale-only projects use Tailscale; mixed application brands, unknown applications with only a recognized VPN sidecar, and the Standalone group use a neutral stack icon. Container rows always keep their individual image's icon, and health indicators remain separate.

#### Custom project and container icons

Hover over a project or container icon to reveal its pencil, or focus it with the keyboard, then click to open **Edit icon**. Choose **Image URL** to paste a direct image link and **Download & save**, or **Upload image** to select a file from your device and **Upload & save**. Selecting a file shows its name without uploading it; nothing is sent until you save. Services validates the image, normalizes it to PNG, and stores a local copy under `SERVICES_DATA_DIR/icons` (`/data/icons` in the supplied Compose deployment). Reloading the dashboard serves that stored copy; it does not contact the original image host. **Use default icon** removes the override and restores automatic artwork. Cancel and rejected images leave the current icon unchanged.

Project overrides and individual container overrides are independent. Projects are identified by project name; container overrides use the full container name, not the replaceable Docker ID, so ordinary recreation with the same name preserves the override. Renaming a project or container gives it a different identity. No Compose or container changes are made by editing an icon.

Both uploaded files and URL imports support PNG, JPEG, GIF (the first frame is used), and a restricted static SVG subset, with a 2 MiB file limit and 4 million pixels, with neither side exceeding 4096 pixels. The server validates the actual image bytes rather than trusting the filename or reported MIME type. Uploaded filenames are not used as storage paths, and uploaded icons do not retain a remote source URL. URL imports additionally require public HTTP(S) destinations: credentials in URLs and private, loopback, link-local, or other non-public network destinations are rejected, including redirect destinations. To use an image from your LAN or Tailscale network, save it to your device first and use **Upload image**. Uploading an existing local file does not require internet access.

SVG imports are parsed using strict element and attribute allowlists, then rendered to static PNG. The original SVG is never stored, served, or previewed in the browser. Basic paths, shapes, groups, solid colors, and previously defined local linear/radial gradient fills are supported. Dimensions must be explicit through a `viewBox` or matching width/height; imported SVGs are proportionally reduced to at most 512 pixels per side. Unsupported files are rejected rather than partially sanitized or silently rendered differently. This is not full SVG compatibility: scripts, event handlers, external references, entities/DOCTYPE, CSS/style, text/fonts, embedded images, filters, animation, `use`, gradient inheritance, `evenodd` fills, and rotation/skew transforms are not accepted. Group/inherited opacity and compositing effects are also outside the supported subset; per-shape fill/stroke opacity is supported. Supported transforms are translate, scale, and matrix.

SVG complexity is bounded to 512 elements, depth 16, 24 attributes per element, 64 KiB total path data, and 8192 path/point tokens, with additional numeric and cumulative-transform limits. Rendering uses at most two child processes, with a three-second queue/execution deadline and bounded PNG output. Both the parent and child validate the input; the byte-stream renderer has no accepted external-resource loading path. The subprocess is a timeout/isolation boundary, not an operating-system filesystem/network sandbox or a guarantee that arbitrary SVG is harmless. Rejected imports preserve the existing saved icon.

Custom icon editing requires an explicitly configured absolute, writable persistent `SERVICES_DATA_DIR`. If storage cannot be initialized, editing is disabled without disabling monitoring. If a metadata write cannot be confirmed durable, editing is disabled until Services restarts and reloads the visible metadata; a failed request is not proof that no disk change occurred. Resolve storage errors before restarting or retrying. Keep the entire icons subfolder with your data backup. Icon mutations require an administrator session and matching Origin/Host checks; read-only and publisher keys cannot edit icons.

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

Create an API key in **Settings → API keys** with `publications:write` and an explicit image-repository allowlist, for example `ghcr.io/everyonesoftware/services`. Wildcards and tag suffixes are rejected. Save that key in the publishing repository's `SERVICES_PUBLISH_TOKEN` Actions secret, never in committed files or workflow output. The receiver is initialized automatically when verified persistent publication storage is available; no Services-side `SERVICES_PUBLISH_*` environment settings are needed for managed keys. Existing installations may retain the legacy `SERVICES_PUBLISH_TOKEN` (32–512 printable non-space ASCII characters) and `SERVICES_PUBLISH_REPOSITORIES` pair for migration. A legacy token remains notification-only and cannot authenticate dashboard or management routes.

Keep the endpoint behind Tailscale ACLs or an authenticated trusted private proxy. It requires an allowed request hostname (the configured public origin is automatically allowed), a bearer token, unencoded `application/json`, and at most 64 KiB. Browser `Origin` and `Sec-Fetch-Site` headers are rejected; existing browser-action origin protections remain unchanged. The Services environment requires `/data` on a verified writable persistent named volume or local bind. Configuration/storage failure disables notifications without disabling monitoring. Worker containers do not inherit the notification credential.

Accepted publication metadata is committed to a separate `/data/publications.db` before acknowledgement, survives Services replacement, and is independent of the operation journal. Treat its SQLite database and any WAL/SHM files with the same stopped-copy or SQLite-aware backup precautions as the operation journal. HTTP 200 returns `{ "result": "accepted" }`, `duplicate`, or `stale`; only accepted newer metadata replaces the appropriate tag's known publication. Conflicting retained event identities return 409. Malformed metadata returns 400, invalid credentials 401, out-of-scope repositories or browser/untrusted-host requests 403, oversized bodies 413, unsupported media 415, and disabled/unavailable persistence 503. No acknowledgement claims an image has been installed.

#### Push publisher setup

The automatic adapter accepts plain newline-delimited Docker build arguments. Quoted or comma-containing argument lists require manual integration review; Services does not reinterpret those inputs or guess which version binding wins.

Open **Publishers** in the administrator navigation and choose **Connect GitHub repository**. The setup wizard automates repository configuration around the existing **push** receiver; Services does not poll GitHub for image publications.

Prerequisites: deploy this version with a verified persistent `SERVICES_DATA_DIR`, publication ingestion available, and an HTTPS `SERVICES_PUBLIC_ORIGIN` such as `https://services.tail1a7c8f.ts.net`. The public origin is used for the manifest callback and the private notification endpoint; it must be reachable by your browser and, through Tailscale, the CI runner. Publisher setup is disabled if those prerequisites fail, without exposing an unauthenticated dashboard.

1. **GitHub App:** choose personal-account or organization ownership, review the GitHub App manifest, then approve registration on GitHub. A private App must be registered under the organization whose repositories it will access. The manifest requests repository Actions, Contents, Workflows, Pull requests, and Secrets write permissions plus Metadata read. GitHub rejects Variables permission in the manifest, so return to Services and use **Open GitHub App settings** to grant **Permissions & events → Repository permissions → Variables → Read and write**, then save. Existing installations must also approve the updated access. Use **Check GitHub permissions**, then install the App on selected repositories. Services verifies the App grant and rechecks the selected installation before reviewing/provisioning repository setup; cached status is not authorization. Repository configuration remains automated after this one-time approval. No personal access token or private key is pasted into Services.
2. **Repository:** discover installed repositories on demand, select one, and confirm the exact lowercase GHCR image repository. A suggested image name is editable, not proof of the workflow's actual published image. Selecting a repository does not require Tailscale settings first.
3. **Tailscale:** follow the repository-specific instructions and links to create an OpenID Connect trust credential. Services reads the repository’s actual GitHub OIDC subject prefix, including immutable owner/repository IDs where enabled, and displays the exact default-branch subject and issuer. Unsupported custom subjects require manual review rather than a guessed identity. A tailnet administrator grants Auth Keys write with the runner tag and network access to Services HTTPS, then copies the nonsecret client ID, audience, and tags. Settings are saved per repository, not reused from another repository’s credential. Existing publishers retain their legacy settings; adding another repository cannot change them. The wizard does **not** automatically create federation, grant network access, or request an OAuth client secret. Displayed subjects cover default-branch jobs without a GitHub environment; other publication contexts need separately reviewed trust rules.
4. **Review setup:** inspect the proposed configuration and workflow file changes. Existing supported notification hooks are reused. Where automatic workflow installation is safely supported, explicitly approve a setup PR, merge it under your repository's branch rules, then request a fresh review. Unsupported or ambiguous workflows remain blocked with explanations; Services does not guess a digest, tag, image, or publishing job. It does not push workflow edits directly to the default branch.
5. **Apply & test push:** Services creates a dedicated image-scoped publisher key, encrypts it using GitHub's repository public key, uploads it directly as `SERVICES_PUBLISH_TOKEN`, and creates missing endpoint/federation variables. Differing existing variables or an existing secret stop fresh provisioning rather than silently replacing them. Approvals bind the selected repository, image, current connection settings, and reviewed workflow snapshot; changed plans require another review.
6. **Verification:** a dedicated workflow sends an authenticated, expiring connection challenge to `POST /api/publishers/callback`. The test does not build/publish an image or manufacture publication metadata. Services enables `SERVICES_PUBLISH_NOTIFICATIONS` only after receiving and persisting the matching proof and verifying the GitHub variable readback. A successful workflow dispatch alone is not proof of connectivity. While waiting, the browser refreshes Services' own saved acknowledgement; it does not poll GitHub.

The connection remains **Connectivity verified** until a real image announcement supplies publication metadata. The connection card shows its last actual accepted publication separately from test status, including version, digest, tags, and source workflow. Ingestion still performs no registry lookup or Docker operation. Existing manual update review is unchanged.

Automatic workflow adaptation is intentionally bounded: one unambiguous GHCR `docker/build-push-action` producer, its actual tags (direct tags or `docker/metadata-action` output), an existing numeric version source passed as `APP_VERSION`, `VERSION`, or `BUILD_VERSION`, conventional hosted `ubuntu-latest`, `ubuntu-22.04`, or `ubuntu-24.04` job execution, and a structurally verified original-payload artifact chain. Recognized version arguments must share the same source; duplicate arguments, conflicting bindings, and missing source dependencies require manual review. Services preserves the existing build arguments and version source instead of renaming arguments or introducing a new counter. Supported sources include positive integer literals, `github.run_number`, earlier unconditional step outputs, declared dependency-job outputs, and resolvable workflow/job environment values. Equivalent expression spacing and uniform LF or CRLF line endings are supported, with the original line-ending convention retained. Known publication events are `push`, `workflow_dispatch`, and `release`; PR verification may coexist, but privileged PR-target/custom event chains are not approved. Matrices, aliases, unsupported expressions, mixed line endings, custom execution contexts/defaults/concurrency, alternate checkout locations, and ambiguous producers require manual review rather than speculative edits. An unsupported setup plan means Services could not safely adapt the integration, not that the repository's publishing workflow is broken. This is not a generic workflow rewriter.

**Recovery and lifecycle:** retry an expired/failed connectivity test using the saved connection. For an interrupted initial provisioning operation, **Recover setup** reconciles missing/identical configuration with the saved key, never blindly overwriting an existing secret; the authenticated callback must still prove the key is actually installed. Known conflicts revoke the newly created local key and require explicit repository conflict resolution. **Rotate key** retains the previous key until a callback authenticated with the replacement succeeds; uncertain retries reuse the saved replacement instead of minting additional keys. **Disconnect** revokes local current/pending keys and attempts to disable the integration's notification variable; a remote cleanup failure remains visible. It does not delete repository workflow files/secrets, shared Tailscale access, or retained publication history. GitHub secret values cannot be read back: metadata checks plus the authenticated push test establish delivery, not the secret PUT response alone.

If GitHub could not be disabled during disconnect, **Retry GitHub cleanup** remains available on the disconnected card until the remote change is verified. Local key revocation does not wait for GitHub, and obsolete cleanup does not disable a newer connection for the same repository. A revoked or expired pending replacement can be rotated again while retaining the original previous credential. Receipt retries and concurrent notifications preserve publication-time/run/attempt ordering rather than arrival order.

**Private integration storage:** GitHub App signing material and retained integration credentials are encrypted with AES-GCM in `/data/publishers.db`, using `/data/publishers.key`. Keep the matching key and database (and any SQLite journal/WAL/SHM files) together in stopped-copy or SQLite-aware backups; filesystem/volume access remains a trust boundary because the key lives on the same volume. The dashboard API returns only nonsecret status. Missing keys, corrupt ciphertext, unsupported storage schemas, or invalid storage paths fail closed for publisher setup. If publisher initialization was attempted but its policy metadata is unavailable, managed-key publication ingestion also fails closed rather than losing source-repository restrictions; unrelated legacy-token ingestion is unchanged. Do not delete these files to reset an integration.

Registration and installation still require GitHub consent, and organization policy may require administrator approval. Provider/API contract tests and local browser fixtures do not establish that a live GitHub runner can reach your tailnet; complete the real push test after deployment.

Browser API requests have bounded deadlines, including response-body reads; a mutation timeout is an uncertain outcome, not a rollback. Refreshing saved connection state precedes a retry.

#### Enable the GitHub Actions sender manually (alternative)

The included workflow captures metadata immediately after `docker/build-push-action` succeeds, using its digest output and the actual Docker metadata tag list. It saves the original payload as an artifact for 30 days. A separate, opt-in job downloads that payload, joins the private tailnet, and sends it. The publish job remains successful if notification delivery fails; the notification job and its failure-report job make the delivery failure visible. **Re-run failed jobs**, not all jobs, to resend without rebuilding or republishing. Missing/expired artifacts fail closed rather than manufacturing a new publication timestamp.

1. Deploy this version of Services with the persistent data mount, complete administrator setup, and create a repository-scoped `publications:write` key. Keep the workflow notification variable disabled until the receiver is deployed.
2. In Tailscale, create a dedicated CI tag and federated OIDC identity with **Auth Keys > Write** (`auth_keys`) and permission to use that tag. Restrict the identity's GitHub trust to the intended repository and publishing refs. Allow the CI tag to reach Services' existing HTTPS endpoint, not other home-server destinations. Application authorization restricts the publisher to ingestion even though it can reach the same listener. No notification-only listener or additional Docker port is required. See the official [Tailscale GitHub Action setup](https://tailscale.com/docs/integrations/github/github-action).
3. Set these **GitHub repository variables**:

   | Variable | Value |
   | --- | --- |
   | `SERVICES_PUBLISH_URL` | Full private HTTPS endpoint, e.g. `https://services.tail1a7c8f.ts.net/api/images/published`. No query, fragment, credentials, or alternate path. |
   | `TAILSCALE_CLIENT_ID` | Federated OIDC identity client ID. |
   | `TAILSCALE_AUDIENCE` | Federated identity audience. |
   | `TAILSCALE_TAGS` | Authorized comma-separated CI tag(s), e.g. `tag:services-publisher`. |
   | `SERVICES_PUBLISH_NOTIFICATIONS` | Set to the literal `true` only after the receiver and tailnet permissions are ready. |

4. Set the **GitHub Actions secret** `SERVICES_PUBLISH_TOKEN` to the newly created publisher API key. Do not put the secret in repository variables.
5. After the next successful publish, confirm the notification job reports an acknowledged result. A matching running application's update status appears on the next dashboard services poll (normally within 30 seconds); open **Review update** to see publication version/time, commit, and workflow links. Installation still needs confirmation.

**Network and application trust are separate:** Tailscale restricts network reachability; Services independently authenticates and authorizes API requests. A publisher API key cannot read monitoring data or perform container/admin operations, even with a forged browser Origin header. Retain Tailscale ACLs and HTTPS as defense in depth. Add the request hostname to `SERVICES_ALLOWED_HOSTS` if it is not already allowed through the configured public origin.

The sender is dependency-free Node 24: `node scripts/publish-notification.mjs capture` creates the artifact once; `node scripts/publish-notification.mjs send` sends the file at `SERVICES_PUBLISH_PAYLOAD_PATH`. Capture uses `PUBLISHED_DIGEST`, newline-separated fully qualified `PUBLISHED_TAGS`, the positive integer `APP_VERSION`, and GitHub's repository/SHA/ref/run/attempt environment. The image name may differ from the source repository name. The send command requires `SERVICES_PUBLISH_URL` and `SERVICES_PUBLISH_TOKEN`. It serializes once and makes at most four attempts, with a ten-second timeout per request and exponential delays. It retries transient network failures and HTTP 408/429/5xx, does not follow redirects, and never logs credentials or untrusted response bodies.

To enable another application, create a separate publisher key scoped to its exact image repository and add the same capture/artifact/notification lifecycle to that application's publish workflow. Supply that workflow's actual tags, root digest, and version; do not infer them from a current GHCR lookup or assume the image name equals the GitHub repository. Only the Services repository's workflow is wired here; other repositories are not modified.

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

### Install as an app

Services includes a web app manifest, standalone launch mode, desktop/Android icons, a maskable icon, and an Apple touch icon. After deploying the new build, open Services over **HTTPS** (for example through your existing Tailscale HTTPS origin), then use your browser's **Install app** action. On iPhone/iPad, use Safari's **Share → Add to Home Screen**. Local development on `localhost` or `127.0.0.1` is also supported; plain HTTP on a remote LAN IP is not a secure context for the service worker.

The production service worker stores **only a public offline screen**. It does not cache the dashboard, account/session responses, service status, uploaded icons, or API requests. When a navigation cannot reach the server after an initial online visit, Services shows a disconnected screen with **Try again** instead of presenting old status as current. Management actions are never queued for offline replay. Existing server-owned operations are unaffected by closing or disconnecting the app.

Updates do not force-reload an open session. The worker checks bypass HTTP caches, and a new worker waits for existing controlled windows to close before taking over. Development (`npm run dev`) does not register a worker; test offline behavior against a production build on a separate origin/port from development.

Open the gear beside the signed-in user, then **Settings → Updates → Check for updates**, to compare this page's frontend build with the web build currently served by Services. If a newer/different build is available, **Update and reload** asks you to finish or save unsaved changes; **Later** or Escape keeps your current page and pending update. Confirmation rechecks the target before reloading. Failed/offline checks never claim you are up to date, and a target that changed during confirmation requires a fresh review. A success notice is shown only after the new document's build matches the confirmed target. This refreshes the single-page web app only: it does not query a container registry, pull images, restart Services, or queue Docker operations. A new frontend build must already have been deployed on the server to be available. Checks require production build metadata; development shows an explicit unavailable state instead of an invented version.

Configure these environment variables (for example in a local `.env` file):

| Variable | Purpose |
| --- | --- |
| `SERVICES_COMPOSE_HOST_ROOT` | Root as written in container Compose labels; defaults to `E:/Containers`. |
| `SERVICES_COMPOSE_MOUNT_ROOT` | Matching path visible to both Services and the Docker daemon; defaults to `/run/desktop/mnt/host/e/Containers`. |
| `SERVICES_ALLOWED_HOSTS` | Additional exact hostnames allowed to perform actions, comma separated. Add your LAN hostname/IP if needed. |
| `SERVICES_PUBLIC_ORIGIN` | Exact browser origin when using a TLS-terminating proxy, e.g. `https://services.example.net` (no trailing slash). |
| `SERVICES_WEBHOOK_URL` | Optional generic JSON notification receiver. |
| `SERVICES_PUBLISH_TOKEN` | Optional legacy notification-only bearer secret, 32–512 printable non-space ASCII characters. Managed API keys do not require this setting. |
| `SERVICES_PUBLISH_REPOSITORIES` | Allowlist for the legacy notification token only: exact untagged GHCR repositories, comma separated. No wildcards. |
| `SERVICES_PROJECT_NAME` | Services' own Compose project name; defaults to `services`. |
| `SERVICES_DATA_DIR` | Must be `/data` backed by a writable named volume or local host-folder bind. Named volumes use the image directory's initial ownership; a host folder needs compatible permissions of its own. |
| `SERVICES_SERVICE_NAME` | Optional Services Compose service identity; defaults to `services`. Configure custom deployments explicitly. |
| `SERVICES_CONTAINER_NAME` | Optional container identity used for inspection; otherwise uses Docker's `HOSTNAME`. Needed if a custom hostname prevents self-discovery. |
| `DOCKER_SOCKET` | Unix socket path inside the Services container; defaults to `/var/run/docker.sock`. |
| `LISTEN_ADDR` | HTTP listen address; defaults to `:8080`. |

For Linux, override the two roots with your real application root and adjust the Compose bind mount accordingly, e.g. both `/srv/containers`. Docker Desktop paths must be verified on the target installation. Top-level working directories and Compose files outside the configured root are rejected. On Docker Desktop, labels may use either the Windows host root or its configured Unix mount root; both resolve to the same mounted project. This supports projects whose services were created from both Windows and inside Services without broadening the allowed filesystem boundary.

### Security boundary

The dashboard requires an administrator session, and API keys have explicit route/repository permissions. Keep Services behind Tailnet ACLs or a trusted private proxy; authentication is not a recommendation to expose Docker management publicly. Browser mutations additionally require a matching Origin and an allowed request hostname. Loopback and Tailnet IPs are allowed by default. These CSRF/Host checks supplement, rather than replace, authentication. Existing network-only deployments will require administrator setup after upgrading; there is no anonymous-management compatibility mode in production.

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

The PWA unit tests run with `npm test`; `npm run build` generates `web/dist/sw.js` from the public offline page. To exercise the actual production artifact in a browser, run `node tests/pwa.browser.mjs` from `web` after building, with Playwright available. `PLAYWRIGHT_MODULE` can point to an external Playwright module URL, `PLAYWRIGHT_CHANNEL=msedge` can select installed Edge instead of Playwright's Chromium, and `PWA_SCREENSHOT_DIR` optionally retains desktop/390px screenshots. This uses isolated browser profiles and a signed-out API fixture, not your live Docker host, and checks manifest/installability, worker activation, offline navigation, uncached API failures, and reconnect/retry.

The same build generates a content-derived frontend identity in `web/dist/web-build.json` and the matching `services-build` HTML meta tag. `node tests/webapp-updates.browser.mjs` verifies the real production bundle and worker against isolated generated build copies, including an actual page reload/new stylesheet, no automatic reload, account-draft preservation on Later/Escape, offline/check/reload failures, target changes, and zero backend mutations. Use `BROWSER_CHANNEL=msedge` for installed Edge and `WEBAPP_SCREENSHOT_DIR` to retain desktop/390px/320px screenshots. No preview-only versions or simulated success states ship in the Settings page.

Tests use local Docker/registry HTTP fixtures and fake Compose runners; they do not update your real applications. Storage tests reopen real temporary databases. Worker and recovery tests must cover duplicate submission, dashboard restart, missing/stopped workers, and uncertain results as well as normal completion. Mount tests cover both named volumes and exact daemon-reported host paths, read-only/unsupported/ambiguous storage rejection, and bind-capability checks before self-update. Real socket permissions, named-volume or host-folder ownership, cross-process SQLite locking, mounted project paths, registry credentials/availability, and actual Compose recreation must also be verified on the target host.

## Versioning and publishing

CI tests the frontend and Go packages and builds the container. Default-branch and `v*` tag pushes publish `ghcr.io/everyonesoftware/services`. The numeric build version is the Git commit count; `APP_VERSION` defaults to `0` for local builds. The Compose image tag defaults to `latest`.
