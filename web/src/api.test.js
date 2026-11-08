import test from 'node:test'
import assert from 'node:assert/strict'
import * as api from './api.ts'

const stream = (...parts) => new Response(new ReadableStream({ start(controller) { const encoder = new TextEncoder(); parts.forEach(part => controller.enqueue(encoder.encode(part))); controller.close() } }))

test('queue submission times out without claiming that the server cancelled the operation', { timeout: 500 }, async () => {
  let signal
  const fetcher = async (_url, options) => { signal = options.signal; return new Promise(() => {}) }
  await assert.rejects(api.updateProject('site', fetcher, 20), /timed out.*unconfirmed/i)
  assert.equal(signal.aborted, true)
})

test('stalled request also times out even before response headers', { timeout: 500 }, async () => {
  await assert.rejects(api.updateProject('site', () => new Promise(() => {}), 20), /timed out.*unconfirmed/i)
})

test('image checks accept 204 and preserve server failures; project update uses encoded group API', async () => {
  assert.equal(typeof api.checkForUpdates, 'function')
  const requests = []
  const fetcher = async (url, options) => { requests.push([url, options.method]); return new Response(null, { status: 204 }) }
  await api.checkForUpdates(fetcher)
  assert.deepEqual(requests, [['/api/updates/check', 'POST']])
  await assert.rejects(api.checkForUpdates(async () => new Response('Registry unavailable', { status: 503 })), /Registry unavailable/)
  const operation = { id: 'operation-1', project: 'media server', action: 'update project', state: 'queued', queuedAt: '2026-11-08T12:00:00Z', messages: [], queuePosition: 2 }
  assert.deepEqual(await api.updateProject('media server', async (url, options) => { requests.push([url, options.method]); return Response.json(operation, { status: 202 }) }), operation)
  assert.deepEqual(requests[1], ['/api/projects/media%20server/update', 'POST'])
})

test('queue submission preserves rejections and rejects invalid acceptance data', async () => {
  await assert.rejects(api.updateProject('site', async () => new Response('Queue is full', { status: 429 })), /Queue is full/)
  await assert.rejects(api.updateProject('site', async () => Response.json({ state: 'complete' })), /Invalid operation/)
})

test('explicit errors, HTTP failure and malformed event data never succeed', async () => {
  await assert.rejects(api.consumeUpdateStream(stream('{"type":"error","message":"Compose configuration missing"}\n{"type":"complete","message":"done"}\n'), () => {}), /Compose configuration missing/)
  await assert.rejects(api.consumeUpdateStream(new Response('Update already running', { status: 409 }), () => {}), /Update already running/)
  await assert.rejects(api.consumeUpdateStream(stream('{"type":"mystery","message":"?"}\n'), () => {}), /Invalid update event/)
  await assert.rejects(api.consumeUpdateStream(stream('not json\n'), () => {}), /Invalid update event/)
  await assert.rejects(api.consumeUpdateStream(new Response(null), () => {}), /stream unavailable/i)
})

test('EOF without explicit complete cannot be mistaken for a successful update', async () => {
  await assert.rejects(api.consumeUpdateStream(stream('{"type":"progress","message":"Pulling"}\n'), () => {}), /before completion/i)
  await assert.rejects(api.consumeUpdateStream(stream(''), () => {}), /before completion/i)
})

test('update stream handles fragmented NDJSON and unterminated final complete event', async () => {
  assert.equal(typeof api.consumeUpdateStream, 'function')
  const events = []
  await api.consumeUpdateStream(stream('{"type":"progress","message":"Pull', 'ing image"}\r\n\n{"type":"complete","message":"Recreated"}'), event => events.push(event))
  assert.deepEqual(events.map(event => event.type), ['progress', 'complete'])
  assert.equal(events[0].message, 'Pulling image')
})
