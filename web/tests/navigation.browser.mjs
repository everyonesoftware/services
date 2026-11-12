// Real application routing against isolated APIs. No live Docker requests.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from '../node_modules/vite/dist/node/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || 'msedge', headless: true })
const errors = [], writes = []
const projectName = 'media / archive #1'
const containerName = 'web / replica #1'
const serviceHash = `#service/${encodeURIComponent(projectName)}`
const containerHash = `${serviceHash}/${encodeURIComponent(containerName)}`
const container = { id: 'replaceable-id', name: containerName, project: projectName, service: 'web', image: 'ghcr.io/demo/a-long-image-reference-for-layout-verification:latest', state: 'running', health: 'healthy', restartCount: 0, healthOutput: 'GET /health → 200 OK <script>', updateAvailable: true, currentDigest: 'sha256:old', proposedDigest: 'sha256:new' }
const original = [{ name: projectName, status: 'healthy', containers: [container, { ...container, name: 'sidecar', id: 'sidecar-id', service: 'tailscale', image: 'tailscale/tailscale:latest', health: '', healthOutput: '', updateAvailable: false }] }, { name: 'other', status: 'healthy', containers: [{ ...container, project: 'other', name: 'other-web', updateAvailable: false }] }]
const base = server.resolvedUrls.local[0]
const shot = async (page, name) => {
  if (!process.env.NAVIGATION_SCREENSHOT_DIR) return
  await mkdir(process.env.NAVIGATION_SCREENSHOT_DIR, { recursive: true })
  await page.screenshot({ path: join(process.env.NAVIGATION_SCREENSHOT_DIR, `${name}.png`) })
}
const fits = async page => {
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth || document.documentElement.scrollHeight > innerHeight), false)
  assert.equal(await page.locator('.app-content').evaluate(el => el.scrollWidth > el.clientWidth), false)
}
try {
  for (const width of [1200, 800, 390, 320]) {
    const context = await browser.newContext({ viewport: { width, height: 900 }, serviceWorkers: 'block' })
    const page = await context.newPage()
    page.setDefaultTimeout(5000)
    page.on('pageerror', error => errors.push(error.message))
    let projects = structuredClone(original), connected = true, failProjects = false, failMonitor = false
    await page.route('**/api/**', route => {
      const req = route.request(), path = new URL(req.url()).pathname
      if (req.method() !== 'GET') writes.push(`${req.method()} ${path}`)
      const fixtures = {
        '/api/auth/status': { setupRequired: false }, '/api/auth/me': { user: { id: 'navigation-fixture', username: 'Fixture', role: 'admin' } },
        '/api/projects': { projects }, '/api/icons': { available: true, icons: [] }, '/api/operations': { operations: [] },
        '/api/monitor': { connected, lastChecked: new Date().toISOString(), incidents: [], events: [
          { id: 'ours', project: projectName, name: containerName, kind: 'healthy', message: 'Scoped health event', time: new Date().toISOString() },
          { id: 'theirs', project: 'other', name: 'other-web', kind: 'healthy', message: 'Unrelated event must not leak', time: new Date().toISOString() },
        ] },
      }
      if ((failProjects && path === '/api/projects') || (failMonitor && path === '/api/monitor')) return route.fulfill({ status: 503, body: 'Fixture unavailable' })
      return Object.hasOwn(fixtures, path) ? route.fulfill({ json: fixtures[path] }) : route.fulfill({ status: 404 })
    })
    await page.goto(base)
    await page.locator('.project-group').first().waitFor()
    assert.equal(await page.locator('.project-group details,.project-group summary,details.project-group').count(), 0, 'Service inventory has navigation, not expanders')
    await page.getByRole('searchbox', { name: 'Search services' }).fill('media')
    const projectLink = page.locator('.project-link').filter({ hasText: projectName })
    assert.equal(await projectLink.getAttribute('href'), serviceHash)
    await projectLink.click()
    await page.getByRole('heading', { name: projectName, exact: true }).waitFor()
    assert.equal(new URL(page.url()).hash, serviceHash)
    await page.locator('.breadcrumb [aria-current="page"]').getByText(projectName, { exact: true }).waitFor()
    assert.equal(await page.locator('.service-detail details,.service-detail summary').count(), 0)
    assert.equal(await page.locator('.container-link').count(), 2)
    const cardLayout = await page.locator('.container-link').first().evaluate(element => {
      const name = element.querySelector('.identity-name').getBoundingClientRect()
      const image = element.querySelector('.container-observation').getBoundingClientRect()
      const status = element.querySelector('.container-link-meta').getBoundingClientRect()
      return { nameBottom: name.bottom, imageY: image.y, imageBottom: image.bottom, statusY: status.y }
    })
    assert.ok(cardLayout.nameBottom <= cardLayout.imageY && cardLayout.imageBottom <= cardLayout.statusY, `Container identity, image and status stack in reading order: ${JSON.stringify(cardLayout)}`)
    await page.getByText('Scoped health event', { exact: true }).waitFor()
    assert.equal(await page.getByText('Unrelated event must not leak', { exact: true }).count(), 0)
    await fits(page)
    if ([1200, 390].includes(width)) await shot(page, `service-${width}`)
    const review = page.getByRole('button', { name: 'Review 1 update', exact: true })
    await review.click()
    await page.getByRole('dialog').waitFor()
    await page.keyboard.press('Escape')
    assert.equal(await review.evaluate(el => el === document.activeElement), true)
    assert.equal(new URL(page.url()).hash, serviceHash)
    await page.locator('.container-link').filter({ hasText: containerName }).click()
    await page.getByRole('heading', { name: containerName, exact: true }).waitFor()
    assert.equal(new URL(page.url()).hash, containerHash)
    await page.getByRole('heading', { name: 'Runtime', exact: true }).waitFor()
    await page.getByText('GET /health → 200 OK <script>', { exact: true }).waitFor()
    assert.equal(await page.locator('.container-page script').count(), 0)
    await fits(page)
    if ([1200, 390].includes(width)) await shot(page, `container-${width}`)

    await page.locator('.breadcrumb').getByRole('link', { name: projectName, exact: true }).click()
    await page.getByRole('heading', { name: projectName, exact: true }).waitFor()
    await page.goBack()
    await page.getByRole('heading', { name: containerName, exact: true }).waitFor()
    await page.goForward()
    await page.getByRole('heading', { name: projectName, exact: true }).waitFor()
    await page.locator('.breadcrumb').getByRole('link', { name: 'Monitor', exact: true }).click()
    await page.getByRole('heading', { name: 'Services', exact: true }).waitFor()
    assert.equal(await page.getByRole('searchbox', { name: 'Search services' }).inputValue(), 'media', 'Drilldown preserves the Monitor search')
    await page.goto(base + containerHash)
    await page.getByRole('heading', { name: containerName, exact: true }).waitFor()
    // Container names survive recreation; Docker IDs must not be the route key.
    projects[0].containers[0].id = 'replacement-id'
    await page.reload()
    await page.getByRole('heading', { name: containerName, exact: true }).waitFor()
    await page.getByText('replacement-id', { exact: true }).waitFor()
    await page.locator('.container-siblings .container-link').click()
    await page.getByRole('heading', { name: 'sidecar', exact: true }).waitFor()
    await page.getByText('No health-check output reported.', { exact: true }).waitFor()
    await page.goto(base + '#service/unknown')
    await page.getByRole('heading', { name: 'Service not found', exact: true }).waitFor()
    await fits(page)
    await page.goto(base + `${serviceHash}/unknown`)
    await page.getByRole('heading', { name: 'Container not found', exact: true }).waitFor()
    await page.goto(base + '#service/%E0%A4%A')
    await page.getByRole('heading', { name: 'Page not found', exact: true }).waitFor()
    failProjects = true
    await page.goto(base + serviceHash)
    await page.reload()
    await page.getByRole('heading', { name: 'Services couldn’t be loaded', exact: true }).waitFor()
    failProjects = false
    await page.getByRole('button', { name: 'Try again', exact: true }).click()
    await page.getByRole('heading', { name: projectName, exact: true }).waitFor()
    connected = false
    await page.reload()
    await page.getByRole('heading', { name: projectName, exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: 'Review 1 update', exact: true }).isDisabled(), true)
    await page.getByRole('alert').filter({ hasText: 'Monitoring interrupted' }).waitFor()
    connected = true; failMonitor = true
    await page.reload()
    await page.getByRole('heading', { name: projectName, exact: true }).waitFor()
    assert.equal(await page.getByText('No recent activity reported.', { exact: true }).count(), 0, 'Failed event retrieval must not be presented as an empty history')
    connected = true; failMonitor = false
    projects = [...structuredClone(original), ...Array.from({ length: 32 }, (_, index) => ({ name: `scroll-${index}`, status: 'healthy', containers: [{ ...container, project: `scroll-${index}`, updateAvailable: false }] }))]
    await page.goto(base + '#monitor')
    await page.reload()
    const distantLink = page.locator('.project-link[href="#service/scroll-31"]')
    await distantLink.waitFor()
    await distantLink.scrollIntoViewIfNeeded()
    const listScroll = await page.locator('.app-content').evaluate(el => el.scrollTop)
    assert.ok(listScroll > 100)

    await distantLink.click()
    await page.getByRole('heading', { name: 'scroll-31', exact: true }).waitFor()
    await page.goBack()
    await page.getByRole('heading', { name: 'Services', exact: true }).waitFor()

    assert.ok(Math.abs(await page.locator('.app-content').evaluate(el => el.scrollTop) - listScroll) < 4, 'Back restores the Monitor list scroll position')
    await context.close()
    console.log(`PASS service/container navigation, encoded names, history, safety and layout at ${width}px`)
  }
  assert.deepEqual(errors, [])
  assert.deepEqual(writes, [], 'Navigating and reviewing never performs a mutation')
} finally { await browser.close(); await server.close() }
