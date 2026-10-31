# Services

Services is a Compose-first dashboard for monitoring the current health of Compose projects on one Docker Engine.

## Current implementation

- Groups containers by Docker Compose project/service labels.
- Aggregates project health from each container's running state and configured Docker health check.
- Refreshes the dashboard every 30 seconds.
- Uses Docker Engine API discovery only; it does not change containers or Compose files.
- Compose projects with no configured health checks are shown as **running-unverified**.
- Unlabelled, non-Compose containers are not shown.

Update detection (Docker Hub/GHCR), settings, notifications, and update approval are not implemented yet.

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

The Compose file mounts `/var/run/docker.sock` so Services can query Docker Desktop's Linux Engine. **The socket is highly privileged**: a read-only bind mount does not make Docker API access read-only. The current app only sends GET requests, but anyone who can compromise it may be able to control the Docker host through the socket. Keep the dashboard Tailnet-restricted and do not expose it publicly. Docker Desktop socket availability and mount behavior must be verified on the target installation.

Your existing Compose project folders are not mounted or modified by this app. The project manager/update features will need a separate, explicit design before any Compose files are accessed.

## Development

```sh
cd web && npm ci && npm run build
cd ..
docker run --rm -v "$PWD:/src" -w /src golang:1.24 go test ./...
```

The Go build serves the generated frontend from `web/dist` and listens on port 8080.
