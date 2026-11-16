// Real production build, isolated HTTP/API fixtures and fresh browser contexts.
// Never connects to a live Services server, Docker socket or provider.
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { mkdir, readFile } from 'node:fs/promises'
import { extname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const root = process.env.WEB_DIST_DIR || fileURLToPath(new URL('../dist/', import.meta.url))
const screenshots = process.env.BROWSER_SCREENSHOT_DIR
if (screenshots) await mkdir(screenshots, { recursive: true })
let legacyWorker = false
const mutations = []
const legacyScript = `
self.addEventListener('install', event => event.waitUntil(self.skipWaiting()));
self.addEventListener('activate', event => event.waitUntil(self.clients.claim()));
self.addEventListener('fetch', event => {
  if (event.request.mode === 'navigate') event.respondWith(fetch(event.request).catch(() => caches.match('/offline.html')));
});`
const unrelatedScript = `self.addEventListener('install', event => event.waitUntil(self.skipWaiting()));`
const server = createServer(async (req, res) => {
  const path = new URL(req.url, 'http://localhost').pathname
  res.setHeader('Cache-Control', 'no-store')
  if (path.startsWith('/api/')) {
    if (!['GET', 'HEAD'].includes(req.method)) mutations.push(`${req.method} ${path}`)
    res.setHeader('Content-Type', 'application/json')
    res.statusCode = path === '/api/auth/status' ? 200 : 401
    res.end(JSON.stringify(path === '/api/auth/status' ? { setupRequired: false } : { error: 'Not signed in' }))
    return
  }
  // This document deliberately contains no frontend code: worker retirement must
  // also work when the old registration updates without opening the new app.
  if (path === '/seed.html') {
    res.setHeader('Content-Type', 'text/html')
    res.end('<!doctype html><title>Migration fixture (no app)</title><body>Migration fixture</body>')
    return
  }
  if ((legacyWorker && path === '/sw.js') || ['/unrelated/worker.js', '/other-worker.js'].includes(path)) {
    res.setHeader('Content-Type', 'text/javascript')
    res.setHeader('Service-Worker-Allowed', '/')
    res.end(path === '/sw.js' ? legacyScript : unrelatedScript)
    return
  }
  try {
    assert.ok(!decodeURIComponent(path).split('/').includes('..'))
    const bytes = await readFile(join(root, path === '/' ? 'index.html' : decodeURIComponent(path).slice(1)))
    res.setHeader('Content-Type', ({ '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.json': 'application/json', '.svg': 'image/svg+xml', '.png': 'image/png' })[extname(path)] || (path === '/' ? 'text/html' : 'application/octet-stream'))
    res.end(bytes)
  } catch { res.statusCode = 404; res.end('Not found') }
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const origin = `http://127.0.0.1:${server.address().port}`
const launch = process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : process.env.PLAYWRIGHT_CHANNEL || process.env.BROWSER_CHANNEL ? { channel: process.env.PLAYWRIGHT_CHANNEL || process.env.BROWSER_CHANNEL } : {}
const browser = await chromium.launch({ headless: true, ...launch })
const storage = page => page.evaluate(async () => ({
  workers: (await navigator.serviceWorker.getRegistrations()).map(reg => ({ scope: new URL(reg.scope).pathname, script: new URL((reg.active || reg.waiting || reg.installing).scriptURL).pathname })).sort((a, b) => a.scope.localeCompare(b.scope)),
  caches: (await caches.keys()).sort(),
}))
async function expectStorage(page, expected) {
  // Cleanup is asynchronous; poll its real browser-visible result, never ready
  // (which would hang correctly on a browser-only app).
  let actual
  const deadline = Date.now() + 10000
  do {
    actual = await storage(page)
    if (JSON.stringify(actual) === JSON.stringify(expected)) break
    await new Promise(resolve => setTimeout(resolve, 50))
  } while (Date.now() < deadline)
  assert.deepEqual(actual, expected)
}
async function seed(page, { rootScript = '/sw.js', nested = true } = {}) {
  await page.goto(`${origin}/seed.html`)
  await page.evaluate(async ({ rootScript, nested }) => {
    const register = async (script, scope) => {
      const reg = await navigator.serviceWorker.register(script, { scope, updateViaCache: 'none' })
      if (!reg.active) await new Promise((resolve, reject) => {
        const worker = reg.installing || reg.waiting
        worker.addEventListener('statechange', () => {
          if (worker.state === 'activated') resolve()
          if (worker.state === 'redundant') reject(new Error('Fixture worker became redundant'))
        })
      })
    }
    await register(rootScript, '/')
    await register('/unrelated/worker.js', '/unrelated/')
    if (nested) await register('/sw.js', '/nested/')
    await (await caches.open('services-offline-old')).put('/offline.html', new Response('<h1>Services is offline</h1><p>No actions are queued while offline</p>', { headers: { 'Content-Type': 'text/html' } }))
    await (await caches.open('unrelated-cache')).put('/unrelated-proof', new Response('keep me'))
    await (await caches.open('services-offline')).put('/similar-prefix-proof', new Response('keep this too'))
  }, { rootScript, nested })
}
const preservedCaches = ['services-offline', 'unrelated-cache']
const unrelated = { scope: '/unrelated/', script: '/unrelated/worker.js' }
try {
  for (const [name, viewport] of [['desktop', { width: 1280, height: 800 }], ['phone', { width: 390, height: 844 }]]) {
    const context = await browser.newContext({ viewport, serviceWorkers: 'allow' })
    try {
      const page = await context.newPage(), errors = [], workerEvents = []
      page.setDefaultTimeout(10000)
      page.on('pageerror', error => errors.push(error.message))
      context.on('serviceworker', worker => workerEvents.push(worker.url()))
      await page.goto(origin)
      await page.getByRole('button', { name: 'Sign in', exact: true }).waitFor()
      assert.equal(await page.locator('link[rel~="manifest"], meta[name="apple-mobile-web-app-capable"], meta[name="mobile-web-app-capable"], meta[name="apple-mobile-web-app-status-bar-style"], link[rel="apple-touch-icon"]').count(), 0, 'No manifest or installation metadata')
      for (const path of ['/manifest.webmanifest', '/offline.html', '/pwa/icon-192.png']) {
        const response = await context.request.get(`${origin}${path}`)
        assert.equal(response.status(), 404, `${path} must not ship in the production build`)
      }
      assert.ok(await page.locator('link[rel~="icon"]').count(), 'Normal browser favicon remains')
      const cdp = await context.newCDPSession(page)
      const manifest = await cdp.send('Page.getAppManifest')
      assert.ok(!manifest.data, 'Chromium must not discover an install manifest')
      await expectStorage(page, { workers: [], caches: [] })
      await page.reload()
      await page.getByRole('button', { name: 'Sign in', exact: true }).waitFor()
      await expectStorage(page, { workers: [], caches: [] })
      assert.deepEqual(workerEvents, [], 'No transient worker registrations on a fresh visit or reload')
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'Responsive layout remains')
      if (screenshots) await page.screenshot({ path: join(screenshots, `browser-only-${name}.png`) })
      await context.setOffline(true)
      await assert.rejects(page.goto(`${origin}/?offline-navigation=1`), /net::ERR_(INTERNET_DISCONNECTED|FAILED)/, 'Offline navigation must fail normally, not serve custom HTML')
      assert.equal(await page.getByRole('heading', { name: 'Services is offline', exact: true }).count(), 0)
      await context.setOffline(false)
      await page.goto(origin)
      await page.getByRole('button', { name: 'Sign in', exact: true }).waitFor()
      await expectStorage(page, { workers: [], caches: [] })
      assert.deepEqual(errors, [])
      console.log(`PASS ${name}: online sign-in, favicon/responsive layout, no manifest/workers/caches, normal offline navigation failure and reconnect`)
    } finally { await context.close() }
  }
  // Frontend migration removes only the old root-scope Services worker, even
  // while the old script is still served; nested /sw.js belongs to another app.
  legacyWorker = true
  const migration = await browser.newContext({ serviceWorkers: 'allow' })
  try {
    const page = await migration.newPage()
    page.setDefaultTimeout(10000)
    await seed(page)
    assert.equal((await storage(page)).workers.length, 3, 'Legacy and unrelated workers really installed')
    await page.goto(origin)
    await page.getByRole('button', { name: 'Sign in', exact: true }).waitFor()
    await expectStorage(page, { workers: [{ scope: '/nested/', script: '/sw.js' }, unrelated], caches: preservedCaches })
    assert.equal(await page.evaluate(async () => (await (await caches.open('unrelated-cache')).match('/unrelated-proof')).text()), 'keep me')
    console.log('PASS frontend migration: old root /sw.js and services-offline-* removed; nested worker, unrelated worker/cache and similar cache prefix preserved')
  } finally { await migration.close() }
  const otherRoot = await browser.newContext({ serviceWorkers: 'allow' })
  try {
    const page = await otherRoot.newPage()
    page.setDefaultTimeout(10000)
    await seed(page, { rootScript: '/other-worker.js', nested: false })
    await page.goto(origin)
    await page.getByRole('button', { name: 'Sign in', exact: true }).waitFor()
    await expectStorage(page, { workers: [{ scope: '/', script: '/other-worker.js' }, unrelated], caches: preservedCaches })
    console.log('PASS frontend migration preserves a root-scope worker with a different script URL')
  } finally { await otherRoot.close() }
  const retirement = await browser.newContext({ serviceWorkers: 'allow' })
  try {
    const page = await retirement.newPage()
    page.setDefaultTimeout(10000)
    await seed(page, { nested: false })
    legacyWorker = false
    // Explicit update fetches the actual production /sw.js; no frontend import
    // or navigation is allowed to perform this scenario's cleanup.
    await page.evaluate(async () => {
      const reg = await navigator.serviceWorker.getRegistration('/')
      if (!reg || new URL(reg.scope).pathname !== '/') throw new Error('Missing seeded root worker')
      await reg.update()
    })
    await expectStorage(page, { workers: [unrelated], caches: preservedCaches })
    assert.equal(new URL(page.url()).pathname, '/seed.html', 'Retirement runs without loading frontend')
    assert.equal(await page.title(), 'Migration fixture (no app)')
    assert.equal(await page.evaluate(async () => (await (await caches.open('unrelated-cache')).match('/unrelated-proof')).text()), 'keep me')
    console.log('PASS production /sw.js update self-unregisters and deletes only services-offline-* without frontend cleanup')
  } finally { await retirement.close() }
  legacyWorker = true
  const nestedRetirement = await browser.newContext({ serviceWorkers: 'allow' })
  try {
    const page = await nestedRetirement.newPage()
    page.setDefaultTimeout(10000)
    await seed(page, { rootScript: '/other-worker.js' })
    await page.evaluate(async () => {
      await (await caches.open('services-offline-nested')).put('/nested-proof', new Response('keep nested cache'))
    })
    legacyWorker = false
    // Update the nested registration to the real production script, without
    // loading the frontend or updating the independently owned root worker.
    await page.evaluate(async () => {
      const reg = await navigator.serviceWorker.getRegistration('/nested/')
      if (!reg || new URL(reg.scope).pathname !== '/nested/') throw new Error('Missing seeded nested worker')
      const settled = new Promise(resolve => {
        reg.addEventListener('updatefound', () => {
          const worker = reg.installing
          worker.addEventListener('statechange', () => {
            // A broken retirement script can unregister and become redundant;
            // let the storage assertions expose both destructive effects.
            if (['activated', 'redundant'].includes(worker.state)) resolve()
          })
        }, { once: true })
      })
      await reg.update()
      await settled
    })
    await expectStorage(page, {
      workers: [{ scope: '/', script: '/other-worker.js' }, { scope: '/nested/', script: '/sw.js' }, unrelated],
      caches: ['services-offline', 'services-offline-nested', 'services-offline-old', 'unrelated-cache'],
    })
    assert.equal(await page.evaluate(async () => (await (await caches.open('services-offline-nested')).match('/nested-proof')).text()), 'keep nested cache')
    assert.match(await page.evaluate(async () => (await (await caches.open('services-offline-old')).match('/offline.html')).text()), /Services is offline/)
    assert.equal(new URL(page.url()).pathname, '/seed.html', 'Nested update must not navigate or load frontend')
    assert.equal(await page.title(), 'Migration fixture (no app)')
    console.log('PASS production nested /sw.js update activates without unregistering or cleaning root-owned/nested caches')
  } finally { await nestedRetirement.close() }
  assert.deepEqual(mutations, [], 'Browser fixtures must not send mutation requests')
} finally {
  await browser.close()
  await new Promise(resolve => server.close(resolve))
}
