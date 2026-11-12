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
const container = (name, overrides = {}) => ({ id: name, name, project: 'agent', service: name, image, state: 'running', health: 'healthy', monitorStatus: 'healthy', restartCount: 0, healthOutput: 'Health check passed.', ...overrides })
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
    await agent.locator('.project-summary').click()
    const cards = agent.locator('.container-detail')
    const geometry = await agent.evaluate(element => {
      const body = element.querySelector('.group-body'), cards = [...body.querySelectorAll('.container-detail')]
      const b = body.getBoundingClientRect(), first = cards[0].getBoundingClientRect(), second = cards[1].getBoundingClientRect()
      return { inset: first.left - b.left, rightInset: b.right - first.right, gap: second.top - first.bottom, radius: getComputedStyle(cards[0]).borderRadius, border: getComputedStyle(cards[0]).borderTopWidth, background: getComputedStyle(body).backgroundColor }
    })
    assert.ok(geometry.inset >= 10 && geometry.rightInset >= 10, `${width}: container cards inset from the project: ${JSON.stringify(geometry)}`)
    assert.ok(geometry.gap >= 8, `${width}: separate container cards have a visible gap`)
    assert.equal(geometry.radius, '6px')
    assert.equal(geometry.border, '1px')
    assert.equal(geometry.background, 'rgb(240, 242, 235)')
    assert.equal(await agent.locator('.project-expanded-facts, .container-dot').count(), 0)
    assert.equal(await cards.first().locator('.container-identity > span').count(), 0, 'Actual container name belongs in diagnostics, not a redundant summary line')
    const first = cards.first()
    const row = first.locator('.container-summary')
    assert.ok((await row.boundingBox()).height >= 48)
    if (width <= 640) {
      const layout = await row.evaluate(element => {
        const name = element.querySelector('.container-identity').getBoundingClientRect(), status = element.querySelector('.status-badge').getBoundingClientRect(), image = element.querySelector('.container-observation').getBoundingClientRect()
        return { nameY: name.y, statusY: status.y, nameRight: name.right, statusLeft: status.left, imageY: image.y, nameBottom: name.bottom }
      })
      assert.ok(Math.abs(layout.nameY - layout.statusY) < 8, `${width}: name and health share the first line`)
      assert.ok(layout.nameRight <= layout.statusLeft, `${width}: name and health never overlap`)
      assert.ok(layout.imageY >= layout.nameBottom, `${width}: image follows on its own line`)
    }
    await first.getByRole('button', { name: 'Edit agent-web-1 container icon', exact: true }).click()
    await page.getByRole('heading', { name: 'Edit agent-web-1 icon', exact: true }).waitFor()
    await page.getByRole('button', { name: 'Cancel', exact: true }).click()
    assert.equal(await first.evaluate(element => element.open), false, 'Editing the icon never expands its card')
    await row.focus()
    await page.keyboard.press('Enter')
    assert.equal(await first.locator('.container-inspection').isVisible(), true)
    await first.locator('.inspection-line').filter({ hasText: 'Name' }).getByText('agent-web-1', { exact: true }).waitFor()
    await first.locator('.inspection-line').getByText(image, { exact: true }).waitFor()
    await page.keyboard.press('Space')
    assert.equal(await first.evaluate(element => element.open), false)
    await agent.getByRole('button', { name: 'Review 1 update', exact: true }).click()
    await page.locator('.update-dialog').waitFor()
    await page.getByRole('button', { name: 'Cancel', exact: true }).click()
    const missing = page.locator('.project-group').filter({ has: page.locator('.project-summary .identity-name', { hasText: 'missing-project' }) })
    const missingCard = missing.locator('.container-detail')
    await missingCard.getByRole('button', { name: 'Resolve…', exact: true }).click()
    await page.locator('.resolution-dialog').waitFor()
    assert.equal(await missingCard.evaluate(element => element.open), false)
    await page.locator('.resolution-dialog').getByRole('button', { name: 'Cancel', exact: true }).click()
    for (const project of await page.locator('.project-group').all()) {
      if (!await project.evaluate(element => element.open)) await project.locator('.project-summary').click()
    }
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth || document.documentElement.scrollHeight > innerHeight), false)
    assert.equal(await page.locator('.app-content').evaluate(element => element.scrollWidth > element.clientWidth), false)
    for (const summary of await page.locator('.container-summary').all()) {
      assert.equal(await summary.evaluate(element => element.scrollWidth > element.clientWidth), false, `${width}: long names, status, resolution and images fit`)
    }
    if (screenshots && [1200, 390].includes(width)) {
      presentation = true
      await page.reload()
      await agent.waitFor()
      await agent.locator('.project-summary').click()
      await page.screenshot({ path: join(screenshots, `container-cards-${width}.png`) })
    }
    console.log(`PASS: ${width}px inset cards, container counts, diagnostics, icon editing, review/cancel, Resolve/cancel and overflow`)
    await context.close()
  }
  assert.deepEqual(errors, [])
  assert.deepEqual(writes, [], 'Layout and cancellation flows must not mutate services')
} finally { await browser.close(); await server.close() }
