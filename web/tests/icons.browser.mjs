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
page.on('console', message => { if (message.type() === 'error' && message.text().includes('controlled input')) errors.push(message.text()) })
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
  if (url.pathname === '/api/auth/status') return route.fulfill({ json: { setupRequired: false } })
  if (url.pathname === '/api/auth/me') return route.fulfill({ json: { user: { id: 'fixture-admin', username: 'Browser fixture', role: 'admin' } } })
  if (url.pathname.startsWith('/api/icons/images/')) return route.fulfill({ path: fileURLToPath(new URL('../public/icons/jellyfin.png', import.meta.url)), contentType: 'image/png' })
  const upload = url.pathname === '/api/icons/upload'
  if (url.pathname === '/api/icons' || upload) {
    requests.push({ method: request.method(), url: url.pathname + url.search, body: upload ? request.postDataBuffer() : request.postDataJSON(), contentType: request.headers()['content-type'] })
    if (request.method() === 'GET') {
      if (readGate) await readGate
      if (iconFailure || failReadback) return route.fulfill({ status: 503, body: 'Icon storage unavailable' })
      return route.fulfill({ json: { available, icons } })
    }
    if (writeGate) await writeGate
    if (failWrite) return route.fulfill({ status: 415, body: 'Unsupported SVG feature. Your displayed icon has been kept.' })
    const target = request.method() === 'PUT' && !upload ? request.postDataJSON() : { kind: url.searchParams.get('kind'), key: url.searchParams.get('key') }
    icons = icons.filter(icon => icon.kind !== target.kind || icon.key !== target.key)
    if (request.method() === 'DELETE') return route.fulfill({ status: 204 })
    const icon = { kind: target.kind, key: target.key, url: `/api/icons/images/${target.kind}.png`, sourceUrl: upload ? '' : target.url }
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
  await page.locator('#icon-url').fill('https://example.com/unsupported-features.svg')
  await page.getByRole('button', { name: 'Download & save', exact: true }).click()
  await wait(() => page.locator('#icon-error').textContent().then(text => text.includes('Unsupported SVG feature')))
  assert.equal(await page.locator('#icon-url').inputValue(), 'https://example.com/unsupported-features.svg')
  assert.equal(await page.locator('.icon-preview img').getAttribute('src'), '/api/icons/images/container.png')
  await page.locator('#icon-url').fill('https://example.com/new.png')
  assert.match(await page.locator('#icon-error').textContent(), /Unsupported SVG feature/, 'error persists while editing')
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
  // Upload fixture: file selection is local until explicit save, including source switches.
  await project.click()
  await page.getByRole('button', { name: 'Upload image', exact: true }).click()
  assert.equal(await page.getByRole('button', { name: 'Upload image', exact: true }).getAttribute('aria-pressed'), 'true')
  const mutations = () => requests.filter(request => request.method !== 'GET').length
  const beforeUpload = mutations()
  await page.getByRole('button', { name: 'Upload & save', exact: true }).click()
  await wait(() => page.locator('#icon-error').textContent().then(text => text.includes('Choose')))
  const fixture = fileURLToPath(new URL('../public/icons/jellyfin.png', import.meta.url))
  assert.equal(await page.locator('#icon-file').getAttribute('accept'), 'image/png,image/jpeg,image/gif,image/svg+xml,.svg')
  assert.match(await page.locator('#icon-help').textContent(), /SVGs are validated and converted to PNG; scripts, external resources and unsupported SVG features are rejected/)
  for (const [name, mimeType] of [['bad.webp', 'image/webp'], ['bad.pdf', 'application/pdf']]) {
    await page.locator('#icon-file').setInputFiles({ name, mimeType, buffer: Buffer.from('unsupported') })
    await page.getByRole('button', { name: 'Upload & save', exact: true }).click()
    await wait(() => page.locator('#icon-error').textContent().then(text => text.includes('PNG, JPEG, GIF or SVG')))
  }
  await page.locator('#icon-file').setInputFiles({ name: 'huge.png', mimeType: 'image/png', buffer: Buffer.alloc(2 * 1024 * 1024 + 1) })
  await page.getByRole('button', { name: 'Upload & save', exact: true }).click()
  await wait(() => page.locator('#icon-error').textContent().then(text => text.includes('2 MiB')))
  assert.equal(mutations(), beforeUpload)
  await page.locator('#icon-file').setInputFiles(fixture)
  await page.getByRole('button', { name: 'Image URL', exact: true }).click()
  assert.equal(await page.locator('#icon-url').inputValue(), 'https://example.com/project.png')
  await page.getByRole('button', { name: 'Upload image', exact: true }).click()
  assert.match(await page.locator('#icon-filename').textContent(), /jellyfin.png/)
  assert.equal(await page.locator('#icon-file').evaluate(el => el.files[0]?.name), 'jellyfin.png')
  assert.equal(mutations(), beforeUpload)
  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  assert.equal(mutations(), beforeUpload)
  await project.click()
  await page.getByRole('button', { name: 'Upload image', exact: true }).click()
  const svg = { name: 'benign.svg', mimeType: 'image/svg+xml', buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32"><rect width="32" height="32" fill="#336699"/></svg>') }
  await page.locator('#icon-file').setInputFiles(svg)
  assert.equal(mutations(), beforeUpload, 'SVG selection is not an upload')
  assert.equal(await page.locator('.icon-preview img').getAttribute('src'), '/api/icons/images/project.png', 'never preview raw selected SVG')
  failWrite = true
  if (process.env.ICON_UPLOAD_DESKTOP_SCREENSHOT) {
    await page.setViewportSize({ width: 1200, height: 900 })
    await page.screenshot({ path: process.env.ICON_UPLOAD_DESKTOP_SCREENSHOT })
    await page.setViewportSize({ width: 390, height: 700 })
  }
  if (process.env.ICON_UPLOAD_SCREENSHOT) await page.screenshot({ path: process.env.ICON_UPLOAD_SCREENSHOT })
  await page.getByRole('button', { name: 'Upload & save', exact: true }).click()
  await wait(() => page.locator('#icon-error').textContent().then(text => text.includes('Unsupported SVG feature')))
  assert.match(await page.locator('#icon-filename').textContent(), /benign.svg/)
  assert.equal(await page.locator('.icon-preview img').getAttribute('src'), '/api/icons/images/project.png')
  assert.equal(await project.locator('img').getAttribute('src'), '/api/icons/images/project.png', 'SVG rejection preserves displayed icon')
  failWrite = false; failReadback = true
  await page.getByRole('button', { name: 'Upload & save', exact: true }).click()
  await wait(() => page.locator('#icon-error').textContent().then(text => text.includes('unavailable')))
  assert.match(await page.locator('#icon-filename').textContent(), /benign.svg/)
  failReadback = false
  writeGate = new Promise(resolve => { releaseWrite = resolve })
  readGate = new Promise(resolve => { releaseRead = resolve })
  await page.getByRole('button', { name: 'Upload & save', exact: true }).click()
  await page.getByRole('button', { name: 'Uploading…', exact: true }).waitFor()
  assert.equal(await page.locator('.icon-dialog button:enabled, .icon-dialog input:enabled').count(), 0)
  await page.keyboard.press('Escape')
  assert.equal(await page.locator('dialog[open]').count(), 1)
  const uploaded = requests.filter(request => request.url.startsWith('/api/icons/upload')).at(-1)
  assert.equal(uploaded.url, '/api/icons/upload?kind=project&key=media')
  assert.equal(uploaded.contentType, 'image/svg+xml')
  assert.deepEqual(uploaded.body, svg.buffer, 'send the original SVG bytes to server validation, not a browser conversion')
  releaseWrite(); writeGate = null
  await wait(() => requests.at(-1)?.method === 'GET')
  assert.equal(await page.locator('dialog[open]').count(), 1)
  releaseRead(); readGate = null
  await wait(() => page.locator('dialog').count().then(n => n === 0))
  await project.click()
  await page.locator('#icon-file').waitFor()
  assert.equal(await page.locator('#icon-file').inputValue(), '')
  assert.match(await page.locator('#icon-filename').textContent(), /No file selected/)
  assert.equal(await page.locator('#icon-file').evaluate(el => document.activeElement === el), true)
  await page.getByRole('button', { name: 'Upload & save', exact: true }).focus()
  await page.keyboard.press('Tab')
  assert.equal(await page.getByRole('button', { name: 'Image URL', exact: true }).evaluate(el => document.activeElement === el), true)
  await page.keyboard.press('Shift+Tab')
  assert.equal(await page.getByRole('button', { name: 'Upload & save', exact: true }).evaluate(el => document.activeElement === el), true)
  const uploadBounds = await page.locator('dialog').evaluate(el => ({ width: el.getBoundingClientRect().width, scroll: el.scrollWidth, client: el.clientWidth }))
  assert.ok(uploadBounds.width <= 390 && uploadBounds.scroll === uploadBounds.client)
  await page.getByRole('button', { name: 'Use default icon', exact: true }).click()
  await wait(() => page.locator('dialog').count().then(n => n === 0))
  assert.equal(await project.locator('img').getAttribute('src'), '/icons/jellyfin.png')
  // Keep a stored icon to exercise the existing image-load fallback checks below.
  icons = [{ kind: 'project', key: 'media', url: '/api/icons/images/project.png', sourceUrl: '' }]
  available = false
  await page.reload()
  await page.locator('.project-group').waitFor()
  assert.equal(await page.locator('.icon-button').count(), 0)
  assert.equal(await page.locator('.project-list .service-icon').count(), 2)
  iconFailure = true
  await page.reload()
  await page.locator('.project-group').waitFor()
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
  console.log('PASS: URL and raw SVG upload without local SVG preview; WebP/PDF/size rejection; source switching preserves selection; selection/cancel sends no mutation; uploaded-source reopening; hover-only pencil; independent overrides; native modal/focus/keyboard; busy lock; save/readback gating; SVG rejection preserves current icon; reset; 390px fit; disconnected monitoring.')
  console.log(JSON.stringify({ requests: requests.filter(request => request.method !== 'GET').map(request => ({ ...request, body: Buffer.isBuffer(request.body) ? `${request.body.length} raw bytes` : request.body })), mobileBounds: bounds, uploadBounds }))
} finally { await browser.close(); await server.close() }
