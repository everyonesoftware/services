import { createHash } from 'node:crypto'
import { readFile, readdir, writeFile } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

async function buildIdentity(directory) {
  // Strip our previous output before hashing, so regenerating is idempotent.
  const index = (await readFile(join(directory, 'index.html'), 'utf8')).replace(
    /<meta\b[^>]*>/gi,
    tag => /\sname\s*=\s*(?:"services-build"|'services-build'|services-build(?=\s|\/?>))/i.test(tag) ? '' : tag,
  )
  const files = []
  async function collect(relative = '') {
    for (const entry of await readdir(join(directory, relative), { withFileTypes: true })) {
      const name = relative ? `${relative}/${entry.name}` : entry.name
      if (entry.isDirectory()) await collect(name)
      else if (entry.isFile() && (
        /\.(?:js|css)$/.test(name) && name !== 'sw.js'
        || name === 'manifest.webmanifest' || name === 'offline.html'
        || /^(?:icons|pwa)\//.test(name) && /\.(?:png|svg|ico|webp|jpe?g)$/.test(name)
        || !name.includes('/') && /\.(?:png|svg|ico)$/.test(name)
      )) files.push(name)
    }
  }
  await collect()
  const hash = createHash('sha256')
  // Relative POSIX names and length framing avoid platform/path ambiguity.
  const add = (name, bytes) => hash.update(`${name}\0${bytes.length}\0`).update(bytes)
  add('index.html', Buffer.from(index))
  for (const name of files.sort()) add(name, await readFile(join(directory, name)))
  const id = hash.digest('hex').slice(0, 20)
  const meta = `<meta name="services-build" content="${id}">`
  if (!/<\/head\s*>/i.test(index)) throw new Error('Build index.html must contain a closing head tag')
  await writeFile(join(directory, 'index.html'), index.replace(/<\/head\s*>/i, closing => `${meta}${closing}`))
  await writeFile(join(directory, 'web-build.json'), `${JSON.stringify({ id })}\n`)
}

export async function buildPwa(directory) {
  await buildIdentity(directory)
  const offline = await readFile(join(directory, 'offline.html'))
  const version = createHash('sha256').update('services-offline-v1').update(offline).digest('hex').slice(0, 20)
  // Deliberately no app-shell or runtime-data cache: old status must never look live.
  const source = `const CACHE = 'services-offline-${version}'
const OFFLINE = '/offline.html'
self.addEventListener('install', event => {
  event.waitUntil(caches.open(CACHE).then(cache => cache.addAll([OFFLINE])))
})
self.addEventListener('activate', event => {
  event.waitUntil((async () => {
    const keys = await caches.keys()
    await Promise.all(keys.filter(key => key.startsWith('services-offline-') && key !== CACHE).map(key => caches.delete(key)))
    await self.clients.claim()
  })())
})
self.addEventListener('fetch', event => {
  const request = event.request
  const url = new URL(request.url)
  if (request.method !== 'GET' || url.origin !== self.location.origin || url.pathname === '/api' || url.pathname.startsWith('/api/') || request.mode !== 'navigate') return
  event.respondWith(fetch(request).catch(async () => {
    const cached = await (await caches.open(CACHE)).match(OFFLINE)
    return cached || new Response('Services is offline. Reconnect and try again.', { status: 503, headers: { 'Content-Type': 'text/plain; charset=utf-8' } })
  }))
})
`
  await writeFile(join(directory, 'sw.js'), source)
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  await buildPwa(resolve('dist'))
}
