// Run with Playwright available, or PLAYWRIGHT_MODULE pointing to its index.mjs.
// Uses browser-only API fixtures; does not mutate a real Services server.
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from '../node_modules/vite/dist/node/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || 'msedge', headless: true })
const page = await browser.newPage({ viewport: { width: 1200, height: 900 } })
const errors = []
page.on('pageerror', error => errors.push(error.message))
let icons = []
let available = true
let iconFailure = false
let failWrite = false
let failReadback = false
let writeGate = null
let readGate = null
let requests = []
let containerId = 'docker-id-1'
const wait = async predicate => { for (let i = 0; i < 100; i++) { if (await predicate()) return; await new Promise(resolve => setTimeout(resolve, 20)) } throw new Error('Timed out waiting for browser assertion') }
await page.route('**/api/**', async route => {
  const request = route.request()
  const url = new URL(request.url())
  if (url.pathname.startsWith('/api/icons/images/')) return route.fulfill({ path: fileURLToPath(new URL('../public/icons/jellyfin.png', import.meta.url)), contentType: 'image/png' })
  if (url.pathname === '/api/icons') {
    requests.push({ method: request.method(), url: url.pathname + url.search, body: request.postDataJSON() })
    if (request.method() === 'GET') {
      if (readGate) await readGate
      if (iconFailure || failReadback) return route.fulfill({ status: 503, body: 'Icon storage unavailable' })
      return route.fulfill({ json: { available, icons } })
    }
    if (writeGate) await writeGate
    if (failWrite) return route.fulfill({ status: 415, body: 'Only PNG, JPEG and GIF images are supported' })
    const target = request.method() === 'PUT' ? request.postDataJSON() : { kind: url.searchParams.get('kind'), key: url.searchParams.get('key') }
    icons = icons.filter(icon => icon.kind !== target.kind || icon.key !== target.key)
    if (request.method() === 'DELETE') return route.fulfill({ status: 204 })
    const icon = { kind: target.kind, key: target.key, url: `/api/icons/images/${target.kind}.png`, sourceUrl: target.url }
    icons.push(icon)
    return route.fulfill({ json: icon })
  }
  if (url.pathname === '/api/projects') return route.fulfill({ json: { projects: [{ name: 'media', status: 'healthy', containers: [{ id: containerId, name: 'media-jellyfin-1', service: 'jellyfin', project: 'media', image: 'jellyfin/jellyfin:latest', state: 'running', health: 'healthy' }] }] } })
  if (url.pathname === '/api/monitor') return route.fulfill({ json: { connected: true, lastChecked: new Date().toISOString(), error: null, incidents: [], events: [], notificationConfigured: false } })
  if (url.pathname === '/api/operations') return route.fulfill({ json: { operations: [] } })
  return route.fulfill({ status: 404 })
})
try {
  await page.goto(server.resolvedUrls.local[0])
  const project = page.getByRole('button', { name: 'Edit media project icon', exact: true })
  const container = page.getByRole('button', { name: 'Edit media-jellyfin-1 container icon', exact: true })
  await project.waitFor()
  await page.mouse.move(0, 0)
  assert.equal(await project.evaluate(el => getComputedStyle(el, '::after').opacity), '0')
  await page.locator('.group-summary').hover({ position: { x: 400, y: 20 } })
  assert.equal(await project.evaluate(el => getComputedStyle(el, '::after').opacity), '0', 'whole row hover must not reveal pencil')
  await project.hover()
  assert.equal(await project.evaluate(el => getComputedStyle(el, '::after').opacity), '1')
  assert.equal(await page.locator('.project-group').evaluate(el => el.open), false)
  await project.click()
  await page.locator('dialog[open]').waitFor()
  assert.equal(await page.locator('.project-group').evaluate(el => el.open), false, 'icon click must not toggle group')
  assert.equal(await page.locator('#icon-url').evaluate(el => el === document.activeElement), true)
  assert.equal(await page.locator('.icon-dialog').evaluate(el => el.getBoundingClientRect().width), 460)
  if (process.env.ICON_DESKTOP_SCREENSHOT) await page.screenshot({ path: process.env.ICON_DESKTOP_SCREENSHOT })
  await page.locator('#icon-url').fill('ftp://example.com/icon.png')
  await page.getByRole('button', { name: 'Download & save', exact: true }).click()
  await wait(() => page.locator('#icon-error').textContent().then(text => text.includes('http://')))
  assert.equal(requests.filter(request => request.method === 'PUT').length, 0)
  await page.keyboard.press('Escape')
  await wait(() => page.locator('dialog').count().then(n => n === 0))
  assert.equal(await project.evaluate(el => el === document.activeElement), true)
  await page.keyboard.press('Enter')
  await page.locator('dialog[open]').waitFor()
  await page.locator('#icon-url').fill('https://example.com/project.png')
  let releaseWrite
  writeGate = new Promise(resolve => { releaseWrite = resolve })
  let releaseRead
  readGate = new Promise(resolve => { releaseRead = resolve })
  await page.keyboard.press('Enter')
  await wait(() => requests.some(request => request.method === 'PUT'))
  assert.equal(await page.locator('.icon-dialog button:enabled').count(), 0, 'all dismissal and mutation buttons locked')
  await page.keyboard.press('Escape')
  assert.equal(await page.locator('dialog[open]').count(), 1)
  await page.mouse.click(4, 4)
  assert.equal(await page.locator('dialog[open]').count(), 1)
  releaseWrite(); writeGate = null
  await wait(() => requests.at(-1)?.method === 'GET')
  assert.equal(await page.locator('dialog[open]').count(), 1, 'must stay open until readback')
  assert.equal(await project.locator('img').getAttribute('src'), '/icons/jellyfin.png', 'no optimistic rendering')
  releaseRead(); readGate = null
  await wait(() => page.locator('dialog').count().then(n => n === 0))
  assert.equal(await project.locator('img').getAttribute('src'), '/api/icons/images/project.png')
  assert.equal(await project.evaluate(el => el === document.activeElement), true)
  await page.locator('.group-summary').click({ position: { x: 400, y: 20 } })
  await container.click()
  assert.equal(await page.locator('.container-detail').evaluate(el => el.open), false, 'container icon does not toggle inspection')
  assert.match(await page.locator('#icon-scope').textContent(), /project icon stays unchanged/)
  assert.equal(await page.locator('#icon-url').inputValue(), '')
  assert.equal(await page.locator('.icon-preview img').getAttribute('src'), '/icons/jellyfin.png')
  await page.locator('#icon-url').fill('https://example.com/container.gif')
  await page.getByRole('button', { name: 'Download & save', exact: true }).click()
  await wait(() => page.locator('dialog').count().then(n => n === 0))
  assert.equal(await container.locator('img').getAttribute('src'), '/api/icons/images/container.png')
  assert.equal(await project.locator('img').getAttribute('src'), '/api/icons/images/project.png')
  containerId = 'recreated-id-2'
  await page.getByRole('button', { name: 'Refresh', exact: false }).click()
  await wait(() => page.locator('.inspection-line code').allTextContents().then(text => text.includes(containerId)))
  assert.equal(await container.locator('img').getAttribute('src'), '/api/icons/images/container.png')
  await container.click()
  assert.equal(await page.locator('#icon-url').inputValue(), 'https://example.com/container.gif')
  failWrite = true
  await page.locator('#icon-url').fill('https://example.com/not-supported.svg')
  await page.getByRole('button', { name: 'Download & save', exact: true }).click()
  await wait(() => page.locator('#icon-error').textContent().then(text => text.includes('Only PNG')))
  assert.equal(await page.locator('#icon-url').inputValue(), 'https://example.com/not-supported.svg')
  assert.equal(await page.locator('.icon-preview img').getAttribute('src'), '/api/icons/images/container.png')
  await page.locator('#icon-url').fill('https://example.com/new.png')
  assert.match(await page.locator('#icon-error').textContent(), /Only PNG/, 'error persists while editing')
  failWrite = false; failReadback = true
  await page.getByRole('button', { name: 'Download & save', exact: true }).click()
  await wait(() => page.locator('#icon-error').textContent().then(text => text.includes('unavailable')))
  assert.equal(await page.locator('dialog[open]').count(), 1)
  assert.equal(await page.locator('#icon-url').inputValue(), 'https://example.com/new.png')
  assert.equal(await container.locator('img').getAttribute('src'), '/api/icons/images/container.png')
  failReadback = false
  await page.getByRole('button', { name: 'Use default icon', exact: true }).click()
  await wait(() => page.locator('dialog').count().then(n => n === 0))
  assert.equal(await container.locator('img').getAttribute('src'), '/icons/jellyfin.png')
  assert.equal(await project.locator('img').getAttribute('src'), '/api/icons/images/project.png')
  assert.ok(requests.some(request => request.method === 'DELETE' && request.url === '/api/icons?kind=container&key=media-jellyfin-1'))
  await page.setViewportSize({ width: 390, height: 700 })
  await project.click()
  await page.locator('dialog[open]').waitFor()
  const bounds = await page.locator('dialog').evaluate(el => ({ width: el.getBoundingClientRect().width, scroll: el.scrollWidth, client: el.clientWidth, height: el.getBoundingClientRect().height }))
  assert.ok(bounds.width <= 390 && bounds.scroll === bounds.client)
  await page.getByRole('button', { name: 'Download & save', exact: true }).focus()
  await page.keyboard.press('Tab')
  assert.equal(await page.locator('dialog').evaluate(el => el.contains(document.activeElement)), true, 'native modal traps focus')
  if (process.env.ICON_SCREENSHOT) await page.screenshot({ path: process.env.ICON_SCREENSHOT })
  await page.keyboard.press('Escape')
  available = false
  await page.reload()
  await page.getByText('No problems detected', { exact: true }).waitFor()
  assert.equal(await page.locator('.icon-button').count(), 0)
  assert.equal(await page.locator('.project-list .service-icon').count(), 2)
  iconFailure = true
  await page.reload()
  await page.getByText('No problems detected', { exact: true }).waitFor()
  assert.equal(await page.locator('.icon-button').count(), 0)
  assert.equal(await page.locator('.project-group').count(), 1)
  // Exercise image loading failure through the real shared renderer.
  await page.route('**/api/icons/images/**', route => route.fulfill({ status: 404 }))
  iconFailure = false; available = true
  await page.reload()
  await project.waitFor()
  await wait(() => project.locator('img').getAttribute('src').then(src => src === '/icons/jellyfin.png'))
  await page.route('**/icons/jellyfin.png', route => route.fulfill({ status: 404 }))
  await page.reload()
  await project.waitFor()
  await wait(() => project.locator('svg[data-brand="stack"]').count().then(n => n === 1))
  assert.deepEqual(errors, [])
  console.log('PASS: hover-only pencil; independent project/container overrides; stable recreation keys; native modal, focus, Enter, Escape, Tab; URL validation; busy dismissal lock; save/readback gating; error preservation; DELETE/readback reset; 390px layout; unavailable/failed icons leave monitor intact.')
  console.log(JSON.stringify({ requests: requests.filter(request => request.method !== 'GET'), mobileBounds: bounds }))
} finally { await browser.close(); await server.close() }
