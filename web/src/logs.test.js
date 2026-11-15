import test from 'node:test'
import assert from 'node:assert/strict'

test('bounded log windows preserve opaque order, dedupe and permit unlimited older paging', async () => {
  const { mergeLogEntries, selectLogEntries, logDownload } = await import('./logs.ts')
  const row = (id, extra = {}) => ({ id: String(id), timestamp: '2026-11-15T00:00:00.000000001Z', containerId: 'exact/id', container: 'worker', stream: 'stderr', message: `message ${id}`, ...extra })
  const existing = [row(3), row(4), row(5)]
  assert.deepEqual(mergeLogEntries(existing, [row(2), row(3)], 'older', 3), { entries: [row(2), row(3), row(4)], trimmed: true })
  assert.deepEqual(mergeLogEntries(existing, [row(5), row(6)], 'newer', 3), { entries: [row(4), row(5), row(6)], trimmed: true })
  assert.deepEqual(mergeLogEntries(existing, [row(3)], 'older', 3), { entries: existing, trimmed: false })
  const large = Array.from({ length: 20 }, (_, index) => row(index, { message: 'x'.repeat(1024 * 1024) }))
  const bounded = mergeLogEntries([], large, 'newer')
  assert.equal(bounded.trimmed, true, 'Buffer also bounds text size, not only line count')
  assert.ok(bounded.entries.reduce((size, entry) => size + entry.message.length, 0) <= 8 * 1024 * 1024)
  let window = existing
  for (let n = 2; n > -30; n--) window = mergeLogEntries(window, [row(n)], 'older', 3).entries
  assert.equal(window[0].id, '-29', 'Cap is a movable window, not a history limit')
  const entries = [row(1), row(2, { stream: 'tty', containerId: 'other' }), row(3, { message: '<script> literal' })]
  assert.deepEqual(selectLogEntries(entries, { query: ' MESSAGE ', stream: 'stderr' }), [entries[0]])
  assert.deepEqual(selectLogEntries(entries, { source: 'other' }), [entries[1]])
  assert.deepEqual(selectLogEntries(entries, { query: 'no match' }), [])
  assert.match(logDownload([entries[2]]), /000000001Z worker stderr <script> literal/)
})

test('log requests preserve exact scope and opaque cursors and surface failures', async () => {
  const { fetchLogs } = await import('./logs.ts')
  const calls = []
  const signal = new AbortController().signal
  const payload = { entries: [], hasMore: false, nextAfter: 'opaque+/=' }
  const fetcher = async (url, options) => { calls.push({ url: new URL(url, 'http://fixture'), options }); return new Response(JSON.stringify(payload)) }
  assert.deepEqual(await fetchLogs({ project: 'a + b', container: 'exact/id', cursor: 'older+/=' }, signal, fetcher), payload)
  assert.deepEqual(Object.fromEntries(calls[0].url.searchParams), { project: 'a + b', container: 'exact/id', limit: '200', cursor: 'older+/=' })
  assert.equal(calls[0].options.signal, signal)
  await fetchLogs({ project: 'all', after: 'new+/=' }, signal, fetcher)
  assert.equal(calls[1].url.searchParams.get('after'), 'new+/=')
  assert.equal(calls[1].url.searchParams.has('container'), false)
  await assert.rejects(fetchLogs({ project: 'a', cursor: 'x', after: 'y' }, signal, fetcher), /mutually exclusive/)
  await assert.rejects(fetchLogs({ project: 'a' }, signal, async () => new Response('Engine unavailable', { status: 503 })), /Engine unavailable/)
  await assert.rejects(fetchLogs({ project: 'a' }, signal, async () => new Response('{}')), /Invalid logs response/)
  for (const status of [400, 409]) await assert.rejects(fetchLogs({ project: 'a', after: 'expired' }, signal, async () => new Response('Log history changed; reload recent logs', { status })), error => error.name === 'LogCursorError')
  const largeEntry = { id: 'large', timestamp: '2026-11-15T00:00:00Z', containerId: 'one', container: 'one', stream: 'stdout', message: 'x'.repeat(9 * 1024 * 1024) }
  await assert.rejects(fetchLogs({ project: 'a' }, signal, async () => new Response(JSON.stringify({ entries: [largeEntry], hasMore: false }))), /viewer safety limit/)
})
