import test from 'node:test'
import assert from 'node:assert/strict'

const implementation = await import('./webAppUpdate.ts').catch(() => ({}))
const current = '0123456789abcdefabcd'

test('web build checks use the frontend metadata only with an uncached same-origin GET', async () => {
  assert.equal(typeof implementation.readWebBuild, 'function')
  const calls = []
  const id = await implementation.readWebBuild(async (...args) => {
    calls.push(args)
    return new Response(JSON.stringify({ id: current }), { headers:{'Content-Type':'application/json'} })
  })
  assert.equal(id, current)
  assert.equal(calls.length, 1)
  assert.equal(calls[0][0], '/web-build.json')
  assert.equal(calls[0][1].method, 'GET')
  assert.equal(calls[0][1].cache, 'no-store')
  assert.equal(calls[0][1].credentials, 'same-origin')
  assert.ok(calls[0][1].signal instanceof AbortSignal)
})

test('HTML, invalid build identities and HTTP failures cannot claim a current web app', async () => {
  assert.equal(typeof implementation.readWebBuild, 'function')
  for (const body of ['<html>Sign in</html>', '{}', 'null', '[]', '{"id":"latest"}', '{"id":"0123456789ABCDEFABCD"}']) {
    await assert.rejects(implementation.readWebBuild(async () => new Response(body)), /build|response/i)
  }
  await assert.rejects(implementation.readWebBuild(async () => new Response('{}', {status:503})), /503/)
})

test('deadlines bound stalled web-build transport and response reads and abort work', async () => {
  assert.equal(typeof implementation.readWebBuild, 'function')
  let signal
  await assert.rejects(implementation.readWebBuild(async (_, options) => { signal=options.signal; return new Promise(()=>{}) }, {timeoutMs:15}), /timed out/i)
  assert.equal(signal.aborted, true)
  await assert.rejects(implementation.readWebBuild(async () => ({ok:true,json:()=>new Promise(()=>{})}), {timeoutMs:15}), /timed out/i)
})

test('cancelling a check settles it even when transport ignores cancellation', async () => {
  assert.equal(typeof implementation.readWebBuild, 'function')
  const controller = new AbortController()
  const promise = implementation.readWebBuild(async () => new Promise(()=>{}), {signal:controller.signal})
  controller.abort()
  await assert.rejects(promise, /cancelled/i)
})

test('an already-cancelled check never starts a metadata request', async () => {
  const controller = new AbortController()
  controller.abort()
  let requests = 0
  await assert.rejects(implementation.readWebBuild(async () => { requests++; return new Response('{}') }, {signal:controller.signal}), /cancelled/i)
  assert.equal(requests, 0)
})

test('current build comes from the loaded HTML identity, not the latest server response', () => {
  assert.equal(typeof implementation.currentWebBuild, 'function')
  assert.equal(implementation.currentWebBuild({querySelector: selector => {
    assert.equal(selector, 'meta[name="services-build"]')
    return {getAttribute:()=>current}
  }}), current)
  assert.equal(implementation.currentWebBuild({querySelector:()=>null}), null)
  assert.equal(implementation.currentWebBuild({querySelector:()=>({getAttribute:()=>'<invalid>'})}), null)
})

test('reload completion is acknowledged only by the new document matching the saved target', () => {
  assert.equal(typeof implementation.rememberWebReload, 'function')
  assert.equal(typeof implementation.webReloadCompleted, 'function')
  assert.equal(typeof implementation.consumeWebReload, 'function')
  const saved = new Map()
  const storage = {setItem:(key,value)=>saved.set(key,value),getItem:key=>saved.get(key)??null,removeItem:key=>saved.delete(key)}
  implementation.rememberWebReload(storage, current)
  assert.equal(implementation.webReloadCompleted(storage, current), true)
  assert.equal(implementation.webReloadCompleted(storage, current), true, 'Reading initial state does not consume the notice')
  assert.equal(implementation.consumeWebReload(storage, '11111111111111111111'), false)
  implementation.rememberWebReload(storage, current)
  assert.equal(implementation.consumeWebReload(storage, current), true)
  assert.equal(implementation.consumeWebReload(storage, current), false, 'Success is one-time, not a persistent claim')
  const blocked = {setItem:()=>{throw new Error('Blocked')},getItem:()=>{throw new Error('Blocked')},removeItem:()=>{throw new Error('Blocked')}}
  assert.doesNotThrow(()=>implementation.rememberWebReload(blocked,current))
  assert.equal(implementation.consumeWebReload(blocked,current),false)
})
