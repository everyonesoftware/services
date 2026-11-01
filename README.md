# Services

Services is a Compose-first dashboard for monitoring the current health of Compose projects on one Docker Engine.

## Current implementation

- Groups containers by Docker Compose project/service labels.
- Aggregates project health from each container's running state and configured Docker health check.
- Refreshes the dashboard every 30 seconds.
- Reads container image repository digests from Docker Engine and checks public Docker Hub and GHCR tags.
- Checks each supported running image at startup and every six hours, comparing the configured tag's registry manifest digest with the running image's recorded digest; failed registry checks retry after 15 minutes.
- Shows each container's name, image, truncated current tag, and running/health/update status; the full tag remains available on hover.
- Clicking an update pill opens a side-by-side current/proposed digest comparison with **Cancel**, **Pull**, and **Update** actions.
- Checks the exact configured tag, including `latest`; digest-pinned, private, unsupported-registry, and images without a matching local repository digest cannot be reported as updated.
- Compose projects with no configured health checks are shown as **Running**; projects with configured checks that pass are shown as **Healthy**.
- Unlabelled, non-Compose containers are not shown.

Registry detection is read-only. A changed manifest digest means the configured tag now points to a different image; tags are not semantically ordered, so this does not claim a version-number comparison. **Cancel** only dismisses the dialog. **Pull** downloads the selected service's image and leaves its running container unchanged. **Update** pulls, then runs `docker compose up -d --no-deps --pull never --no-build --wait --wait-timeout 300 <service>`; the dialog closes only after Compose reports the service running/healthy. Errors leave the dialog open. Updating recreates every replica of that Compose service and applies the current Compose files and `.env` values for that service; it does not rebuild images or start dependencies.

## CI and GHCR publishing

`.github/workflows/verify-and-publish.yml` runs the frontend build, Go tests, and Docker image build for pull requests and pushes. Pushes to the default branch or a `v*` tag publish `ghcr.io/everyonesoftware/services` with the numeric version, `latest` (default branch), branch/tag, and commit-SHA tags. Pull requests run verification only. Publishing uses the workflow's `GITHUB_TOKEN` with package-write permission; no personal token is needed in repository secrets.

## Version number

The displayed version is the number of Git commits reachable from the build's `HEAD`. CI checks out full history, calculates that integer, injects it into the app, and uses it as an additional GHCR tag. Compose defaults the image tag to `latest`; its build argument still defaults to integer `0` unless `APP_VERSION` is supplied. For a local Docker Desktop build in PowerShell, calculate the commit count so the dashboard version and image tag match:

```powershell
$env:APP_VERSION = (git rev-list --count HEAD).Trim()
docker compose up --build -d
```

## Run with Docker Desktop

From this project directory in PowerShell (sets the image tag and dashboard version to the current commit count):

```powershell
$env:APP_VERSION = (git rev-list --count HEAD).Trim()
docker compose up --build -d
```

Open `http://localhost:8080` or the server's Tailnet address on port 8080. No separate login is included; use Tailnet ACLs and avoid publishing this port to the public internet.

Pull and Update actions also restrict the request host: loopback and Tailnet IP addresses are allowed by default. If you access the dashboard through a MagicDNS or custom hostname, set `SERVICES_ALLOWED_HOSTS` to a comma-separated list of exact hostnames (without scheme, path, or port); do not use wildcards. For example: `SERVICES_ALLOWED_HOSTS=services.your-tailnet.ts.net`.

If HTTPS terminates at Tailscale Serve or another trusted reverse proxy before reaching Services over HTTP, set `SERVICES_PUBLIC_ORIGIN` to the exact browser origin (scheme and host, no trailing slash), such as `https://services.your-tailnet.ts.net`. This makes the action check compare the browser's `Origin` against that configured value; it does not trust forwarded scheme headers. The configured hostname is automatically added to the allowed-host list. After changing the setting, recreate the container with `docker compose up -d --no-build`.

The Compose file mounts `/var/run/docker.sock` so Services can query Docker Desktop's Linux Engine. `DOCKER_SOCKET`, if overridden, must be the Unix socket path inside the Services container; it is separate from the daemon-visible path used for Compose bind mounts. **The socket is highly privileged**: a read-only bind mount does not make Docker API access read-only. Status checks use read-only requests, while explicit **Pull** and **Update** actions use the socket to pull an image or recreate the selected Compose service. Anyone who can compromise Services may be able to control the Docker host through the socket. Keep the dashboard Tailnet-restricted and do not expose it publicly. Docker Desktop socket availability and mount behavior must be verified on the target installation.

The Compose file mounts `E:/Containers` read-only at Docker Desktop's daemon-visible `/run/desktop/mnt/host/e/Containers` path. This lets the in-container Compose CLI resolve relative bind mounts using a path the Docker Engine can see. If your projects live elsewhere, set both `SERVICES_COMPOSE_HOST_ROOT` (the host path from container labels) and `SERVICES_COMPOSE_MOUNT_ROOT` (the corresponding Docker Desktop daemon-visible path) before `docker compose up`. The updater rejects labeled working directories and top-level Compose files outside that root. Compose may also follow project references such as `include`, `extends.file`, `env_file`, and file-backed `configs` or `secrets`; those references are not separately sandboxed, and configured provider commands may run inside the Services container. This is a trust boundary, not a sandbox: Compose configuration or provider commands can read files available to Services and use the privileged Docker socket. Keep references within the mounted root, only run actions for trusted projects, and ensure untrusted actors cannot modify project files during an action. Services never writes to the mounted project files; the read-only bind does not freeze host-side changes.

Compose must read each selected project's configuration and `.env` files to resolve the service. Those files are therefore readable inside the Services container during an explicit **Pull** or **Update** action, although the bind mount is read-only. The Compose subprocess uses a fixed `PATH` and `/tmp` home directory instead of inheriting Services' environment or Docker credentials. The Docker socket remains highly privileged and permits engine changes despite its read-only bind mount; keep Services restricted to trusted Tailnet clients.

## Development

```sh
cd web && npm ci && npm run build
cd ..
docker run --rm -v "$PWD:/src" -w /src golang:1.24 go test ./...
```

The Go build serves the generated frontend from `web/dist` and listens on port 8080.
