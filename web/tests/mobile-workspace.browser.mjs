// Real pages and shared shell; isolated API fixtures only, no live backend writes.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from '../node_modules/vite/dist/node/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || 'msedge', headless: true })
const screenshots = process.env.MOBILE_SCREENSHOT_DIR
if (screenshots) await mkdir(screenshots, { recursive: true })
const pages = ['monitor', 'activity', 'publishers', 'settings']
const names = ['Monitor', 'Activity', 'Publishers', 'Settings']
const titles = ['Services', 'Activity', 'Publishers', 'Settings']
async function exercise(viewport) {
  const context = await browser.newContext({ viewport, serviceWorkers: 'block' })
  const page = await context.newPage()
  const errors = [], writes = []
  page.on('pageerror', error => errors.push(error.message))
  let signedIn = true
  const user = { id: 'mobile-admin', username: 'Layout fixture', role: 'admin' }
  const projects = Array.from({ length: 25 }, (_, i) => ({ name: `sample-${i}`, status: 'healthy', containers: [{ id: `c-${i}`, name: `sample-${i}-web-1`, service: 'web', project: `sample-${i}`, image: 'nginx:stable', state: 'running', health: 'healthy', updateAvailable: i === 0 }] }))
  await context.route('**/api/**', async route => {
    const req = route.request(), path = new URL(req.url()).pathname
    if (req.method() !== 'GET') writes.push(`${req.method()} ${path}`)
    if (path === '/api/auth/logout') { signedIn = false; return route.fulfill({ status: 204 }) }
    if (path === '/api/auth/me' && !signedIn) return route.fulfill({ status: 401, json: { error: 'Signed out' } })
    const fixtures = {
      '/api/auth/status': { setupRequired: false }, '/api/auth/me': { user },
      '/api/auth/users': { users: [user] }, '/api/auth/keys': { keys: [] },
      '/api/projects': { projects }, '/api/icons': { available: false, icons: [] }, '/api/operations': { operations: [] },
      '/api/monitor': { connected: true, lastChecked: new Date().toISOString(), incidents: [], events: projects.map((p, i) => ({ id: `e-${i}`, kind: 'healthy', project: p.name, name: 'web', message: 'Sample health observation', time: new Date().toISOString() })), notificationConfigured: false },
      '/api/publishers': { available: true, github: { connected: true, permissionsChecked: true, variablesWrite: true }, tailnet: { clientId: '', audience: '', tags: '' }, connections: [], endpoint: 'https://example.test/api/images/published' },
    }
    return Object.hasOwn(fixtures, path) ? route.fulfill({ json: fixtures[path] }) : route.fulfill({ status: 404 })
  })
  const mobile = viewport.width <= 640
  const nav = page.locator(mobile ? '.mobile-navigation' : '.app-sidebar')
  const measure = () => page.evaluate(() => {
    const content = document.querySelector('.app-content'), n = document.querySelector('.mobile-navigation')
    return { outerOverflow: document.documentElement.scrollWidth > document.documentElement.clientWidth, outerScroll: document.documentElement.scrollHeight > innerHeight, contentOverflow: content.scrollWidth > content.clientWidth, contentLeft: content.getBoundingClientRect().left, navTop: n?.getBoundingClientRect().top, contentBottom: content.getBoundingClientRect().bottom, navHeight: n?.getBoundingClientRect().height }
  })
  try {
    await page.goto(`${server.resolvedUrls.local[0]}#monitor`)
    await page.getByRole('heading', { name: 'Services', exact: true }).waitFor()
    if (mobile) {
      assert.equal(await page.locator('.mobile-navigation').isVisible(), true, 'Shared labeled bottom navigation replaces mobile sidebar')
      assert.equal(await page.locator('.app-sidebar').isVisible(), false)
      assert.deepEqual(await nav.getByRole('link').allTextContents().then(a => a.map(s => s.trim())), names)
      const targets = await nav.getByRole('link').evaluateAll(a => a.map(e => ({ height: e.getBoundingClientRect().height, font: parseFloat(getComputedStyle(e).fontSize) })))
      assert.ok(targets.every(a => a.height >= 48 && a.font >= 12))
    } else assert.equal(await page.locator('.mobile-navigation').isVisible(), false)
    await page.evaluate(() => { window.__mobileLayoutMarker = 'mounted' })
    const headerBoxes = []
    for (let i = 0; i < pages.length; i++) {
      await nav.getByRole('link', { name: names[i], exact: true }).click()
      await page.getByRole('heading', { name: titles[i], level: 1, exact: true }).waitFor()
      assert.equal(await page.evaluate(() => window.__mobileLayoutMarker), 'mounted', 'Navigation remains client-side')
      assert.equal(await nav.locator('[aria-current="page"]').getAttribute('href'), `#${pages[i]}`)
      const geometry = await measure()
      assert.equal(geometry.outerOverflow, false, `${pages[i]} outer overflow`)
      assert.equal(geometry.contentOverflow, false, `${pages[i]} content overflow`)
      assert.equal(geometry.outerScroll, false, `${pages[i]} outer scroll`)
      if (mobile) {
        assert.equal(geometry.contentLeft, 0)
        assert.ok(geometry.navHeight >= 68)
        assert.ok(geometry.contentBottom <= geometry.navTop + 1, 'Content ends above bottom navigation')
      }
      headerBoxes.push(await page.locator('h1').evaluate(e => ({ x: e.getBoundingClientRect().x, y: e.getBoundingClientRect().y, size: getComputedStyle(e).fontSize })))
      if (screenshots) await page.screenshot({ path: join(screenshots, `${viewport.width}-${viewport.height}-${pages[i]}.png`) })
    }
    for (const key of ['x', 'y', 'size']) assert.equal(new Set(headerBoxes.map(b => b[key])).size, 1, `Shared page heading ${key}`)
    const sections = page.getByRole('navigation', { name: 'Settings sections' })
    for (const name of ['Updates', 'Administrators', 'API keys', 'Password']) {
      await sections.getByRole('button', { name, exact: true }).click()
      const geometry = await measure()
      assert.equal(geometry.contentOverflow, false, `Settings/${name} overflow`)
      if (mobile) {
        const button = sections.getByRole('button', { name, exact: true })
        assert.ok(await button.evaluate(e => parseFloat(getComputedStyle(e).fontSize)) >= 16)
        assert.ok((await button.boundingBox()).height >= 48)
        if (name === 'Updates') {
          assert.ok(await page.locator('.webapp-version-details dt').first().evaluate(e => parseFloat(getComputedStyle(e).fontSize)) >= 14, 'Update metadata uses readable secondary type')
          assert.ok(await page.locator('.webapp-version-details dd').first().evaluate(e => parseFloat(getComputedStyle(e).fontSize)) >= 16, 'Update values use readable body type')
          assert.ok(await page.locator('.webapp-update-help').evaluate(e => parseFloat(getComputedStyle(e).fontSize)) >= 14)
        }
      }
      if (name === 'Administrators') await page.getByLabel('Username', { exact: true }).fill('Draft retained')
      if (screenshots) await page.screenshot({ path: join(screenshots, `${viewport.width}-${viewport.height}-settings-${name.replaceAll(' ', '-')}.png`) })
    }
    await sections.getByRole('button', { name: 'Administrators', exact: true }).click()
    assert.equal(await page.getByLabel('Username', { exact: true }).inputValue(), 'Draft retained')
    if (mobile) {
      assert.ok(await page.getByLabel('Username', { exact: true }).evaluate(e => parseFloat(getComputedStyle(e).fontSize)) >= 16)
      assert.equal(await page.locator('.mobile-account').getByText(user.username, { exact: true }).isVisible(), true)
      await nav.getByRole('link', { name: 'Monitor', exact: true }).click()
      await page.locator('.project-group').first().waitFor()
      const before = await nav.boundingBox()
      await page.locator('.app-content').evaluate(e => { e.scrollTop = e.scrollHeight })
      assert.deepEqual(await nav.boundingBox(), before, 'Bottom navigation stays stationary while content scrolls')
      await nav.getByRole('link', { name: 'Settings', exact: true }).click()
      await page.locator('.mobile-account').getByRole('button', { name: 'Sign out', exact: true }).click()
      await page.getByRole('heading', { name: 'Sign in', exact: true }).waitFor()
      assert.deepEqual(writes, ['POST /api/auth/logout'])
    } else assert.deepEqual(writes, [])
    assert.deepEqual(errors, [])
    console.log(`PASS shared mobile workspace ${viewport.width}x${viewport.height}: all pages, nav, typography, forms, scrolling, signout`)
  } finally { await context.close() }
}
try {
  for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 875 }, { width: 320, height: 560 }]) await exercise(viewport)
} finally { await browser.close(); await server.close() }
