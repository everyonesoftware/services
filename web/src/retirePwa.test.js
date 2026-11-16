import test from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import vm from 'node:vm'

const read = path => readFileSync(new URL(path, import.meta.url), 'utf8')

test('browser entry has no install metadata or worker registration', () => {
  assert.doesNotMatch(read('../index.html'), /manifest|apple-touch-icon|apple-mobile-web-app/)
  assert.doesNotMatch(read('./main.tsx'), /registerPwa|\.register\(/)
  for (const path of ['../public/manifest.webmanifest', '../public/offline.html', '../public/pwa']) {
    assert.equal(existsSync(new URL(path, import.meta.url)), false, `${path} removed`)
  }
})

test('startup retires only Services root workers and offline caches', async () => {
  const { retirePwa } = await import('./retirePwa.ts')
  const removed = [], deleted = []
  const origin = 'https://services.test'
  const registration = (scope, scriptURL, name, state = 'active') => ({ scope, [state]: { scriptURL }, unregister: async () => { removed.push(name); return true } })
  const workers = { getRegistrations: async () => [
    registration(`${origin}/`, `${origin}/sw.js`, 'services'),
    registration(`${origin}/`, `${origin}/sw.js`, 'waiting', 'waiting'),
    registration(`${origin}/other/`, `${origin}/sw.js`, 'other-scope'),
    registration(`${origin}/`, `${origin}/another-worker.js`, 'other-script'),
  ] }
  const caches = { keys: async () => ['services-offline-old', 'services-offline-new', 'another-cache'], delete: async key => { deleted.push(key); return true } }
  await retirePwa(workers, caches, origin)
  assert.deepEqual(removed, ['services', 'waiting'])
  assert.deepEqual(deleted, ['services-offline-old', 'services-offline-new'])
  assert.match(read('./main.tsx'), /retirePwa\(/)
})

test('retirement failures do not prevent startup or independent cleanup', async () => {
  const { retirePwa } = await import('./retirePwa.ts')
  const deleted = []
  await assert.doesNotReject(retirePwa({ getRegistrations: async () => { throw Error('blocked') } }, {
    keys: async () => ['services-offline-old'], delete: async key => { deleted.push(key); return true },
  }, 'https://services.test'))
  assert.deepEqual(deleted, ['services-offline-old'])
  await assert.doesNotReject(retirePwa(undefined, { keys: async () => { throw Error('blocked') } }, 'https://services.test'))
})

test('old worker URL only retires itself and Services offline caches', async () => {
  const source = read('../public/sw.js')
  const handlers = {}, deleted = [], events = []
  vm.runInNewContext(source, {
    self: {
      addEventListener: (type, handler) => { handlers[type] = handler },
      skipWaiting: async () => { events.push('skip') },
      location: { origin: 'https://services.test' },
      registration: { scope: 'https://services.test/', unregister: async () => { events.push('unregister'); return true } },
    },
    caches: { keys: async () => ['services-offline-old', 'unrelated'], delete: async key => { deleted.push(key); return true } },
  })
  let work
  handlers.install({ waitUntil: promise => { work = promise } }); await work
  handlers.activate({ waitUntil: promise => { work = promise } }); await work
  assert.deepEqual(events, ['skip', 'unregister'])
  assert.deepEqual(deleted, ['services-offline-old'])
  assert.equal(handlers.fetch, undefined)
  assert.doesNotMatch(source, /clients\.claim|clients\.navigate|location\.reload/)
})

test('nested old worker URL update preserves its registration and all caches', async () => {
  const handlers = {}, events = []
  vm.runInNewContext(read('../public/sw.js'), {
    self: {
      location: { origin: 'https://services.test' },
      registration: { scope: 'https://services.test/nested/', unregister: async () => { events.push('unregister'); return true } },
      addEventListener: (type, handler) => { handlers[type] = handler },
      skipWaiting: async () => { events.push('skip') },
    },
    caches: {
      keys: async () => { events.push('cache-keys'); return ['services-offline-old', 'services-offline-nested', 'unrelated'] },
      delete: async key => { events.push(`delete:${key}`); return true },
    },
  })
  let work
  handlers.install({ waitUntil: promise => { work = promise } }); await work
  handlers.activate({ waitUntil: promise => { work = promise } }); await work
  assert.deepEqual(events, ['skip'], 'Nested activation must not inspect/delete root-owned caches or unregister')
  assert.equal(handlers.fetch, undefined)
})
