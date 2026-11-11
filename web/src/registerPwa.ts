export async function registerPwa(serviceWorker: Pick<ServiceWorkerContainer, 'register'>) {
  try {
    await serviceWorker.register('/sw.js', { scope: '/', updateViaCache: 'none' })
  } catch (error) {
    // Installation is optional; network-only Services remains fully usable.
    console.warn('Services offline support could not be registered.', error)
  }
}
