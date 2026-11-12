import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import * as api from './api.ts'

const target = { kind: 'container', key: 'media / jellyfin &+?#一' }
const icon = { ...target, url: '/api/icons/images/abc.png', sourceUrl: 'https://example.com/icon.png' }
test('uploaded icons send raw file bytes and require exact uploaded readback', async () => {
  const { changeIcon } = await import('./icons.ts')
  const file = new File([new Uint8Array([137, 80, 78, 71])], 'icon.png', { type: 'image/png' })
  const uploaded = { ...icon, sourceUrl: '' }
  const calls = []
  const fetcher = async (url, options) => { calls.push([url, options]); return Response.json(options.method === 'PUT' ? uploaded : { available: true, icons: [uploaded] }) }
  assert.deepEqual(await changeIcon(target, file, fetcher), { available: true, icons: [uploaded] })
  assert.equal(calls[0][0], '/api/icons/upload?kind=container&key=media%20%2F%20jellyfin%20%26%2B%3F%23%E4%B8%80')
  assert.equal(calls[0][1].body, file)
  assert.equal(calls[0][1].headers['Content-Type'], 'image/png')
  assert.deepEqual(calls.map(([, options]) => options.method), ['PUT', 'GET'])
  const untyped = new File(['bytes'], 'icon')
  await api.uploadIcon(target, untyped, async (_url, options) => {
    assert.equal(options.headers['Content-Type'], 'application/octet-stream')
    assert.equal(options.body, untyped)
    assert.ok(options.signal instanceof AbortSignal)
    return Response.json(uploaded)
  })
  for (const wrong of [{ ...uploaded, url: '/api/icons/images/stale.png' }, icon]) {
    await assert.rejects(changeIcon(target, file, async (_url, options) => Response.json(options.method === 'PUT' ? uploaded : { available: true, icons: [wrong] })), /could not be verified/i)
  }
  await assert.rejects(changeIcon(target, file, async (_url, options) => Response.json(options.method === 'PUT' ? icon : { available: true, icons: [icon] })), /could not be verified/i)
  await assert.rejects(changeIcon(target, file, async (_url, options) => Response.json(options.method === 'PUT' ? null : { available: true, icons: [] })), /could not be verified/i)
})
test('icon API lists readiness, saves source URLs and deletes exact encoded stable keys', async () => {
  assert.equal(typeof api.getIcons, 'function')
  const calls = []
  const fetcher = async (url, options) => {
    calls.push([url, options])
    return options.method === 'DELETE' ? new Response(null, { status: 204 }) : Response.json(options.method === 'PUT' ? icon : { available: true, icons: [icon] })
  }
  assert.deepEqual(await api.getIcons(fetcher), { available: true, icons: [icon] })
  assert.deepEqual(await api.saveIcon(target, icon.sourceUrl, fetcher), icon)
  await api.deleteIcon(target, fetcher)
  assert.equal(calls[0][0], '/api/icons')
  assert.equal(calls[0][1].cache, 'no-store')
  assert.equal(calls[1][0], '/api/icons')
  assert.equal(calls[1][1].method, 'PUT')
  assert.equal(calls[1][1].headers['Content-Type'], 'application/json')
  assert.deepEqual(JSON.parse(calls[1][1].body), { ...target, url: icon.sourceUrl })
  assert.equal(calls[2][0], '/api/icons?kind=container&key=media%20%2F%20jellyfin%20%26%2B%3F%23%E4%B8%80')
  assert.ok(calls.every(([, options]) => options.signal instanceof AbortSignal))
})

test('verified icon mutations refresh before success and reject stale or unavailable readback', async () => {
  const icons = await import('./icons.ts')
  const requests = []
  const fetcher = async (url, options) => { requests.push(options.method); return Response.json(options.method === 'PUT' ? icon : { available: true, icons: [icon] }) }
  assert.deepEqual(await icons.changeIcon(target, icon.sourceUrl, fetcher), { available: true, icons: [icon] })
  assert.deepEqual(requests, ['PUT', 'GET'])
  for (const list of [{ available: false, icons: [icon] }, { available: true, icons: [] }, { available: true, icons: [{ ...icon, sourceUrl: 'https://example.com/other.png' }] }]) {
    await assert.rejects(icons.changeIcon(target, icon.sourceUrl, async (_url, options) => Response.json(options.method === 'PUT' ? icon : list)), /could not be verified/i)
  }
  await assert.rejects(icons.changeIcon(target, icon.sourceUrl, async (_url, options) => Response.json(options.method === 'PUT' ? { ...icon, kind: 'project' } : { available: true, icons: [icon] })), /could not be verified/i)
  const resetCalls = []
  assert.deepEqual(await icons.changeIcon(target, null, async (_url, options) => { resetCalls.push(options.method); return options.method === 'DELETE' ? new Response(null, { status: 204 }) : Response.json({ available: true, icons: [] }) }), { available: true, icons: [] })
  assert.deepEqual(resetCalls, ['DELETE', 'GET'])
  await assert.rejects(icons.changeIcon(target, null, async (_url, options) => options.method === 'DELETE' ? new Response(null, { status: 204 }) : Response.json({ available: true, icons: [icon] })), /could not be verified/i)
})

test('file preflight accepts SVG MIME or an untyped SVG filename without bypassing unsupported MIME or size checks', async () => {
  const { imageFileError } = await import('./icons.ts')
  assert.match(imageFileError(null), /Choose/)
  for (const type of ['image/png', 'image/jpeg', 'image/gif', 'image/svg+xml']) {
    assert.equal(imageFileError(new File([new Uint8Array(2 * 1024 * 1024)], 'icon', { type })), '')
  }
  for (const name of ['icon.svg', 'ICON.SVG']) assert.equal(imageFileError(new File(['<svg/>'], name)), '')
  for (const type of ['image/png', 'image/svg+xml', '']) {
    assert.match(imageFileError(new File([new Uint8Array(2 * 1024 * 1024 + 1)], 'large.svg', { type })), /2 MiB/)
  }
  for (const name of ['icon.png', 'icon.svg', 'icon.svg.png']) {
    for (const type of ['image/webp', 'application/pdf', 'text/plain', 'application/octet-stream']) {
      assert.match(imageFileError(new File(['bytes'], name, { type })), /PNG, JPEG, GIF or SVG/)
    }
  }
  for (const name of ['icon.png', 'icon.svg.png', 'icon.svgz', 'icon']) assert.match(imageFileError(new File(['bytes'], name)), /PNG, JPEG, GIF or SVG/)
})

test('image URL validation permits HTTP(S) only without guessing extensions', async () => {
  const { imageUrlError } = await import('./icons.ts')
  for (const url of ['', 'ftp://example.com/icon.png', 'javascript:alert(1)', '/icons/a.png', 'https://user:pass@example.com/a.png']) assert.ok(imageUrlError(url), url)
  for (const url of ['https://example.com/image?id=1', 'http://example.com/a.gif', 'https://example.com/a.JPG']) assert.equal(imageUrlError(url), '')
})

const server = await (await import('vite')).createServer({ server: { middlewareMode: true, hmr: false, ws: false }, appType: 'custom' })
after(() => server.close())

test('compact native icon dialog has scoped heading, local current preview, URL field and explicit actions', async () => {
  const { IconDialog } = await server.ssrLoadModule('/src/IconDialog.tsx')
  const { createElement } = await import('react')
  const { renderToStaticMarkup } = await import('react-dom/server')
  for (const kind of ['project', 'container']) {
    const html = renderToStaticMarkup(createElement(IconDialog, { target: { ...target, kind, current: icon, automatic: { src: '/icons/nginx.png', slug: 'nginx', name: 'Nginx' } }, available: true, onClose() {}, onSaved() {} }))
    assert.match(html, /<dialog[^>]*class="update-dialog icon-dialog"/)
    assert.match(html, /aria-labelledby="icon-title"/)
    assert.match(html, /Edit media \/ jellyfin &amp;\+\?#一 icon/)
    assert.match(html, new RegExp(kind === 'project' ? 'container icons stay unchanged' : 'project icon stays unchanged'))
    assert.match(html, /src="\/api\/icons\/images\/abc.png"/)
    assert.match(html, /type="url"/)
    assert.match(html, /value="https:\/\/example.com\/icon.png"/)
    assert.match(html, /role="alert"/)
    assert.match(html, /PNG, JPEG, GIF or SVG\. SVGs are validated and converted to PNG; scripts, external resources and unsupported SVG features are rejected\./)
    assert.match(html, /accept="image\/png,image\/jpeg,image\/gif,image\/svg\+xml,\.svg"/)
    assert.match(html, /2 MiB.*4 million pixels.*4096/)
    assert.doesNotMatch(html, /SVG is not supported/)
    assert.match(html, /Download &amp; save/)
    assert.match(html, /Use default icon/)
    assert.match(html, />Cancel</)
  }
})

test('App polls icons independently, gates editing on readiness and wires verified readback without hiding monitoring', async () => {
  const { readFileSync } = await import('node:fs')
  const source = readFileSync(new URL('./App.tsx', import.meta.url), 'utf8')
  assert.match(source, /usePolling<IconList>\('\/api\/icons'/)
  assert.match(source, /iconsRequest\.data\?\.available/)
  assert.match(source, /!iconsRequest\.error/)
  assert.match(source, /onEditIcon:\s*setIconTarget/)
  for (const component of ['ProjectGroup', 'ServiceDetail', 'ContainerDetail']) assert.ok(source.includes(`<${component}`) && source.includes('detailProps(project)'), `${component} receives shared verified icon-editing props`)
  assert.match(source, /<IconDialog/)
  assert.match(source, /iconsRequest\.replace\(next\)/)
  assert.doesNotMatch(source, /const error =[^\n]*iconsRequest/)
})

test('icon requests preserve failures and time out even if fetch ignores abort', async () => {
  assert.equal(typeof api.getIcons, 'function')
  for (const request of [f => api.getIcons(f), f => api.saveIcon(target, icon.sourceUrl, f), f => api.uploadIcon(target, new File(['bytes'], 'icon.png', { type: 'image/png' }), f), f => api.deleteIcon(target, f)]) {
    await assert.rejects(request(async () => new Response('Image is too large', { status: 413 })), /Image is too large/)
    await assert.rejects(request(async () => new Response('', { status: 503 })), /503/)
  }
  let signal
  await assert.rejects(api.saveIcon(target, icon.sourceUrl, (_url, options) => { signal = options.signal; return new Promise(() => {}) }, 10), /timed out.*unconfirmed/i)
  assert.equal(signal.aborted, true)
  await assert.rejects(api.getIcons(() => new Promise(() => {}), 10), /timed out/i)
  await assert.rejects(api.deleteIcon(target, () => new Promise(() => {}), 10), /timed out/i)
  await assert.rejects(api.uploadIcon(target, new File(['bytes'], 'icon.png'), () => new Promise(() => {}), 10), /timed out.*unconfirmed/i)
})
