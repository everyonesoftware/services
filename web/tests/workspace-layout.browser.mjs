// Real React pages with isolated API fixtures; no live services or credentials.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from '../node_modules/vite/dist/node/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || 'msedge', headless: true })
const screenshots = process.env.WORKSPACE_SCREENSHOT_DIR
if (screenshots) await mkdir(screenshots, { recursive: true })
const routes = ['monitor', 'activity', 'publishers', 'settings']
const titles = ['Services', 'Activity', 'Publishers', 'Settings']
const labels = ['Monitor', 'Activity', 'Publishers', 'Settings']
const wait = async predicate => {
  for (let attempt = 0; attempt < 150; attempt++) {
    if (await predicate()) return
    await new Promise(resolve => setTimeout(resolve, 30))
  }
  assert.fail('Timed out waiting for workspace state')
}
async function exercise(viewport) {
  const context = await browser.newContext({ viewport })
  const page = await context.newPage()
  page.setDefaultTimeout(5000)
  const errors = [], writes = []
  let disconnected = false
  page.on('pageerror', error => errors.push(error.message))
  const projects = Array.from({ length: 40 }, (_, i) => ({ name: `sample-${String(i).padStart(2, '0')}`, status: i === 1 ? 'unhealthy' : 'healthy', containers: [{ id: `c-${i}`, name: `sample-${i}-web-1`, project: `sample-${String(i).padStart(2, '0')}`, service: 'web', image: 'nginx:stable', state: 'running', health: i === 1 ? 'unhealthy' : 'healthy', updateAvailable: i % 3 === 0 }] }))
  const events = projects.map((project, i) => ({ id: `e-${i}`, project: project.name, name: project.containers[0].name, kind: 'healthy', message: 'Fixture health observation', time: new Date(Date.now() - i * 10000).toISOString() }))
  const user = { id: 'fixture', username: 'Browser fixture', role: 'admin' }
  await context.route('**/api/**', async route => {
    const request = route.request(), path = new URL(request.url()).pathname
    if (request.method() !== 'GET') writes.push(`${request.method()} ${path}`)
    const fixtures = {
      '/api/auth/status': { setupRequired: false }, '/api/auth/me': { user },
      '/api/auth/users': { users: [user] }, '/api/auth/keys': { keys: [] },
      '/api/projects': { projects }, '/api/operations': { operations: [] }, '/api/icons': { available: false, icons: [] },
      '/api/monitor': { connected: !disconnected, lastChecked: new Date().toISOString(), error: disconnected ? 'Fixture engine unavailable' : null, incidents: [], events, notificationConfigured: false },
      '/api/publishers': { available: true, github: { connected: true, permissionsChecked: true, variablesWrite: true }, tailnet: { clientId: '', audience: '', tags: '' }, connections: [], endpoint: 'https://example.test/api/images/published' },
    }
    return Object.hasOwn(fixtures, path) ? route.fulfill({ json: fixtures[path] }) : route.fulfill({ status: 404 })
  })
  const go = async index => {
    await page.locator('.app-sidebar').getByRole('link', { name: labels[index], exact: true }).click()
    await page.getByRole('heading', { level: 1, name: titles[index], exact: true }).waitFor()
    await page.locator('.app-content').evaluate(element => { element.scrollTop = 0 })
  }
  const geometry = () => page.evaluate(() => {
    const header = document.querySelector('.unified-page-header'), title = header.querySelector('h1'), style = getComputedStyle(title), box = title.getBoundingClientRect(), content = document.querySelector('.app-content')
    return { x: box.x, y: box.y, fontSize: style.fontSize, fontWeight: style.fontWeight, lineHeight: style.lineHeight, letterSpacing: style.letterSpacing, color: style.color, border: getComputedStyle(header).borderBottomWidth, overflow: content.scrollWidth > content.clientWidth || document.documentElement.scrollWidth > innerWidth, outerScroll: document.documentElement.scrollHeight > innerHeight }
  })
  const capture = suffix => screenshots ? page.screenshot({ path: join(screenshots, `workspace-${viewport.width}-${viewport.height}-${suffix}.png`) }) : Promise.resolve()
  try {
    await page.goto(`${server.resolvedUrls.local[0]}#monitor`)
    await page.getByRole('heading', { level: 1, name: 'Services', exact: true }).waitFor()
    const styles = []
    for (let i = 0; i < routes.length; i++) {
      await go(i)
      const g = await geometry()
      assert.equal(g.fontSize, '24px'); assert.equal(g.fontWeight, '600'); assert.equal(g.border, '0px')
      assert.equal(g.overflow, false); assert.equal(g.outerScroll, false)
      assert.equal(await page.locator('.page-description,.page-heading .publisher-muted').count(), 0)
      styles.push(g)
      await capture(routes[i])
    }
    for (const property of ['x', 'y', 'fontSize', 'fontWeight', 'lineHeight', 'letterSpacing', 'color']) assert.equal(new Set(styles.map(s => s[property])).size, 1, `Shared header ${property}`)
    for (const index of [0, 1]) {
      await go(index)
      await page.locator(index ? '.activity-feed > li' : '.project-group').first().waitFor()
      const panel = page.locator('.sticky-panel')
      const baseline = await panel.boundingBox()
      await page.locator('.app-content').evaluate(element => { element.scrollTop = 20 })
      const moving = await panel.boundingBox()
      assert.ok(moving.y < baseline.y, 'Toolbar scrolls before reaching top')
      await page.locator('.app-content').evaluate(element => { element.scrollTop = 500 })
      const first = await page.evaluate(() => ({ panel: document.querySelector('.sticky-panel').getBoundingClientRect().top, row: document.querySelector('.project-group,.activity-feed > li').getBoundingClientRect().top, sidebar: document.querySelector('.app-sidebar').getBoundingClientRect().top }))
      await page.mouse.move(viewport.width - 50, viewport.height - 60)
      await page.mouse.wheel(0, 200)
      await wait(async () => await page.locator('.app-content').evaluate(element => element.scrollTop > 550))
      const later = await page.evaluate(() => ({ panel: document.querySelector('.sticky-panel').getBoundingClientRect().top, row: document.querySelector('.project-group,.activity-feed > li').getBoundingClientRect().top, sidebar: document.querySelector('.app-sidebar').getBoundingClientRect().top }))
      assert.ok(Math.abs(first.panel - later.panel) < 1 && Math.abs(later.panel) < 1, 'Toolbar stays at content top')
      assert.ok(later.row < first.row, 'Only results continue moving'); assert.equal(later.sidebar, first.sidebar)
      await capture(`${routes[index]}-pinned`)
      const button = page.getByRole('button', { name: index ? /^Sort activity:/ : /^Service filter:/ })
      await button.click()
      const menu = page.locator('.activity-dropdown-menu:popover-open')
      const menuBox = await menu.boundingBox()
      assert.ok(menuBox.x >= 0 && menuBox.x + menuBox.width <= viewport.width && menuBox.y >= 0 && menuBox.y + menuBox.height <= viewport.height)
      await page.keyboard.press('Escape')
      assert.equal(await button.evaluate(element => element === document.activeElement), true)
      assert.equal(await page.locator('.activity-dropdown-menu:popover-open').count(), 0)
    }
    await go(0)
    assert.equal(await page.locator('.health-banner,.monitor-details,.services-title').count(), 0)
    const filterButton = page.getByRole('button', { name: /^Service filter:/ })
    assert.equal(await filterButton.textContent(), '')
    assert.equal(await page.getByRole('button', { name: 'Reset view', exact: true }).count(), 0)
    await filterButton.click()
    await page.getByRole('menuitemradio', { name: /^Updates/ }).click()
    await wait(async () => await page.locator('.project-group').count() === 14)
    await page.getByRole('searchbox', { name: 'Search services', exact: true }).fill('sample-03')
    assert.equal(await page.locator('.project-group').count(), 1)
    await page.getByRole('button', { name: 'Reset view', exact: true }).click()
    await wait(async () => await page.locator('.project-group').count() === 40)
    await filterButton.click()
    await page.getByRole('menuitemradio', { name: /^Needs attention/ }).click()
    await wait(async () => await page.locator('.project-group').count() === 1)
    await page.getByRole('searchbox', { name: 'Search services', exact: true }).fill('not-a-service')
    await page.getByRole('heading', { name: 'No matching services', exact: true }).waitFor()
    await page.getByRole('button', { name: 'Reset view', exact: true }).click()
    disconnected = true
    await page.getByRole('button', { name: /Refresh/ }).click()
    await page.getByRole('alert').filter({ hasText: 'Fixture engine unavailable' }).waitFor()
    assert.equal(await page.getByRole('button', { name: 'Check for updates', exact: true }).isDisabled(), true)
    assert.deepEqual(writes, [])
    assert.deepEqual(errors, [])
    console.log(`PASS workspace ${viewport.width}x${viewport.height}: unified headers, sticky scrolling, menus, filtering/reset, errors preserved`)
  } finally { await context.close() }
}
try {
  for (const viewport of [{ width: 1440, height: 900 }, { width: 1000, height: 650 }, { width: 390, height: 740 }, { width: 320, height: 560 }]) await exercise(viewport)
} finally { await browser.close(); await server.close() }
