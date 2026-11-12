import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { mkdir, mkdtemp, writeFile, readFile, rm } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import vm from 'node:vm'

const publicFile = path => readFileSync(new URL(`../public/${path}`, import.meta.url))

test('Services has standalone install metadata and real PNG home-screen icons', () => {
  const manifest = JSON.parse(publicFile('manifest.webmanifest'))
  assert.equal(manifest.id, '/')
  assert.equal(manifest.name, 'Services')
  assert.equal(manifest.short_name, 'Services')
  assert.equal(manifest.start_url, '/')
  assert.equal(manifest.scope, '/')
  assert.equal(manifest.display, 'standalone')
  assert.equal(manifest.theme_color, '#23695b')
  for (const size of [192, 512]) {
    const icon = manifest.icons.find(icon => icon.sizes === `${size}x${size}` && icon.purpose === 'any')
    assert.ok(icon, `${size}px install icon`)
    assert.equal(icon.type, 'image/png')
    const png = publicFile(icon.src.slice(1))
    assert.equal(png.subarray(1, 4).toString(), 'PNG')
    assert.equal(png.readUInt32BE(16), size)
    assert.equal(png.readUInt32BE(20), size)
  }
  const maskable = manifest.icons.find(icon => icon.purpose === 'maskable')
  assert.ok(maskable)
  assert.equal(publicFile(maskable.src.slice(1)).readUInt32BE(16), 512)
  const html = readFileSync(new URL('../index.html', import.meta.url), 'utf8')
  assert.match(html, /rel="manifest" href="\/manifest.webmanifest"/)
  assert.match(html, /rel="apple-touch-icon"[^>]*href="\/pwa\/apple-touch-icon.png"/)
  assert.equal(publicFile('pwa/apple-touch-icon.png').readUInt32BE(16), 180)
})

async function buildFixture(directory) {
  await mkdir(join(directory, 'assets'), { recursive: true })
  await writeFile(join(directory, 'index.html'), '<!doctype html><html><head><title>Services</title></head><body><script src="/assets/app.js"></script></body></html>')
  await writeFile(join(directory, 'assets/app.js'), 'console.log("Services")')
  await writeFile(join(directory, 'assets/app.css'), 'body { color: black }')
  await writeFile(join(directory, 'offline.html'), '<h1>Services is offline</h1>')
}

test('production build emits matching build identity metadata and is repeatable', async () => {
  const { buildPwa } = await import('../scripts/build-pwa.mjs')
  const directory = await mkdtemp(join(process.env.TMPDIR || tmpdir(), 'services-build-'))
  try {
    await buildFixture(directory)
    await buildPwa(directory)
    const metadata = await readFile(join(directory, 'web-build.json'), 'utf8')
    const { id } = JSON.parse(metadata)
    assert.match(id, /^[a-f0-9]{20}$/)
    assert.deepEqual(JSON.parse(metadata), { id })
    const html = await readFile(join(directory, 'index.html'), 'utf8')
    assert.match(html, new RegExp(`<meta name="services-build" content="${id}">`))
    assert.equal((html.match(/name="services-build"/g) || []).length, 1)
    await buildPwa(directory)
    assert.equal(await readFile(join(directory, 'web-build.json'), 'utf8'), metadata)
    assert.equal(await readFile(join(directory, 'index.html'), 'utf8'), html)
  } finally { await rm(directory, { recursive: true, force: true }) }
})

test('build identity follows all frontend bytes, not paths or generated artifacts', async () => {
  const { buildPwa } = await import('../scripts/build-pwa.mjs')
  const directories = await Promise.all([1, 2].map(() => mkdtemp(join(process.env.TMPDIR || tmpdir(), 'services-identity-'))))
  const [directory, other] = directories
  const identity = async dir => {
    await buildPwa(dir)
    return JSON.parse(await readFile(join(dir, 'web-build.json'), 'utf8')).id
  }
  try {
    for (const dir of directories) {
      await buildFixture(dir)
      await mkdir(join(dir, 'pwa'))
      await mkdir(join(dir, 'icons'))
      await mkdir(join(dir, 'assets/lazy'))
      const entries = [
        ['manifest.webmanifest', '{"name":"Services"}'],
        ['pwa/icon.png', 'icon bytes'], ['icons/service.svg', '<svg/>'],
        ['assets/lazy/chunk.js', 'export default 1'], ['assets/lazy/chunk.css', '.lazy{}'],
      ]
      for (const [name, bytes] of dir === other ? entries.toReversed() : entries) await writeFile(join(dir, name), bytes)
    }
    const original = await identity(directory)
    assert.equal(await identity(other), original, 'absolute directories and file creation order are excluded')
    const worker = await readFile(join(directory, 'sw.js'), 'utf8')
    for (const name of ['index.html', 'assets/app.js', 'assets/app.css', 'assets/lazy/chunk.js', 'assets/lazy/chunk.css', 'manifest.webmanifest', 'offline.html', 'pwa/icon.png', 'icons/service.svg']) {
      const bytes = await readFile(join(directory, name))
      await writeFile(join(directory, name), Buffer.concat([bytes, Buffer.from('\nchanged')]))
      assert.notEqual(await identity(directory), original, `${name} changes build identity`)
      await writeFile(join(directory, name), bytes)
      assert.equal(await identity(directory), original, `${name} restores build identity`)
    }
    assert.equal(await readFile(join(directory, 'sw.js'), 'utf8'), worker)
    await writeFile(join(directory, 'web-build.json'), '{"id":"stale"}')
    await writeFile(join(directory, 'sw.js'), '// stale worker')
    await writeFile(join(directory, 'assets/app.js.map'), 'ignored sourcemap')
    assert.equal(await identity(directory), original, 'generated artifacts and source maps are excluded')
    let html = await readFile(join(directory, 'index.html'), 'utf8')
    html = html.replace(/<meta name="services-build"[^>]*>/, '<META content="stale" NAME=\'services-build\'><meta name=services-build content=stale>')
    await writeFile(join(directory, 'index.html'), html)
    assert.equal(await identity(directory), original, 'prior meta tags are normalized regardless of attribute order or quoting')
    await writeFile(join(directory, 'index.html'), html.replace('</head>', '<meta data-name="services-build" content="not our tag"></head>'))
    assert.notEqual(await identity(directory), original, 'unrelated data-name metadata remains part of the index')
  } finally { await Promise.all(directories.map(dir => rm(dir, { recursive: true, force: true }))) }
})

test('production worker caches only a public offline screen, never authenticated data or actions', async () => {
  const { buildPwa } = await import('../scripts/build-pwa.mjs')
  const directory = await mkdtemp(join(process.env.TMPDIR || tmpdir(), 'services-pwa-'))
  try {
    await buildFixture(directory)
    await buildPwa(directory)
    const source = await readFile(join(directory, 'sw.js'), 'utf8')
    const handlers = {}, deleted = [], stored = [], claims = []
    let offline = false
    const cache = { addAll: async paths => stored.push(...paths), match: async path => stored.includes(path) ? new Response('Services is offline') : undefined }
    vm.runInNewContext(source, {
      self: { location: { origin: 'https://services.test' }, addEventListener: (type, handler) => { handlers[type] = handler }, clients: { claim: async () => claims.push(true) } },
      caches: { open: async () => cache, keys: async () => ['services-offline-old', 'another-app-cache'], delete: async key => deleted.push(key) },
      URL, Response,
      fetch: async () => { if (offline) throw new TypeError('Network unavailable'); return new Response('fresh', { status: 200 }) },
    })
    let work
    handlers.install({ waitUntil: promise => { work = promise } }); await work
    assert.deepEqual(stored, ['/offline.html'])
    handlers.activate({ waitUntil: promise => { work = promise } }); await work
    assert.deepEqual(deleted, ['services-offline-old'])
    assert.equal(claims.length, 1)
    const request = (path, mode = 'navigate', method = 'GET') => ({ url: new URL(path, 'https://services.test').href, mode, method })
    const dispatch = async req => {
      let response
      handlers.fetch({ request: req, respondWith: promise => { response = promise } })
      return response
    }
    assert.equal(await (await dispatch(request('/'))).text(), 'fresh')
    offline = true
    assert.equal(await (await dispatch(request('/'))).text(), 'Services is offline')
    for (const req of [request('/api/projects'), request('/api/auth/me'), request('/api'), request('/api/projects', 'navigate', 'POST'), request('https://other.test/'), request('/assets/app.js', 'cors'), request('/api/icons/images/private.png', 'cors')]) {
      assert.equal(await dispatch(req), undefined, `must not intercept ${req.method} ${req.url}`)
    }
    await writeFile(join(directory, 'offline.html'), '<h1>New offline screen</h1>')
    await buildPwa(directory)
    assert.notEqual(await readFile(join(directory, 'sw.js'), 'utf8'), source, 'offline changes invalidate the worker/cache')
    assert.doesNotMatch(source, /skipWaiting/, 'updates must not replace a worker mid-session')
  } finally { await rm(directory, { recursive: true, force: true }) }
})

test('registration bypasses HTTP caches and failure does not break startup', async () => {
  const { registerPwa } = await import('./registerPwa.ts')
  const calls = []
  await registerPwa({ register: async (...args) => { calls.push(args) } })
  assert.deepEqual(calls, [['/sw.js', { scope: '/', updateViaCache: 'none' }]])
  const main = readFileSync(new URL('./main.tsx', import.meta.url), 'utf8')
  assert.match(main, /import\.meta\.env\.PROD/)
  assert.match(main, /window\.isSecureContext/)
  assert.match(main, /'serviceWorker' in navigator/)
  assert.match(main, /registerPwa\(navigator\.serviceWorker\)/)
  const warn = console.warn
  const warnings = []
  console.warn = (...args) => warnings.push(args)
  try {
    await assert.doesNotReject(registerPwa({ register: async () => { throw new Error('Unavailable') } }))
    assert.equal(warnings.length, 1)
  } finally { console.warn = warn }
})
