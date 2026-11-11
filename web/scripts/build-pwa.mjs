import { createHash } from 'node:crypto'
import { readFile, writeFile } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

export async function buildPwa(directory) {
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
