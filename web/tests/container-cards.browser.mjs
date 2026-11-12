// Real app and CSS with isolated inventory fixtures; no live Docker or API writes.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from '../node_modules/vite/dist/node/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || 'msedge', headless: true })
const screenshots = process.env.CONTAINER_CARDS_SCREENSHOT_DIR
if (screenshots) await mkdir(screenshots, { recursive: true })
const image = 'ghcr.io/everyonesoftware/a-very-long-image-name-for-overflow-verification:latest'
const container = (name, overrides = {}) => ({ id: name, name, project: 'agent', service: name, image, state: 'running', health: 'healthy', monitorStatus: 'healthy', restartCount: 0, exitCode: 0, oomKilled: false, startedAt: '2026-11-12T09:00:00Z', healthOutput: 'Health check passed.', ...overrides })
const projects = [
  { name: 'agent', status: 'healthy', containers: [container('agent', { name: 'agent-web-1', service: 'web', updateAvailable: true, currentDigest: 'sha256:old', proposedDigest: 'sha256:new' }), container('agent-tailscale', { name: 'agent-web-2', service: 'web', image: 'tailscale/tailscale:latest' })] },
  { name: 'audiobookshelf', status: 'running-unverified', containers: [container('audiobookshelf-with-a-long-container-name', { project: 'audiobookshelf', monitorStatus: 'running-unverified', health: '' })] },
  { name: 'missing-project', status: 'missing', containers: [container('missing-with-a-long-container-name', { project: 'missing-project', state: 'missing', monitorStatus: 'missing', health: '', monitorReason: 'Container no longer present.' })] },
  { name: 'unhealthy-project', status: 'unhealthy', containers: [container('unhealthy-with-a-long-container-name', { project: 'unhealthy-project', monitorStatus: 'unhealthy', health: 'unhealthy', monitorReason: 'A long health-check failure reason remains readable without widening the card.' })] },
]
const errors = [], writes = []
try {
  for (const width of [1200, 800, 641, 640, 390, 320]) {
    const context = await browser.newContext({ viewport: { width, height: 900 }, serviceWorkers: 'block' })
    const page = await context.newPage()
    let presentation = false
    page.on('pageerror', error => errors.push(error.message))
    await page.route('**/api/**', route => {
      const request = route.request(), path = new URL(request.url()).pathname
      if (request.method() !== 'GET') writes.push(`${request.method()} ${path}`)
      const fixtures = {
        '/api/auth/status': { setupRequired: false }, '/api/auth/me': { user: { id: 'cards-fixture', username: 'Layout fixture', role: 'admin' } },
        '/api/projects': { projects: presentation ? projects.slice(0, 2) : projects }, '/api/icons': { available: true, icons: [] }, '/api/operations': { operations: [] },
        '/api/monitor': { connected: true, lastChecked: new Date().toISOString(), incidents: presentation ? [] : [{ id: 'missing-project/missing-with-a-long-container-name/missing-with-a-long-container-name', project: 'missing-project', name: 'missing-with-a-long-container-name', service: 'missing-with-a-long-container-name', status: 'missing', reason: 'Container no longer present.', since: '2026-11-12T10:00:00Z' }], events: [] },
      }
      return Object.hasOwn(fixtures, path) ? route.fulfill({ json: fixtures[path] }) : route.fulfill({ status: 404 })
    })
    await page.goto(server.resolvedUrls.local[0])
    const agent = page.locator('.project-group').filter({ has: page.locator('.project-summary .identity-name', { hasText: /^agent$/ }) })
    await agent.waitFor()
    assert.equal(await agent.locator('.project-summary-counts').innerText(), '2 containers', 'Replica count must not collapse to the unique service count')
    assert.equal(await agent.evaluate(element => element.tagName), 'ARTICLE')
    assert.equal(await agent.locator('div.group-summary.project-summary').count(), 1)
    assert.equal(await agent.locator('details, .container-row, .container-inspection').count(), 0, 'Monitor services are compact, not nested container disclosures')
    const projectLink = agent.locator('a.project-link')
    assert.equal(await projectLink.getAttribute('href'), `#service/${encodeURIComponent('agent')}`)
    await projectLink.click()
    await page.locator('.service-detail').getByRole('heading', { name: 'agent', level: 1, exact: true }).waitFor()
    const breadcrumb = page.locator('nav[aria-label="Breadcrumb"]')
    assert.equal(await breadcrumb.getByRole('link', { name: 'Monitor', exact: true }).count(), 1)
    const cards = page.locator('.service-detail .container-row')
    assert.equal(await cards.count(), 2)
    assert.equal(await page.locator('.service-detail details, .service-detail .container-inspection').count(), 0)
    assert.deepEqual(await cards.locator('a.container-link .identity-name').allTextContents().then(names => names.map(name => name.trim())), ['agent-web-1', 'agent-web-2'])
    for (const [index, name] of ['agent-web-1', 'agent-web-2'].entries()) {
      assert.equal(await cards.nth(index).locator('a.container-link').getAttribute('href'), `#service/agent/${encodeURIComponent(name)}`)
      assert.equal(await cards.nth(index).locator('a.container-link button').count(), 0, 'Icon and action buttons are independent of navigation links')
      assert.equal(await cards.nth(index).locator('div.container-summary').count(), 1)
    }
    const geometry = await cards.first().evaluate(first => {
      const second = first.nextElementSibling, a = first.getBoundingClientRect(), b = second.getBoundingClientRect()
      const style = getComputedStyle(first)
      return { gap: Math.max(b.top - a.bottom, b.left - a.right), radius: parseFloat(style.borderRadius), border: parseFloat(style.borderTopWidth) }
    })
    assert.ok(geometry.gap >= 8, `${width}: separate clickable container cards have a visible gap: ${JSON.stringify(geometry)}`)
    assert.ok(geometry.radius > 0 && geometry.border >= 1, 'Cards retain distinct rounded boundaries')
    const first = cards.first()
    const row = first.locator('.container-summary')
    assert.ok((await row.boundingBox()).height >= 48)
    const serviceURL = page.url()
    await first.getByRole('button', { name: 'Edit agent-web-1 container icon', exact: true }).click()
    await page.getByRole('heading', { name: 'Edit agent-web-1 icon', exact: true }).waitFor()
    await page.getByRole('button', { name: 'Cancel', exact: true }).click()
    assert.equal(page.url(), serviceURL, 'Editing the icon never navigates')
    assert.equal(await page.locator('.container-inspection').count(), 0)
    await first.locator('a.container-link').focus()
    await page.keyboard.press('Enter')
    const inspection = page.locator('.container-page .container-inspection')
    await inspection.waitFor()
    assert.equal(new URL(page.url()).hash, '#service/agent/agent-web-1')
    await page.locator('.container-page').getByRole('heading', { level: 1, name: 'agent-web-1', exact: true }).waitFor()
    for (const [label, value] of [['State', 'running'], ['Health check', 'healthy'], ['Restarts', '0'], ['Exit code', '0'], ['OOM killed', 'No']]) {
      assert.equal(await inspection.getByText(label, { exact: true }).locator('..').locator('dd').innerText(), value)
    }
    assert.notEqual(await inspection.getByText('Started', { exact: true }).locator('..').locator('dd').innerText(), 'Not reported')
    await inspection.getByText(image, { exact: true }).waitFor()
    await inspection.getByRole('heading', { name: 'Health output', exact: true }).waitFor()
    await inspection.getByText('Health check passed.', { exact: true }).waitFor()
    assert.equal(await page.locator('.container-inspection').count(), 1)
    await breadcrumb.getByRole('link', { name: 'agent', exact: true }).click()
    await page.locator('.service-detail:not(.container-page)').getByRole('heading', { name: 'agent', level: 1, exact: true }).waitFor()
    assert.equal(page.url(), serviceURL)
    assert.equal(await page.locator('.container-inspection').count(), 0)
    await cards.nth(1).locator('a.container-link').click()
    assert.equal(new URL(page.url()).hash, '#service/agent/agent-web-2')
    await page.locator('.container-page').getByRole('heading', { level: 1, name: 'agent-web-2', exact: true }).waitFor()
    await inspection.getByText('tailscale/tailscale:latest', { exact: true }).waitFor()
    await breadcrumb.getByRole('link', { name: 'agent', exact: true }).click()
    await page.locator('.service-detail:not(.container-page)').getByRole('heading', { name: 'agent', level: 1, exact: true }).waitFor()
    await breadcrumb.getByRole('link', { name: 'Monitor', exact: true }).click()
    await agent.waitFor()
    const monitorURL = page.url()
    await agent.getByRole('button', { name: 'Review 1 update', exact: true }).click()
    await page.locator('.update-dialog').waitFor()
    assert.equal(page.url(), monitorURL, 'Review never navigates')
    await page.getByRole('button', { name: 'Cancel', exact: true }).click()
    const missing = page.locator('.project-group').filter({ has: page.locator('.project-summary .identity-name', { hasText: 'missing-project' }) })
    await missing.locator('a.project-link').click()
    const missingCard = page.locator('.container-row')
    const missingURL = page.url()
    await missingCard.getByRole('button', { name: 'Resolve…', exact: true }).click()
    await page.locator('.resolution-dialog').waitFor()
    assert.equal(page.url(), missingURL, 'Resolve never navigates')
    assert.equal(await missingCard.locator('a.container-link button').count(), 0)
    assert.equal(await page.locator('.container-inspection').count(), 0)
    await page.locator('.resolution-dialog').getByRole('button', { name: 'Cancel', exact: true }).click()
    await breadcrumb.getByRole('link', { name: 'Monitor', exact: true }).click()
    for (const project of await page.locator('.project-group').all()) {
      await project.locator('a.project-link').click()
      await page.locator('.service-detail').waitFor()
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${width}: service page fits the viewport`)
      assert.equal(await page.locator('.app-content').evaluate(element => element.scrollWidth > element.clientWidth), false)
      for (const summary of await page.locator('.container-summary').all()) {
        assert.equal(await summary.evaluate(element => element.scrollWidth > element.clientWidth), false, `${width}: long names, status, resolution and images fit`)
      }
      await breadcrumb.getByRole('link', { name: 'Monitor', exact: true }).click()
    }
    if (screenshots && [1200, 390].includes(width)) {
      presentation = true
      await page.reload()
      await agent.waitFor()
      await agent.locator('a.project-link').click()
      await page.locator('.service-detail').waitFor()
      await page.screenshot({ path: join(screenshots, `container-cards-${width}.png`) })
    }
    console.log(`PASS: ${width}px compact service navigation, separate replica cards, runtime/health/image detail, breadcrumbs, independent icon/review/Resolve actions and overflow`)
    await context.close()
  }
  assert.deepEqual(errors, [])
  assert.deepEqual(writes, [], 'Layout and cancellation flows must not mutate services')
} finally { await browser.close(); await server.close() }
