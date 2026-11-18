// Real React pages with isolated browser-only APIs; no live provider writes.
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { createServer } from '../node_modules/vite/dist/node/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch({ ...(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : { channel: process.env.BROWSER_CHANNEL || 'msedge' }), headless: true })
const failures = []
const capture = async (page, name) => {
  if (!process.env.MOBILE_DETAILS_SCREENSHOT_DIR) return
  await mkdir(process.env.MOBILE_DETAILS_SCREENSHOT_DIR, { recursive: true })
  await page.screenshot({ path: join(process.env.MOBILE_DETAILS_SCREENSHOT_DIR, `${name}.png`) })
}
const sizes = [{ width: 390, height: 844 }, { width: 320, height: 640 }, { width: 390, height: 600 }, { width: 320, height: 560 }]
const font = async (locator, size) => {
  for (const item of await locator.all()) assert.equal(await item.evaluate(el => getComputedStyle(el).fontSize), `${size}px`, `${await item.textContent()} typography`)
  assert.ok(await locator.count(), 'Typography assertion matched real content')
}
const target = async locator => {
  for (const item of await locator.all()) assert.ok((await item.boundingBox()).height >= 48, `${await item.textContent()} has a 48px target`)
}
const fits = async page => {
  const geometry = await page.evaluate(() => {
    const content = document.querySelector('.app-content')
    return { docX: document.documentElement.scrollWidth - innerWidth, docY: document.documentElement.scrollHeight - innerHeight, contentX: content.scrollWidth - content.clientWidth }
  })
  for (const [key, value] of Object.entries(geometry)) assert.ok(value <= 1, `${key}: ${value}`)
}
async function fixture(viewport, hash) {
  const context = await browser.newContext({ viewport, locale: 'en-US', timezoneId: 'America/Los_Angeles' })
  const page = await context.newPage()
  page.setDefaultTimeout(5000)
  const errors = [], writes = []
  page.on('pageerror', error => errors.push(error.message))
  const repository = { id: 14, fullName: 'Demo/Publisher-with-a-long-name', defaultBranch: 'main', installationId: 8 }
  const setupRepository = { ...repository, id: 15, fullName: 'Demo/Publisher-new-with-a-long-name' }
  const connection = { id: 'fixture', repository, imageRepository: 'ghcr.io/demo/publisher-with-a-long-name', state: 'verified', createdAt: '2026-01-01T00:00:00Z', lastPublication: { version: 'v3.2.1', tags: ['latest', 'v3.2.1'], digest: `sha256:${'a'.repeat(64)}`, commit: 'abcdef1234567890', publishedAt: '2026-11-10T08:00:00Z', receivedAt: '2026-11-10T08:01:00Z', result: 'accepted' } }
  const status = { available: true, github: { connected: true, name: 'Browser fixture App', permissionsChecked: true, variablesWrite: true }, tailnet: { clientId: '', audience: '', tags: '' }, connections: [connection], endpoint: 'https://services.example.test/api/images/published' }
  const operation = { id: 'running', project: 'publisher-with-a-long-project-name', action: 'update project', state: 'running', phase: 'pulling', queuedAt: '2026-11-12T07:58:12Z', startedAt: '2026-11-12T07:59:12Z', messages: ['a24c9d02 Downloading 64 MB / 100 MB', 'Browser fixture output retained'] }
  await context.route('**/api/**', async route => {
    const req = route.request(), path = new URL(req.url()).pathname
    if (req.method() !== 'GET') writes.push(`${req.method()} ${path}`)
    if (path === '/api/auth/status') return route.fulfill({ json: { setupRequired: false } })
    if (path === '/api/auth/me') return route.fulfill({ json: { user: { id: 'fixture-admin', username: 'Browser fixture', role: 'admin' } } })
    if (path === '/api/publishers' && req.method() === 'POST') {
      const body = req.postDataJSON()
      const saved = { id: 'setup', repository: body.repository, imageRepository: body.imageRepository, state: 'awaiting_callback', createdAt: connection.createdAt }
      status.connections.push(saved)
      return route.fulfill({ json: { connection: saved } })
    }
    if (path === '/api/publishers') return route.fulfill({ json: status })
    if (path === '/api/publishers/repositories') return route.fulfill({ json: { repositories: [setupRepository] } })
    if (path === '/api/publishers/tailnet/guidance') return route.fulfill({ json: { issuer: 'https://token.actions.githubusercontent.com', subject: 'repo:Demo@42/Publisher-new-with-a-long-name@15:ref:refs/heads/main', tailnet: { clientId: '', audience: '', tags: '' } } })
    if (path === '/api/publishers/tailnet') return route.fulfill({ json: status })
    if (path === '/api/publishers/plan') return route.fulfill({ json: { supported: true, problems: [], repository: setupRepository, imageRepository: req.postDataJSON().imageRepository, approval: 'fixture-approval', secretName: 'SERVICES_PUBLISH_TOKEN', variables: { TAILSCALE_CLIENT_ID: 'fixture-client' } } })
    if (path === '/api/operations') return route.fulfill({ json: { operations: [operation] } })
    if (path === '/api/monitor') return route.fulfill({ json: { connected: true, lastChecked: new Date().toISOString(), incidents: [], events: [{ id: 'healthy', project: 'publisher-with-a-long-project-name', name: 'publisher-web-1', kind: 'healthy', message: 'Recovered after a health check; all meaningful event details remain readable.', time: '2026-11-12T08:00:19Z' }] } })
    if (path === '/api/projects') return route.fulfill({ json: { projects: [] } })
    if (path === '/api/icons') return route.fulfill({ json: { available: false, icons: [] } })
    throw new Error(`Unexpected fixture request ${req.method()} ${path}`)
  })
  await page.goto(`${server.resolvedUrls.local[0]}#${hash}`)
  return { context, page, errors, writes }
}
async function activity(viewport) {
  const { context, page, errors, writes } = await fixture(viewport, 'activity')
  try {
    await page.locator('.activity-feed .operation-identity strong').waitFor()
    await font(page.locator('.activity-feed .operation-identity strong,.activity-event-identity strong'), 18)
    await font(page.locator('.activity-event-identity p'), 16)
    await font(page.locator('.activity-feed .operation-identity > span,.activity-event-identity > span,.activity-duration small,.activity-clock,.activity-date'), 14)
    for (const row of await page.locator('.activity-feed summary,.activity-event').all()) {
      const g = await row.evaluate(el => {
        const bounds = selector => { const r = el.querySelector(selector).getBoundingClientRect(); return { x: r.x, y: r.y, right: r.right, bottom: r.bottom } }
        return { identity: bounds('.operation-identity,.activity-event-identity'), status: bounds('.status-badge,.activity-event-kind'), duration: bounds('.activity-duration'), time: bounds('.activity-clock'), date: bounds('.activity-date'), stamp: el.querySelector('time').textContent }
      })
      assert.ok(g.identity.bottom <= g.status.y + 1, 'Full-width identity precedes status')
      assert.ok(g.identity.bottom <= g.duration.y + 1, 'Duration follows identity')
      assert.ok(g.identity.x < g.status.right, 'Identity is not squeezed into a status-side column')
      assert.ok(Math.abs(g.time.y - g.date.y) < 1 && g.time.right < g.date.x, 'Time/date inline')
      assert.match(g.stamp, /\d{2}:\d{2}:\d{2} [AP]M · 2026-11-12|\d{2}:\d{2}:\d{2} [AP]M · 2026-11-11/)
    }
    assert.ok(await page.locator('.activity-event-kind i').isVisible())
    await capture(page, `activity-${viewport.width}x${viewport.height}`)
    await page.locator('.activity-feed summary').click()
    await font(page.locator('.activity-feed .operation-latest,.activity-feed .output-heading h3,.activity-feed .output-controls label'), 16)
    await font(page.locator('.activity-feed .operation-terminal,.activity-feed .operation-timestamps'), 14)
    await target(page.locator('.activity-feed .output-controls label'))
    await page.getByLabel('Show raw output', { exact: true }).check()
    await page.getByLabel('Follow output', { exact: true }).uncheck()
    assert.match(await page.locator('.operation-terminal').textContent(), /Browser fixture output retained/)
    await fits(page)
    await page.getByRole('searchbox', { name: 'Search activity' }).fill('Recovered')
    assert.equal(await page.locator('.activity-event').count(), 1)
    assert.equal(await page.locator('.activity-feed .operation-row').count(), 0)
    assert.deepEqual(writes, []); assert.deepEqual(errors, [])
  } finally { await context.close() }
}
async function publishers(viewport) {
  const { context, page, errors, writes } = await fixture(viewport, 'publishers')
  const button = name => page.getByRole('button', { name, exact: true })
  const primary = () => page.locator('.publisher-wizard .publisher-footer .primary')
  try {
    await page.locator('.publisher-row-name').waitFor()
    await font(page.locator('.publisher-row-name'), 18)
    await font(page.locator('.publisher-row-identity code,.publisher-row-state,.publisher-row-delivery'), 14)
    assert.equal(await page.locator('.publisher-row-delivery').isVisible(), true, 'Delivery metadata is not hidden for fit')
    await capture(page, `publishers-overview-${viewport.width}x${viewport.height}`)
    await button('Refresh').evaluate(el => el.scrollIntoView())
    await target(page.locator('.publishers-workspace .page-heading .button,.publisher-row-summary'))
    await page.locator('.publisher-row-summary').click()
    await font(page.locator('.publisher-detail-tabs button'), 16)
    await target(page.locator('.publisher-detail-tabs button'))
    await page.getByText('Technical metadata', { exact: true }).click()
    await font(page.locator('.publisher-facts dt'), 14)
    await font(page.locator('.publisher-facts dd'), 16)
    await fits(page)
    await page.getByRole('tab', { name: 'Connection', exact: true }).click()
    await font(page.locator('.publisher-muted'), 14)
    await target(page.locator('.publisher-connection-actions .button'))
    await button('Disconnect').click()
    const dialog = page.getByRole('dialog', { name: 'Disconnect Demo/Publisher-with-a-long-name?', exact: true })
    await dialog.waitFor()
    await font(dialog.locator('h2'), 18)
    await font(dialog.locator('#publisher-confirmation-description'), 16)
    await target(dialog.locator('.button'))
    assert.ok(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth))
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
    assert.deepEqual(writes, [], 'Named confirmation cancel does not mutate')
    await button('Connect repository').click()
    const stationary = async step => {
      assert.equal(await page.locator('.publisher-steps li[aria-current="step"]').count(), 1)
      assert.equal(await page.locator('.publisher-steps li:not(.publisher-aside)').count(), 5)
      await font(page.locator('.publisher-step-content h2,.publisher-step-content h3'), 18)
      await font(page.locator('.publisher-steps li:not(.publisher-aside),.publisher-footer > p'), 14)
      await target(page.locator('.publisher-wizard .publisher-footer button'))
      const content = page.locator('.publisher-step-content')
      const get = () => page.evaluate(() => {
        const box = s => { const r = document.querySelector(s).getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height, bottom: r.bottom } }
        const outer = document.querySelector('.app-content'), content = document.querySelector('.publisher-step-content')
        return { steps: box('.publisher-steps'), footer: box('.publisher-wizard .publisher-footer'), buttons: [...document.querySelectorAll('.publisher-wizard .publisher-footer button')].map(el => ({ text: el.textContent, width: el.clientWidth, height: el.clientHeight })), outer: outer.scrollHeight - outer.clientHeight, horizontal: content.scrollWidth - content.clientWidth, height: content.clientHeight }
      })
      const before = await get()
      assert.ok(before.height > 80, `step ${step} retains usable short-phone scroll region: ${JSON.stringify(before)}`)
      assert.ok(before.footer.bottom <= viewport.height - 68 + 1, `step ${step} footer remains above navigation`)
      assert.ok(before.outer <= 1 && before.horizontal <= 1, `step ${step} scroll owners: ${JSON.stringify(before)}`)
      await content.evaluate(el => { const filler = document.createElement('div'); filler.id = 'mobile-scroll-fixture'; filler.style.height = '1600px'; el.append(filler); el.scrollTop = 1000 })
      const after = await get()
      assert.deepEqual(after.steps, before.steps, `step ${step} stationary navigation`)
      assert.deepEqual(after.footer, before.footer, `step ${step} stationary footer`)
      assert.ok(await content.evaluate(el => el.scrollTop > 0))
      await content.evaluate(el => { el.querySelector('#mobile-scroll-fixture').remove(); el.scrollTop = 0 })
      await fits(page)
      await capture(page, `publisher-step-${step}-${viewport.width}x${viewport.height}`)
    }
    await stationary(0)
    await primary().click()
    await page.getByRole('heading', { name: 'Select a publishing repository' }).waitFor()
    await page.getByRole('radio', { name: /Demo\/Publisher/ }).check()
    await stationary(1)
    await primary().click()
    await page.getByRole('heading', { name: /Set up Tailscale for/ }).waitFor()
    await page.getByRole('button', { name: 'Copy subject', exact: true }).waitFor()
    await font(page.locator('.publisher-copy-block code'), 14)
    await target(page.locator('.publisher-copy-value'))
    await page.getByLabel('Client ID', { exact: true }).fill('fixture-client')
    await page.getByLabel('Audience', { exact: true }).fill('api.tailscale.com/fixture-client')
    await page.getByLabel('Runner tags', { exact: true }).fill('tag:services-publisher')
    await stationary(2)
    await primary().click()
    await page.getByRole('heading', { name: 'Review publisher setup' }).waitFor()
    await stationary(3)
    await primary().click()
    await page.locator('.publisher-steps li[aria-current="step"]').filter({ hasText: 'Test connection' }).waitFor()
    await stationary(4)
    await button('Back to publishers').click()
    await page.locator('.publisher-row-summary').first().waitFor()
    assert.deepEqual(errors, [])
    assert.deepEqual(writes, ['POST /api/publishers/tailnet/guidance', 'PUT /api/publishers/tailnet', 'POST /api/publishers/plan', 'POST /api/publishers'])
  } finally { await context.close() }
}
try {
  if (!process.env.MOBILE_DETAILS_CASE || process.env.MOBILE_DETAILS_CASE === 'publishers') for (const viewport of sizes) {
    try { await publishers(viewport); console.log(`PASS mobile Publishers ${viewport.width}x${viewport.height}`) }
    catch (error) { failures.push(`Publishers ${viewport.width}x${viewport.height}: ${error.stack}`) }
  }
  if (!process.env.MOBILE_DETAILS_CASE || process.env.MOBILE_DETAILS_CASE === 'activity') for (const viewport of sizes) {
    try { await activity(viewport); console.log(`PASS mobile Activity ${viewport.width}x${viewport.height}`) }
    catch (error) { failures.push(`Activity ${viewport.width}x${viewport.height}: ${error.stack}`) }
  }
  assert.deepEqual(failures, [])
} finally { await browser.close(); await server.close() }
