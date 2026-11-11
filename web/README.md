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
- `GET /api/operations` every 2 seconds: shared Compose queue, execution status, and retained command output.
- `GET /api/projects` every 30 seconds: grouped containers, health diagnostics, available image updates.
- `POST /api/updates/check`: checks images, then refreshes groups. A 204 response is valid.
- `POST /api/projects/{name}/update`: one confirmed group operation, with streamed NDJSON progress. Success requires an explicit `complete` event; errors, malformed events, or premature EOF are not success.

Failed reads preserve the last snapshot and visibly mark it stale or disconnected. Unconfigured health checks are unverified, not healthy. Group reviews deduplicate services while showing all affected replica digests. Standalone containers and services without Compose labels cannot be updated from this UI; the backend additionally validates current Compose configuration.

`npm test` runs behavior/API tests, React server-rendered markup checks, and the existing utility tests. Test fixtures only live in test files. Browser interaction and narrow-screen checks should accompany changes to the dashboard or native update dialog.

**Activity** is a separate navigation destination combining operations and monitor events. Its compact toolbar keeps search visible and offers icon dropdowns for application, activity type, and four sort orders; it has no state filter or visible result-count/selection summary. Queue recovery notices remain outside filters. Operation log disclosure, compact/raw output, and follow-output controls reuse the existing operation renderer. The Monitor and Activity views share the dashboard polling owner so accepted updates and inventory reconciliation remain intact while switching between them.

Run `node tests/activity.browser.mjs` with a separately installed Playwright module (`PLAYWRIGHT_MODULE` may be a `file://` module URL; `BROWSER_CHANNEL` defaults to `msedge`). This fixture exercises the real app entry with isolated API responses, including navigation/reload, sorting, combined search/type/application filters, keyboard dropdowns, stale/disconnected reads, retained log controls, and interruption acknowledgement cancel/failure/success. Optional `ACTIVITY_SCREENSHOT_DIR` writes desktop and phone evidence. No real Docker update or publisher mutation is performed.
