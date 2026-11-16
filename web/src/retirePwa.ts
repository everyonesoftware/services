// Migration only: never register a worker or intercept requests.
export async function retirePwa(
  workers: Pick<ServiceWorkerContainer, 'getRegistrations'> | undefined,
  storage: Pick<CacheStorage, 'keys' | 'delete'> | undefined,
  origin: string,
) {
  await Promise.allSettled([
    (async () => {
      if (!workers) return
      const registrations = await workers.getRegistrations()
      await Promise.allSettled(registrations.filter(registration =>
        registration.scope === `${origin}/` &&
        [registration.active, registration.waiting, registration.installing].some(worker => worker?.scriptURL === `${origin}/sw.js`),
      ).map(registration => registration.unregister()))
    })(),
    (async () => {
      if (!storage) return
      const keys = await storage.keys()
      await Promise.allSettled(keys.filter(key => key.startsWith('services-offline-')).map(key => storage.delete(key)))
    })(),
  ])
}
