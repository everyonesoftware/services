# Services monitor frontend

React + TypeScript + Vite. The production dashboard reads live Docker snapshots from the Services backend; it contains no demo data.

## Development

Use Node 24 or newer (the Node test runner loads the TypeScript behavior helpers directly).

```sh
npm ci
npm run dev
npm test
npm run build
npm run lint
```

Vite proxies `/api` to the Services backend at `http://localhost:8080`. To use another backend, set `SERVICES_API_URL` when starting Vite. Production uses same-origin `/api` requests; serve `dist/` with the Services backend.

## Data and actions

- `GET /api/monitor` every 15 seconds: engine connectivity, incidents, recent events, notification configuration.
- `GET /api/projects` every 30 seconds: grouped containers, health diagnostics, available image updates.
- `POST /api/updates/check`: checks images, then refreshes groups. A 204 response is valid.
- `POST /api/projects/{name}/update`: one confirmed group operation, with streamed NDJSON progress. Success requires an explicit `complete` event; errors, malformed events, or premature EOF are not success.

Failed reads preserve the last snapshot and visibly mark it stale or disconnected. Unconfigured health checks are unverified, not healthy. Group reviews deduplicate services while showing all affected replica digests. Standalone containers and services without Compose labels cannot be updated from this UI; the backend additionally validates current Compose configuration.

`npm test` runs behavior/API tests, React server-rendered markup checks, and the existing utility tests. Test fixtures only live in test files. Browser interaction and narrow-screen checks should accompany changes to the dashboard or native update dialog.
