// Retirement endpoint for browsers that still have the former Services worker.
// No fetch handler, offline fallback, client claiming, or automatic navigation.
self.addEventListener('install', event => {
  event.waitUntil(self.skipWaiting())
})
self.addEventListener('activate', event => {
  event.waitUntil((async () => {
    if (self.registration.scope !== `${self.location.origin}/`) return
    try {
      const keys = await caches.keys()
      await Promise.allSettled(keys.filter(key => key.startsWith('services-offline-')).map(key => caches.delete(key)))
    } finally {
      await self.registration.unregister()
    }
  })())
})
