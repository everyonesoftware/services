// Isolated browser-only fixtures exercise the production AuthGate and React pages.
// No Docker, registry, publisher or real account mutations are performed.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'
import { createServer } from '../node_modules/vite/dist/node/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || 'msedge', headless: true })
const screenshots = process.env.ACTIVITY_SCREENSHOT_DIR
if (screenshots) await mkdir(screenshots, { recursive: true })
const failures = []
const wait = async (predicate, message = 'Timed out waiting for activity') => {
  for (let i = 0; i < 150; i++) { if (await predicate()) return; await new Promise(resolve => setTimeout(resolve, 30)) }
  assert.fail(message)
}
async function exercise(viewport) {
  const context = await browser.newContext({ viewport, locale: 'en-US', timezoneId: 'America/Los_Angeles' })
  // Accelerate only background polling; no application or data transformation is replaced.
  await context.addInitScript(() => {
    const interval = window.setInterval.bind(window)
    window.setInterval = (callback, delay, ...args) => interval(callback, delay >= 2000 && delay <= 30000 ? 200 : delay, ...args)
  })
  const page = await context.newPage()
  page.setDefaultTimeout(4000)
  const errors = [], mutations = []
  let operationsError = false, monitorError = false, monitorConnected = true, acknowledgeError = false, acknowledgements = 0
  const now = Date.now(), at = seconds => new Date(now + seconds * 1000).toISOString()
  const running = { id: 'running', project: 'jellyfin', action: 'update project', state: 'running', phase: 'pulling', queuedAt: at(-181), startedAt: at(-180), messages: ['a24c9d02 Downloading 64 MB / 100 MB', 'warning: fixture output'] }
  let operations = [running,
    { id: 'queued', project: 'sonarr', action: 'update project', state: 'queued', queuePosition: 1, queuedAt: at(-10), messages: [] },
    { id: 'complete', project: 'jellyfin', action: 'update project', state: 'complete', queuedAt: at(-601), startedAt: at(-600), finishedAt: at(-540), messages: ['Container healthy'] },
    { id: 'failed', project: 'radarr', action: 'update project', state: 'error', queuedAt: at(-422), startedAt: at(-420), finishedAt: at(-400), messages: ['Image pull failed'], error: 'Fixture pull failed' },
    { id: 'literal-all', project: 'all', action: 'update project', state: 'complete', queuedAt: at(-1000), startedAt: at(-999), finishedAt: at(-990), messages: [] },
  ]
  const events = [
    { id: 'event', project: 'jellyfin', name: 'jellyfin-web-1', kind: 'healthy', message: 'Recovered after check <script>', time: at(-120) },
    { id: 'engine', project: '', name: '', kind: 'connected', message: 'Engine connection restored', time: at(-300) },
  ]
  page.on('pageerror', error => errors.push(error.message))
  await context.route('**/api/**', async route => {
    const request = route.request(), path = new URL(request.url()).pathname
    if (request.method() !== 'GET') mutations.push(`${request.method()} ${path}`)
    if (path === '/api/auth/status') return route.fulfill({ json: { setupRequired: false } })
    if (path === '/api/auth/me') return route.fulfill({ json: { user: { id: 'fixture-admin', username: 'Browser fixture', role: 'admin' } } })
    if (path === '/api/operations') return operationsError ? route.fulfill({ status: 503, body: 'Fixture worker unavailable' }) : route.fulfill({ json: { operations } })
    if (path === '/api/monitor') return monitorError ? route.fulfill({ status: 503, body: 'Fixture monitor unavailable' }) : route.fulfill({ json: { connected: monitorConnected, lastChecked: new Date().toISOString(), error: null, incidents: [], events, notificationConfigured: false } })
    if (path === '/api/projects') return route.fulfill({ json: { projects: [{ name: 'jellyfin', status: 'healthy', containers: [{ id: 'web', name: 'jellyfin-web-1', project: 'jellyfin', service: 'web', image: 'jellyfin/jellyfin:latest', state: 'running', health: 'healthy', updateAvailable: false }] }] } })
    if (path === '/api/icons') return route.fulfill({ json: { available: false, icons: [] } })
    if (path === '/api/operations/interrupted/acknowledge') {
      acknowledgements++
      if (acknowledgeError) return route.fulfill({ status: 409, body: 'Fixture worker still running' })
      operations = operations.map(operation => operation.id === 'interrupted' ? { ...operation, needsAttention: false } : operation)
      return route.fulfill({ json: { acknowledged: true } })
    }
    if (path === '/api/auth/users') return route.fulfill({ json: { users: [] } })
    if (path === '/api/auth/keys') return route.fulfill({ json: { keys: [] } })
    if (path === '/api/publishers') return route.fulfill({ json: { available: false, connections: [] } })
    return route.fulfill({ status: 404, json: { error: `Unexpected fixture route ${path}` } })
  })
  const ids = () => page.locator('.activity-feed [data-entry-id]').evaluateAll(rows => rows.map(row => row.dataset.entryId))
  const choose = async (name, label) => {
    await page.getByRole('button', { name: new RegExp(`^${name}:`) }).click()
    await page.getByRole('menuitemradio', { name: label, exact: true }).click()
  }
  const capture = suffix => screenshots ? page.screenshot({ path: join(screenshots, `activity-${viewport.width}-${suffix}.png`) }) : Promise.resolve()
  const layout = async () => {
    const g = await page.evaluate(() => { const c = document.querySelector('.app-content'); return { w: innerWidth, h: innerHeight, doc: document.documentElement.scrollWidth, dh: document.documentElement.scrollHeight, c: c.scrollWidth, cw: c.clientWidth } })
    assert.ok(g.doc <= g.w && g.c <= g.cw && g.dh <= g.h, JSON.stringify(g))
  }
  const presentation = async () => {
    const stamps = await page.locator('.activity-feed .activity-timestamp').evaluateAll(elements => elements.map(element => {
      const time = element.querySelector('.activity-clock'), date = element.querySelector('.activity-date')
      const bounds = node => { const box = node.getBoundingClientRect(); return { x: box.x, y: box.y, right: box.right, bottom: box.bottom } }
      const instant = new Date(element.dateTime)
      return {
        time: time && bounds(time), date: date && bounds(date), row: bounds(element.closest('summary, article')),
        text: element.textContent, dateText: date?.textContent, timeText: time?.textContent,
        expectedDate: `${instant.getFullYear()}-${String(instant.getMonth() + 1).padStart(2, '0')}-${String(instant.getDate()).padStart(2, '0')}`,
        expectedTime: instant.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }),
      }
    }))
    assert.equal(stamps.length, (await ids()).length)
    for (const stamp of stamps) {
      assert.equal(stamp.dateText, stamp.expectedDate)
      assert.equal(stamp.timeText, stamp.expectedTime)
      assert.doesNotMatch(stamp.text, /Finished|Observed|Started|Queued/)
      assert.ok(stamp.time.x >= stamp.row.x && stamp.date.right <= stamp.row.right, JSON.stringify(stamp))
      if (viewport.width > 640) {
        assert.ok(stamp.date.y >= stamp.time.bottom, 'Desktop date is below time')
        assert.ok(Math.abs(stamp.date.right - stamp.time.right) < 1, 'Desktop timestamp is right-aligned')
      } else {
        assert.ok(stamp.date.x > stamp.time.right, 'Phone time is left of date')
        assert.ok(Math.abs(stamp.date.y - stamp.time.y) < 1, 'Phone date and time share one line')
      }
    }
    const statuses = await page.locator('.activity-feed .operation-row > summary > .status-badge').evaluateAll(elements => elements.map(element => {
      const style = getComputedStyle(element), box = element.getBoundingClientRect()
      return { text: element.textContent, radius: parseFloat(style.borderRadius), height: box.height, background: style.backgroundColor, border: style.borderTopColor, width: box.width, scroll: element.scrollWidth, client: element.clientWidth }
    }))
    assert.ok(statuses.length > 0)
    for (const status of statuses) {
      assert.ok(status.radius >= status.height / 2, `${status.text} stays pill-shaped`)
      assert.notEqual(status.background, 'rgba(0, 0, 0, 0)', `${status.text} keeps its pill background`)
      assert.notEqual(status.border, 'rgba(0, 0, 0, 0)', `${status.text} keeps its pill border`)
      assert.ok(status.scroll <= status.client, `${status.text} is not clipped`)
    }
    const events = await page.locator('.activity-event-kind').evaluateAll(elements => elements.map(element => {
      const style = getComputedStyle(element), dot = getComputedStyle(element.querySelector('i'))
      return { border: style.borderTopWidth, background: style.backgroundColor, radius: dot.borderRadius, width: dot.width, height: dot.height }
    }))
    assert.ok(events.length > 0)
    for (const event of events) {
      assert.equal(event.border, '0px'); assert.equal(event.background, 'rgba(0, 0, 0, 0)')
      assert.equal(event.radius, '50%'); assert.equal(event.width, event.height)
    }
    assert.equal(await page.locator('.operation-time,.activity-event-time').filter({ hasText: /Finished|Observed/ }).count(), 0)
  }
  try {
    await page.goto(`${server.resolvedUrls.local[0]}#monitor`)
    await page.getByRole('heading', { level: 1, name: 'Monitor', exact: true }).waitFor()
    assert.equal(await page.locator('.docker-activity,.activity-summary,.activity-feed').count(), 0, 'Activity is removed from Monitor')
    await page.getByRole('link', { name: 'Activity', exact: true }).click()
    await page.getByRole('heading', { level: 1, name: 'Activity', exact: true }).waitFor()
    await wait(async () => (await ids()).length === 7)
    const newest = await ids()
    assert.deepEqual(newest, ['operation:queued','event:event','operation:running','event:engine','operation:failed','operation:complete','operation:literal-all'])
    assert.equal(await page.getByRole('searchbox', { name: 'Search activity', exact: true }).isVisible(), true)
    assert.equal(await page.locator('.activity-filters select,.activity-filter-summary,[data-state-filter]').count(), 0)
    assert.equal(await page.locator('.activity-filters').evaluate(element => element.nextElementSibling.className), 'activity-feed', 'Feed directly follows toolbar without a visible count or scope row')
    const buttons = page.locator('.activity-filters [aria-haspopup=menu]')
    assert.equal(await buttons.count(), 3)
    assert.deepEqual(await buttons.evaluateAll(elements => elements.map(element => element.textContent.trim())), ['', '', ''])
    for (const size of await buttons.evaluateAll(elements => elements.map(element => ({ width: element.getBoundingClientRect().width, height: element.getBoundingClientRect().height, title: element.title })))) assert.ok(size.width >= 44 && size.height >= 44 && size.title)
    await layout(); await presentation(); await capture('default')
    for (const [label, expected] of [['Oldest first', [...newest].reverse()], ['Longest first', ['operation:running','operation:complete','operation:failed','operation:literal-all']], ['Shortest first', ['operation:literal-all','operation:failed','operation:complete','operation:running']], ['Newest first', newest]]) {
      await choose('Sort activity', label)
      assert.deepEqual(await ids(), expected)
    }
    await choose('Application', 'all')
    assert.deepEqual(await ids(), ['operation:literal-all'], 'Literal project all is distinct from all applications')
    await choose('Application', 'jellyfin')
    await choose('Activity type', 'Operations')
    assert.deepEqual(await ids(), ['operation:running','operation:complete'])
    await page.getByRole('searchbox').fill('fixture output')
    assert.deepEqual(await ids(), ['operation:running'])
    await page.getByRole('searchbox').fill('no-matching-record')
    await page.getByRole('heading', { name: 'No matching activity', exact: true }).waitFor()
    await page.getByRole('button', { name: 'Clear search', exact: true }).click()
    assert.deepEqual(await ids(), ['operation:running','operation:complete'])
    await page.getByRole('button', { name: 'Reset view', exact: true }).click()
    assert.deepEqual(await ids(), newest)
    const row = page.locator('[data-entry-id="operation:running"]')
    await row.locator('summary').click()
    await row.getByLabel('Show raw output', { exact: true }).check()
    await row.getByLabel('Follow output', { exact: true }).uncheck()
    operations = operations.map(operation => operation.id === 'running' ? { ...operation, messages: [...operation.messages, 'new fixture log line'] } : operation)
    await wait(async () => (await row.locator('.operation-terminal').textContent()).includes('new fixture log line'))
    assert.equal(await row.getByLabel('Show raw output', { exact: true }).isChecked(), true, 'Polling retains inspection state')
    assert.equal(await row.getByLabel('Follow output', { exact: true }).isChecked(), false)
    await capture('output')
    // Keyboard-operated menus preserve search focus and never change toolbar height.
    const application = page.getByRole('button', { name: /^Application:/ })
    const before = await page.locator('.activity-filters').boundingBox()
    await application.focus(); await page.keyboard.press('ArrowDown')
    await page.getByRole('menu', { name: 'Application options', exact: true }).waitFor()
    await page.keyboard.press('End')
    assert.equal(await page.getByRole('menuitemradio').last().evaluate(element => element === document.activeElement), true)
    await page.keyboard.press('Home'); await page.keyboard.press('Escape')
    assert.equal(await application.evaluate(element => element === document.activeElement), true)
    assert.equal((await page.locator('.activity-filters').boundingBox()).height, before.height)
    await application.focus(); await page.keyboard.press('ArrowDown'); await page.keyboard.press('Home'); await page.keyboard.press('ArrowDown'); await page.keyboard.press('Enter')
    assert.deepEqual(await ids(), ['operation:literal-all'], 'Keyboard selection updates the actual application filter')
    await page.getByRole('button', { name: 'Reset view', exact: true }).click()
    await application.click(); await capture('menu')
    const menuBox = await page.getByRole('menu', { name: 'Application options', exact: true }).boundingBox()
    assert.ok(menuBox.x >= 0 && menuBox.x + menuBox.width <= viewport.width + 1 && menuBox.y + menuBox.height <= viewport.height + 1)
    await page.getByRole('heading', { level: 1, name: 'Activity', exact: true }).click()
    assert.equal(await page.getByRole('menu').count(), 0, 'Outside click dismisses')
    operationsError = true
    await page.getByText(/Activity unavailable:.*Fixture worker unavailable/).waitFor()
    assert.ok((await ids()).includes('operation:running'), 'Last observed records survive failure')
    operationsError = false
    await wait(async () => !(await page.getByText(/Activity unavailable:/).count()))
    monitorError = true
    await page.getByText(/Monitor events unavailable:/).waitFor()
    assert.ok((await ids()).includes('event:event'), 'Last observed events survive failure')
    monitorError = false
    monitorConnected = false
    await page.getByText(/Monitor events unavailable: Engine disconnected/).waitFor()
    assert.ok((await ids()).includes('event:event'), 'Engine disconnection retains last observed events')
    monitorConnected = true
    operations = [...operations, { id: 'interrupted', project: 'radarr', action: 'update project', state: 'error', phase: 'interrupted', needsAttention: true, queuedAt: at(-100), messages: [], error: 'Fixture result unknown' }]
    // Queue-pause safety controls remain visible even when all feed entries are filtered away.
    await page.getByRole('searchbox').fill('no-matching-record')
    await page.getByRole('button', { name: 'Acknowledge interruption', exact: true }).waitFor()
    page.once('dialog', dialog => dialog.dismiss())
    await page.getByRole('button', { name: 'Acknowledge interruption', exact: true }).click()
    assert.equal(acknowledgements, 0)
    acknowledgeError = true
    page.once('dialog', dialog => dialog.accept())
    await page.getByRole('button', { name: 'Acknowledge interruption', exact: true }).click()
    await page.getByText(/Fixture worker still running/).waitFor()
    acknowledgeError = false
    page.once('dialog', dialog => dialog.accept())
    await page.getByRole('button', { name: 'Acknowledge interruption', exact: true }).click()
    await wait(async () => (await page.getByRole('button', { name: 'Acknowledge interruption', exact: true }).count()) === 0)
    assert.equal(acknowledgements, 2)
    await page.getByRole('button', { name: 'Reset view', exact: true }).first().click()
    await layout(); await presentation(); await capture('statuses')
    await page.reload()
    await page.getByRole('heading', { level: 1, name: 'Activity', exact: true }).waitFor()
    await wait(async () => (await ids()).length === 8)
    await page.getByRole('link', { name: 'Monitor', exact: true }).click()
    await page.getByRole('heading', { level: 1, name: 'Monitor', exact: true }).waitFor()
    assert.equal(await page.locator('.activity-feed,.docker-activity,.activity-summary').count(), 0)
    assert.equal(await page.getByRole('button', { name: 'Check for updates', exact: true }).isVisible(), true)
    assert.deepEqual(errors, [])
    assert.deepEqual(mutations, ['POST /api/operations/interrupted/acknowledge', 'POST /api/operations/interrupted/acknowledge'])
  } finally { await context.close() }
}
try {
  for (const viewport of [{ width: 1440, height: 1000 }, { width: 801, height: 900 }, { width: 640, height: 844 }, { width: 390, height: 844 }, { width: 320, height: 640 }]) {
    try { await exercise(viewport); console.log(`PASS activity ${viewport.width}x${viewport.height}`) }
    catch (error) { failures.push(`${viewport.width}: ${error.stack}`) }
  }
  assert.deepEqual(failures, [])
  console.log('PASS: real Activity navigation/feed, combined filters, four sorts, live logs, keyboard menus, failure retention, unfiltered recovery controls, reload and responsive layout. No Docker updates executed.')
} finally { await browser.close(); await server.close() }
