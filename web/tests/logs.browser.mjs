import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { createServer } from '../node_modules/vite/dist/node/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM, headless: true } : { channel: process.env.BROWSER_CHANNEL || 'msedge', headless: true })
const wait = async (predicate, message = 'Timed out') => { for (let n = 0; n < 150; n++) { if (await predicate()) return; await new Promise(resolve => setTimeout(resolve, 30)) } assert.fail(message) }
const line = (n, containerId = 'web-id') => ({ id: `line-${n}`, timestamp: `2026-11-15T10:00:00.${String(n).padStart(9, '0')}Z`, containerId, container: containerId === 'web-id' ? 'app-web-1' : 'app-worker-1', stream: n % 2 ? 'stdout' : 'stderr', message: n === 100 ? '<img src=x onerror="window.compromised=true">' : `message ${n}` })
async function exercise(viewport) {
  const context = await browser.newContext({ viewport, acceptDownloads: true })
  const page = await context.newPage()
  page.setDefaultTimeout(4000)
  const errors = [], requests = []
  page.on('pageerror', error => errors.push(error.message))
  let newest = 200, unavailable = false, partial = false, empty = false, missingAfter = false, delayed = false, expired = false
  const containers = [{ id: 'web-id', name: 'app-web-1', project: 'app', service: 'web', state: 'running' }, { id: 'worker/id', name: 'app-worker-1', project: 'app', service: 'worker', state: 'running' }]
  await context.route('**/api/**', async route => {
    const url = new URL(route.request().url()), path = url.pathname
    if (path === '/api/auth/status') return route.fulfill({ json: { setupRequired: false } })
    if (path === '/api/auth/me') return route.fulfill({ json: { user: { id: 'admin', username: 'Fixture', role: 'admin' } } })
    if (path === '/api/projects') return route.fulfill({ json: { projects: [{ name: 'app', status: 'healthy', containers }] } })
    if (path === '/api/monitor') return route.fulfill({ json: { connected: true, lastChecked: new Date().toISOString(), incidents: [], events: [], notificationConfigured: false } })
    if (path === '/api/icons') return route.fulfill({ json: { available: false, icons: [] } })
    if (path === '/api/operations') return route.fulfill({ json: { operations: [] } })
    if (path === '/api/publishers') return route.fulfill({ json: { available: false, connections: [] } })
    if (path === '/api/logs') {
      const query = Object.fromEntries(url.searchParams); requests.push(query)
      if (delayed && query.container === 'worker/id') await new Promise(resolve => setTimeout(resolve, 500))
      if (unavailable) return route.fulfill({ status: 503, body: 'Fixture engine unavailable' })
      if (expired && (query.after || query.cursor)) return route.fulfill({ status: 409, body: 'Fixture history changed; reload recent logs' })
      if (empty) return route.fulfill({ json: { entries: [], hasMore: false, nextAfter: '650' } })
      const start = query.cursor ? Number(query.cursor) - 200 : query.after ? Number(query.after) + 1 : newest - 199
      const end = query.cursor ? Number(query.cursor) - 1 : Math.min(newest, start + 199)
      const entries = Array.from({ length: Math.max(0, end - start + 1) }, (_, i) => line(start + i, query.container || 'web-id'))
      return route.fulfill({ json: { entries, hasMore: true, nextCursor: partial ? undefined : String(start), nextAfter: partial ? query.after : missingAfter ? undefined : String(query.after ? Math.max(end, Number(query.after)) : newest), hasNewer: partial && query.after ? true : end < newest && !!query.after, warnings: partial ? [{ containerId: 'worker/id', container: 'app-worker-1', message: 'Fixture source unavailable' }] : [] } })
    }
    return route.fulfill({ status: 404, json: { error: path } })
  })
  try {
    await page.goto(`${server.resolvedUrls.local[0]}#service/app`)
    await page.getByRole('heading', { name: 'app', exact: true }).waitFor()
    assert.equal(requests.length, 0, 'Overview does not load logs')
    const logsTab = page.getByRole('tab', { name: 'Logs', exact: true })
    await logsTab.click()
    await page.locator('[data-log-id="line-200"]').waitFor()
    assert.deepEqual(requests[0], { project: 'app', limit: '200' })
    assert.equal(await page.getByRole('tab', { name: 'Logs' }).getAttribute('aria-selected'), 'true')
    assert.equal(await page.getByLabel('Wrap lines', { exact: true }).isChecked(), true, 'Logs start wrapped for readable desktop and phone output')
    const box = await page.getByRole('region', { name: 'Log output', exact: true }).boundingBox()
    if (box.height <= viewport.height * .32) console.log(await page.locator('.service-detail').evaluate(element => Array.from(element.children).map(node => ({ class: node.className, text: node.textContent.slice(0, 80), box: { y: node.getBoundingClientRect().y, h: node.getBoundingClientRect().height }, style: getComputedStyle(node).marginBottom }))))
    assert.ok(box.height > viewport.height * .32, JSON.stringify(box))
    assert.ok(box.y + box.height <= viewport.height + 1, 'Output fits remaining viewport')
    assert.equal(await page.locator('.logs-output').evaluate(element => getComputedStyle(element).backgroundColor), 'rgb(32, 42, 38)', 'Log viewer preserves approved dark terminal')
    if (viewport.width < 640) {
      assert.ok(await page.locator('.log-message').first().evaluate(element => parseFloat(getComputedStyle(element).fontSize)) >= 16, 'Phone log messages are readable')
    }
    if (process.env.LOGS_SCREENSHOT_DIR) {
      await mkdir(process.env.LOGS_SCREENSHOT_DIR, { recursive: true })
      await page.screenshot({ path: join(process.env.LOGS_SCREENSHOT_DIR, `logs-${viewport.width}.png`) })
    }
    assert.equal(await page.locator('.logs-output img').count(), 0)
    assert.equal(await page.evaluate(() => window.compromised), undefined)
    const output = page.getByRole('region', { name: 'Log output', exact: true })
    // Older pages retain the same visible line, even when rows have unequal heights.
    await output.evaluate(element => { element.scrollTop = 50; element.dispatchEvent(new Event('scroll')) })
    const anchor = await page.locator('[data-log-id="line-5"]').evaluate(element => element.getBoundingClientRect().top)
    await page.locator('[data-log-id="line--199"]').waitFor()
    assert.ok(Math.abs((await page.locator('[data-log-id="line-5"]').evaluate(element => element.getBoundingClientRect().top)) - anchor) < 2, 'Stable prepend anchor')
    assert.equal(await page.getByText('Paused', { exact: true }).isVisible(), true)
    await page.getByRole('searchbox', { name: 'Search loaded logs', exact: true }).fill('message 101')
    await new Promise(resolve => setTimeout(resolve, 100))
    assert.equal(await page.getByText('Paused', { exact: true }).isVisible(), true, 'Filtering a paused window must not resume following')
    await page.getByRole('searchbox', { name: 'Search loaded logs', exact: true }).fill('')
    newest = 650
    await new Promise(resolve => setTimeout(resolve, 2200))
    assert.equal(await page.locator('[data-log-id="line-650"]').count(), 0, 'Paused output stays put')
    await page.getByRole('button', { name: 'Jump to latest', exact: true }).click()
    await page.locator('[data-log-id="line-650"]').waitFor()
    assert.ok(requests.some(query => query.after === '200') && requests.some(query => query.after === '400') && requests.some(query => query.after === '600'), 'Resume drains all incremental pages')
    assert.equal(await page.getByText('Following', { exact: true }).isVisible(), true)
    expired = true
    await page.getByRole('alert').filter({ hasText: 'Fixture history changed' }).waitFor()
    assert.equal(await page.locator('[data-log-id="line-650"]').count(), 1, 'Invalidated cursor retains existing output')
    const beforeReset = requests.length
    expired = false
    await page.getByRole('button', { name: 'Jump to latest', exact: true }).click()
    await wait(() => Promise.resolve(requests.length > beforeReset))
    assert.deepEqual(requests[beforeReset], { project: 'app', limit: '200' }, 'Explicit jump reloads recent after cursor invalidation')
    await page.locator('[data-log-id="line-650"]').waitFor()
    const search = page.getByRole('searchbox', { name: 'Search loaded logs', exact: true })
    await search.fill('message 649')
    assert.equal(await page.locator('[data-log-id]').count(), 1)
    await page.getByLabel('Stream (loaded)', { exact: true }).selectOption('stderr')
    assert.equal(await page.locator('[data-log-id]').count(), 0)
    const beforeEmpty = requests.filter(query => query.cursor).length
    await output.evaluate(element => { element.scrollTop = 0; element.dispatchEvent(new Event('scroll')) })
    await new Promise(resolve => setTimeout(resolve, 100))
    assert.equal(requests.filter(query => query.cursor).length, beforeEmpty, 'No automatic history requests for zero matches')
    await page.getByLabel('Stream (loaded)', { exact: true }).selectOption('')
    const downloadPromise = page.waitForEvent('download')
    await page.getByRole('button', { name: 'Download loaded filtered logs', exact: true }).click()
    const download = await downloadPromise
    const stream = await download.createReadStream(); const chunks = []
    for await (const chunk of stream) chunks.push(chunk)
    const text = Buffer.concat(chunks).toString('utf8')
    assert.match(text, /message 649/); assert.doesNotMatch(text, /message 648/)
    await search.fill('')
    await page.getByLabel('Wrap lines', { exact: true }).check()
    assert.equal(await page.locator('.logs-output').evaluate(element => element.classList.contains('wrap')), true)
    await page.getByLabel('Container', { exact: true }).selectOption('worker/id')
    await wait(() => Promise.resolve(requests.some(query => query.container === 'worker/id' && !query.cursor && !query.after)))
    await page.locator('[data-log-id="line-650"]').waitFor()
    assert.equal(await page.getByLabel('Container', { exact: true }).inputValue(), 'worker/id')
    assert.equal(await page.locator('[data-log-id="line-650"]').textContent().then(text => text.includes('app-worker-1')), true)
    unavailable = true
    await page.getByRole('alert').filter({ hasText: 'Fixture engine unavailable' }).waitFor()
    assert.equal(await page.locator('[data-log-id="line-650"]').count(), 1, 'Errors retain output')
    const beforeRetry = requests.length
    unavailable = false; partial = true; newest = 651
    await page.getByRole('button', { name: 'Retry logs', exact: true }).click()
    await page.getByText('Partial logs · 1 source warnings', { exact: true }).waitFor()
    await page.locator('[data-log-id="line-651"]').waitFor()
    assert.equal(requests[beforeRetry].container, 'worker/id', 'Retry preserves scope')
    assert.equal(await page.getByText('Paused', { exact: true }).isVisible(), true, 'Partial reads retain good output but pause without advancing the failed source')
    partial = false
    await page.getByRole('button', { name: 'Retry logs', exact: true }).click()
    await page.getByRole('button', { name: 'Jump to latest', exact: true }).click()
    newest = 650
    // Visibility cancellation prevents background requests and resumes from the last cursor.
    await page.evaluate(() => { Object.defineProperty(document, 'hidden', { configurable: true, value: true }); document.dispatchEvent(new Event('visibilitychange')) })
    const hiddenRequests = requests.length
    await new Promise(resolve => setTimeout(resolve, 2200))
    assert.equal(requests.length, hiddenRequests)
    await page.evaluate(() => { Object.defineProperty(document, 'hidden', { configurable: true, value: false }); document.dispatchEvent(new Event('visibilitychange')) })
    await wait(() => Promise.resolve(requests.length > hiddenRequests))
    await page.getByRole('tab', { name: 'Logs', exact: true }).focus()
    await page.keyboard.press('ArrowLeft')
    assert.equal(await page.getByRole('tab', { name: 'Overview', exact: true }).getAttribute('aria-selected'), 'true')
    assert.equal(await page.getByRole('heading', { name: 'Containers', exact: false }).isVisible(), true)
    const unmountedRequests = requests.length
    await new Promise(resolve => setTimeout(resolve, 2200))
    assert.equal(requests.length, unmountedRequests, 'No polling after tab unmount')
    await page.evaluate(() => { location.hash = '#service/app/app-worker-1' })
    await page.getByRole('heading', { name: 'app-worker-1', exact: true }).waitFor()
    await page.getByRole('tab', { name: 'Logs', exact: true }).click()
    await page.locator('[data-log-id="line-650"]').waitFor()
    assert.equal(requests.at(-1).container, 'worker/id')
    assert.equal(await page.getByLabel('Container', { exact: true }).count(), 0, 'Container details stay scoped')
    await page.evaluate(() => { location.hash = '#monitor' })
    await page.getByRole('heading', { name: 'Services', exact: true }).waitFor()
    const navigatedRequests = requests.length
    await new Promise(resolve => setTimeout(resolve, 2200))
    assert.equal(requests.length, navigatedRequests, 'No polling after navigation')
    // Scope changes abort old transport and ignore delayed results from the old ID.
    partial = false; delayed = true
    await page.evaluate(() => { location.hash = '#service/app' })
    await page.getByRole('tab', { name: 'Logs', exact: true }).click()
    await page.locator('[data-log-id="line-650"]').waitFor()
    await page.getByLabel('Container', { exact: true }).selectOption('worker/id')
    await wait(() => Promise.resolve(requests.at(-1).container === 'worker/id'))
    await page.getByLabel('Container', { exact: true }).selectOption('web-id')
    await page.locator('[data-log-id="line-650"]').waitFor()
    await new Promise(resolve => setTimeout(resolve, 650))
    assert.equal(await page.locator('.log-source').evaluateAll(nodes => nodes.every(node => node.textContent === 'app-web-1')), true)
    assert.equal(await page.getByLabel('Container', { exact: true }).inputValue(), 'web-id')
    delayed = false; empty = true
    await page.getByLabel('Container', { exact: true }).selectOption('worker/id')
    await page.getByText('No logs reported.', { exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: 'Load older', exact: true }).isDisabled(), true)
    empty = false; missingAfter = true
    await page.getByLabel('Container', { exact: true }).selectOption('web-id')
    await page.getByRole('alert').filter({ hasText: 'Live continuation unavailable' }).waitFor()
    assert.equal(await page.locator('[data-log-id="line-650"]').count(), 1)
    const noCursorRequests = requests.length
    await new Promise(resolve => setTimeout(resolve, 2200))
    assert.equal(requests.length, noCursorRequests, 'Missing incremental cursor never silently tails over an unread interval')
    missingAfter = false
    await page.getByRole('button', { name: 'Jump to latest', exact: true }).click()
    await page.locator('[data-log-id="line-650"]').waitFor()
    if (viewport.width > 640) {
      await page.getByRole('searchbox', { name: 'Search loaded logs', exact: true }).fill('no matching fixture')
      const older = page.getByRole('button', { name: 'Load older', exact: true })
      for (let n = 0; n < 52; n++) {
        await older.click()
        await wait(async () => !await older.isDisabled(), `Older page ${n} settled`)
      }
      await page.getByText(/Loaded log window reached its limit/).waitFor()
      assert.match(await page.locator('.logs-count').textContent(), /10000 loaded/)
      assert.ok(Number(requests.filter(query => query.cursor).at(-1).cursor) < -9000, 'History remains pageable past the memory cap')
      const beforeReload = requests.length
      await page.getByRole('button', { name: 'Jump to latest', exact: true }).click()
      await wait(() => Promise.resolve(requests.length > beforeReload))
      assert.deepEqual(requests[beforeReload], { project: 'app', container: 'web-id', limit: '200' }, 'Capped window explicitly reloads recent instead of skipping an unread interval')
      await page.getByRole('searchbox', { name: 'Search loaded logs', exact: true }).fill('')
      await page.locator('[data-log-id="line-650"]').waitFor()
      assert.equal(await page.locator('[data-log-id]').count(), 200)
      newest = 12000
      await page.getByRole('button', { name: 'Jump to latest', exact: true }).click()
      await page.locator('[data-log-id="line-12000"]').waitFor({ timeout: 30000 })
      assert.equal(await page.getByText('Following', { exact: true }).isVisible(), true, 'Live following keeps moving after the loaded buffer fills')
      assert.ok(await page.locator('[data-log-id]').count() <= 10000, 'Live buffer stays bounded')
      assert.ok(requests.some(query => query.after === '10650'), 'Live catch-up crosses the buffer cap without dropping unread pages')
    }
    await page.getByRole('button', { name: 'Load older', exact: true }).waitFor({ state: 'visible' })
    await wait(async () => !await page.getByRole('button', { name: 'Load older', exact: true }).isDisabled())
    await page.getByRole('tab', { name: 'Overview', exact: true }).click()
    await new Promise(resolve => setTimeout(resolve, 100))
    assert.deepEqual(errors, [])
    console.log(`PASS logs paging/follow/filters/scope/lifecycle/races/cap ${viewport.width}x${viewport.height}`)
  } finally { await context.close() }
}
try { for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) await exercise(viewport) }
finally { await browser.close(); await server.close() }
