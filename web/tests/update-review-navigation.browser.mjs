// Isolated real-app fixture: delayed acceptance must not dismiss a newer review.
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from '../node_modules/vite/dist/node/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch({ ...(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : { channel: process.env.BROWSER_CHANNEL || 'msedge' }), headless: true })
const deferred = () => { let resolve; const promise = new Promise(done => { resolve = done }); return { promise, resolve } }
const project = name => ({ name, status: 'healthy', containers: [{ id: `${name}-web`, name: `${name}-web`, project: name, service: 'web', image: 'nginx:stable', state: 'running', health: 'healthy', updateAvailable: true, currentDigest: 'sha256:old', proposedDigest: 'sha256:new' }] })
const errors = []
const failures = []
try {
  for (const target of ['beta', 'alpha']) {
    const context = await browser.newContext({ viewport: { width: 1200, height: 900 }, serviceWorkers: 'block' })
    const page = await context.newPage()
    page.setDefaultTimeout(5000)
    page.on('pageerror', error => errors.push(error.message))
    const held = deferred(), submitted = deferred(), reconciled = deferred()
    const writes = []
    const reads = { projects: 0, monitor: 0, operations: 0, icons: 0 }
    let operations = [], terminalObserved = false
    const receipt = { id: `old-alpha-${target}`, project: 'alpha', action: 'update', state: target === 'alpha' ? 'complete' : 'queued', messages: [] }
    await page.route('**/api/**', async route => {
      const request = route.request(), path = new URL(request.url()).pathname
      if (request.method() !== 'GET') {
        writes.push(`${request.method()} ${path}`)
        if (path === '/api/projects/alpha/update') {
          submitted.resolve()
          await held.promise
          return route.fulfill({ json: receipt })
        }
        return route.fulfill({ status: 500, body: 'Unexpected mutation' })
      }
      if (path === '/api/auth/status') return route.fulfill({ json: { setupRequired: false } })
      if (path === '/api/auth/me') return route.fulfill({ json: { user: { id: 'fixture', username: 'Fixture', role: 'admin' } } })
      if (path === '/api/projects') {
        reads.projects++
        // This request follows observation of the terminal operation, so it can
        // reconcile inventory before opening a same-project *new-image* review.
        if (terminalObserved) reconciled.resolve()
        const alpha = project('alpha')
        if (terminalObserved) alpha.containers[0].proposedDigest = 'sha256:next-image'
        return route.fulfill({ json: { projects: [alpha, project('beta')] } })
      }
      if (path === '/api/monitor') { reads.monitor++; return route.fulfill({ json: { connected: true, lastChecked: new Date().toISOString(), incidents: [], events: [] } }) }
      if (path === '/api/operations') { reads.operations++; terminalObserved ||= operations.some(item => item.state === 'complete'); return route.fulfill({ json: { operations } }) }
      if (path === '/api/icons') { reads.icons++; return route.fulfill({ json: { available: true, icons: [] } }) }
      return route.fulfill({ status: 404 })
    })
    const review = page.getByRole('button', { name: 'Review 1 update', exact: true })
    const dialog = page.getByRole('dialog')
    try {
      await page.goto(server.resolvedUrls.local[0])
      await page.locator('.project-link[href="#service/alpha"]').click()
      await page.getByRole('heading', { name: 'alpha', exact: true }).waitFor()
      await review.click()
      assert.deepEqual(writes, [], 'Opening a review never submits')
      await dialog.getByRole('button', { name: 'Confirm & update', exact: true }).evaluate(button => { button.click(); button.click() })
      await submitted.promise
      await page.goBack()
      await page.getByRole('heading', { name: 'Services', exact: true }).waitFor()
      await dialog.waitFor({ state: 'detached' })
      if (target === 'alpha') {
        // The operation feed can finish before the held POST response arrives.
        // Reconcile first: queued/running/stale same-project scopes must still
        // invalidate reviews, not be bypassed merely to test callback identity.
        operations = [receipt]
        await reconciled.promise
      }
      await page.locator(`.project-link[href="#service/${target}"]`).click()
      await page.getByRole('heading', { name: target, exact: true }).waitFor()
      await review.click()
      await dialog.getByRole('heading', { name: `Review updates / ${target}`, exact: true }).waitFor()
      const before = { ...reads }
      held.resolve()
      // onUpdated refreshes icons immediately; observing that response and two
      // browser frames ensures the synchronous subsequent onClose has committed.
      await page.waitForResponse(response => new URL(response.url()).pathname === '/api/icons')
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
      assert.equal(new URL(page.url()).hash, `#service/${target}`)
      assert.equal(await dialog.count(), 1, `Old Alpha acceptance must not close a newly opened ${target} review`)
      assert.equal(await dialog.isVisible(), true)
      assert.deepEqual(writes, ['POST /api/projects/alpha/update'], 'No duplicate or implicit new-review submission')
      for (const key of Object.keys(reads)) assert.ok(reads[key] > before[key], `Old acceptance still refreshes ${key}`)
      if (target === 'beta') await dialog.getByRole('button', { name: 'Confirm & queue', exact: true }).waitFor()
      await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
      await dialog.waitFor({ state: 'detached' })
      await review.click()
      await page.keyboard.press('Escape')
      await dialog.waitFor({ state: 'detached' })
      assert.deepEqual(writes, ['POST /api/projects/alpha/update'])
      if (target === 'beta') {
        // The receipt is retained even when the operations poll still returns []:
        // returning to Alpha must expose Queued, not a stale actionable update.
        await page.goBack()
        await page.locator('.project-group').filter({ hasText: 'alpha' }).getByText('Queued', { exact: true }).waitFor()
      }
      console.log(`PASS old Alpha acceptance preserves new ${target} review, refresh/receipt reconciliation, explicit close and one submission`)
    } catch (error) { failures.push(`${target}: ${error.stack}`) }
    finally { held.resolve(); await context.close() }
  }
  // A currently mounted review still closes normally on acceptance.
  const page = await browser.newPage()
  let submissions = 0
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname
    if (route.request().method() !== 'GET') { submissions++; return route.fulfill({ json: { id: 'current', project: 'alpha', action: 'update', state: 'queued', messages: [] } }) }
    const fixtures = { '/api/auth/status': { setupRequired: false }, '/api/auth/me': { user: { id: 'fixture', username: 'Fixture', role: 'admin' } }, '/api/projects': { projects: [project('alpha')] }, '/api/monitor': { connected: true, lastChecked: new Date().toISOString(), incidents: [], events: [] }, '/api/operations': { operations: [] }, '/api/icons': { available: true, icons: [] } }
    return route.fulfill({ json: fixtures[path] ?? {} })
  })
  await page.goto(server.resolvedUrls.local[0])
  await page.getByRole('button', { name: 'Review 1 update', exact: true }).click()
  await page.getByRole('button', { name: 'Confirm & update', exact: true }).click()
  await page.getByRole('dialog').waitFor({ state: 'detached' })
  assert.equal(submissions, 1)
  console.log('PASS current review acceptance closes normally with one submission')
  assert.deepEqual(errors, [])
  assert.deepEqual(failures, [])
} finally { await browser.close(); await server.close() }
