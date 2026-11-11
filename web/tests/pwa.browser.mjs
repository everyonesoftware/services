// Production-build browser check with an isolated signed-out API fixture.
// No live Services server, Docker socket, credentials, or external mutations.
import assert from 'node:assert/strict'
import { mkdir, mkdtemp, rm } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'
import { preview } from 'vite'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const screenshots = process.env.PWA_SCREENSHOT_DIR
if (screenshots) await mkdir(screenshots, { recursive: true })
const mutations = []
const server = await preview({
  configFile: false,
  root: fileURLToPath(new URL('../', import.meta.url)),
  preview: { host: '127.0.0.1', port: 0 },
  plugins: [{ name: 'signed-out-pwa-fixture', configurePreviewServer(server) {
    server.middlewares.use((req, res, next) => {
      if (!req.url.startsWith('/api')) return next()
      if (!['GET', 'HEAD'].includes(req.method)) mutations.push(`${req.method} ${req.url}`)
      res.setHeader('Content-Type', 'application/json')
      res.setHeader('Cache-Control', 'no-store')
      res.statusCode = req.url === '/api/auth/status' ? 200 : 401
      res.end(JSON.stringify(req.url === '/api/auth/status' ? { setupRequired: false } : { error: 'Not signed in' }))
    })
  } }],
})
let context
const profiles = []
try {
  for (const [name, viewport] of [['desktop', { width: 1280, height: 800 }], ['phone', { width: 390, height: 844 }]]) {
    const profile = await mkdtemp(join(process.env.TMPDIR || tmpdir(), 'services-pwa-browser-'))
    profiles.push(profile)
    context = await chromium.launchPersistentContext(profile, { headless: true, viewport, serviceWorkers: 'allow', ...(process.env.PLAYWRIGHT_CHANNEL ? { channel: process.env.PLAYWRIGHT_CHANNEL } : {}) })
    const page = await context.newPage()
    const errors = []
    page.on('pageerror', error => errors.push(error.message))
    const origin = server.resolvedUrls.local[0]
    await page.goto(origin)
    await page.getByRole('button', { name: 'Sign in', exact: true }).waitFor()
    await page.evaluate(async () => {
      await navigator.serviceWorker.ready
      if (!navigator.serviceWorker.controller) await new Promise(resolve => navigator.serviceWorker.addEventListener('controllerchange', resolve, { once: true }))
    })
    const cdp = await context.newCDPSession(page)
    const manifest = await cdp.send('Page.getAppManifest')
    assert.deepEqual(manifest.errors, [])
    assert.equal(JSON.parse(manifest.data).display, 'standalone')
    const installability = await cdp.send('Page.getInstallabilityErrors')
    assert.deepEqual(installability.installabilityErrors, [], 'Chromium installability checks')
    const cached = await page.evaluate(async () => {
      const keys = await caches.keys()
      return Promise.all(keys.map(async key => ({ key, paths: (await (await caches.open(key)).keys()).map(request => new URL(request.url).pathname) })))
    })
    assert.equal(cached.length, 1)
    assert.match(cached[0].key, /^services-offline-/)
    assert.deepEqual(cached[0].paths, ['/offline.html'])
    await context.setOffline(true)
    await page.reload()
    await page.getByRole('heading', { name: 'Services is offline' }).waitFor()
    assert.match(await page.locator('body').innerText(), /No actions are queued while offline/)
    const networkOnly = await page.evaluate(async () => {
      const results = []
      for (const [path, method] of [['/api/auth/me', 'GET'], ['/api/projects', 'GET'], ['/api/updates/check', 'POST']]) {
        try { await fetch(path, { method }); results.push(false) } catch { results.push(true) }
      }
      return results
    })
    assert.deepEqual(networkOnly, [true, true, true], 'offline API reads and mutations must fail, not return fallback HTML')
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
    await page.getByRole('button', { name: 'Try again' }).focus()
    assert.equal(await page.evaluate(() => document.activeElement.matches(':focus-visible')), true)
    if (screenshots) await page.screenshot({ path: join(screenshots, `pwa-offline-${name}.png`) })
    await context.setOffline(false)
    await page.getByRole('button', { name: 'Try again' }).click()
    await page.getByRole('button', { name: 'Sign in', exact: true }).waitFor()
    assert.deepEqual(errors, [])
    console.log(`${name}: production worker active; manifest/installability valid; offline navigation, network-only APIs, keyboard retry and reconnect passed`)
    await context.close()
  }
  assert.deepEqual(mutations, [], 'no queued actions replay after reconnect')
} finally {
  await context?.close()
  await new Promise(resolve => server.httpServer.close(resolve))
  for (const profile of profiles) await rm(profile, { recursive: true, force: true })
}
