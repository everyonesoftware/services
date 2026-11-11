import test from 'node:test'
import assert from 'node:assert/strict'

const operation = (changes = {}) => ({ id: 'op', project: 'site', action: 'update', state: 'complete', queuedAt: '2026-11-11T10:00:00Z', startedAt: '2026-11-11T10:01:00Z', finishedAt: '2026-11-11T10:02:00Z', messages: [], ...changes })
const now = Date.parse('2026-11-11T10:10:00Z')
const event = (changes = {}) => ({ id: 'event', project: 'site', name: 'web', kind: 'healthy', message: 'Recovered', time: '2026-11-11T10:03:00Z', ...changes })

const options = changes => ({ application: '', kind: 'all', query: '', sort: 'newest', ...changes })
const ids = result => result.entries.map(entry => entry.id)

test('all four sorts are deterministic across kinds; time unknowns are last and duration unknowns counted', async () => {
  const { buildActivity, selectActivity } = await import('./activity.ts')
  const entries = buildActivity([
    operation({ id: 'short' }),
    operation({ id: 'long', startedAt: operation().queuedAt }),
    operation({ id: 'running', state: 'running', finishedAt: undefined }),
    operation({ id: 'queued', state: 'queued', startedAt: undefined, finishedAt: undefined }),
    operation({ id: 'unknown', queuedAt: '', startedAt: '', finishedAt: '' }),
  ], [event({ id: 'latest' }), event({ id: 'tie', time: operation().finishedAt }), event({ id: 'unknown', time: 'invalid' })])
  const expected = {
    newest: ['event:latest', 'event:tie', 'operation:long', 'operation:short', 'operation:running', 'operation:queued', 'event:unknown', 'operation:unknown'],
    oldest: ['operation:queued', 'operation:running', 'event:tie', 'operation:long', 'operation:short', 'event:latest', 'event:unknown', 'operation:unknown'],
    longest: ['operation:running', 'operation:long', 'operation:short'],
    shortest: ['operation:short', 'operation:long', 'operation:running'],
  }
  for (const [sort, ordered] of Object.entries(expected)) {
    const result = selectActivity(entries, options({ sort }), now)
    assert.deepEqual(ids(result), ordered, sort)
    assert.equal(result.excluded, ['longest', 'shortest'].includes(sort) ? 5 : 0)
    assert.deepEqual(selectActivity([...entries].reverse(), options({ sort }), now), result)
  }
  const tied = buildActivity([
    operation({ id: 'b' }), operation({ id: 'a' }),
    operation({ id: 'new', startedAt: '2026-11-11T10:02:00Z', finishedAt: '2026-11-11T10:03:00Z' }),
  ], [])
  for (const sort of ['longest', 'shortest']) assert.deepEqual(ids(selectActivity(tied, options({ sort }), now)), ['operation:new', 'operation:a', 'operation:b'])
})

test('selection combines exact application, kind and trimmed case-insensitive literal search without dropping states', async () => {
  const { buildActivity, selectActivity, activityApplications } = await import('./activity.ts')
  const entries = buildActivity([
    operation({ id: 'all', project: 'all', action: 'restart' }),
    operation({ id: 'special', project: 'a+b [prod]', error: 'Registry Denied', messages: ['Layer Download'] }),
    ...['queued', 'running', 'complete', 'error'].map(state => operation({ id: state, state })),
    operation({ id: 'global', project: '' }),
  ], [event({ id: 'special', project: 'a+b [prod]', name: 'database', kind: 'unhealthy', message: 'Probe TIMEOUT' }), event({ id: 'global', project: '' })])
  assert.deepEqual(activityApplications(entries), ['a+b [prod]', 'all', 'site'])
  assert.equal(selectActivity(entries, options(), now).entries.length, entries.length)
  assert.deepEqual(ids(selectActivity(entries, options({ application: 'all' }), now)), ['operation:all'])
  assert.equal(selectActivity(entries, options({ application: 'ALL' }), now).entries.length, 0)
  assert.equal(selectActivity(entries, options({ application: 'a+b [prod]' }), now).entries.length, 2)
  assert.deepEqual(ids(selectActivity(entries, options({ application: 'a+b [prod]', kind: 'operations', query: '  dEnIeD  ' }), now)), ['operation:special'])
  for (const query of ['database', 'UNHEALTHY', 'timeout']) assert.deepEqual(ids(selectActivity(entries, options({ kind: 'events', query }), now)), ['event:special'])
  for (const query of ['Layer Download', 'registry', 'a+b [prod]']) assert.equal(selectActivity(entries, options({ kind: 'operations', query }), now).entries.length, 1)
  assert.deepEqual(ids(selectActivity(entries, options({ query: 'RESTART' }), now)), ['operation:all'])
  assert.deepEqual(ids(selectActivity(entries, options({ query: 'no match' }), now)), [])
  assert.equal(selectActivity(entries, options({ kind: 'operations', application: 'site' }), now).entries.length, 4)
  const duration = selectActivity(entries, options({ kind: 'events', application: 'a+b [prod]', sort: 'longest' }), now)
  assert.deepEqual(duration, { entries: [], excluded: 1 })
  assert.deepEqual(activityApplications([]), [])
  assert.deepEqual(selectActivity([], options(), now), { entries: [], excluded: 0 })
})

test('pure helpers preserve frozen API records, messages, entries and selection options', async () => {
  const { buildActivity, selectActivity, activityApplications, activityRuntime } = await import('./activity.ts')
  const op = Object.freeze(operation({ messages: Object.freeze(['Waiting', 'Completed']) }))
  const ev = Object.freeze(event())
  const operations = Object.freeze([op, op])
  const events = Object.freeze([ev, ev])
  const before = JSON.stringify({ operations, events })
  const entries = Object.freeze(buildActivity(operations, events).map(Object.freeze))
  for (const sort of ['newest', 'oldest', 'longest', 'shortest']) {
    const selection = Object.freeze(options({ sort }))
    const result = selectActivity(entries, selection, now)
    assert.notEqual(result.entries, entries)
    assert.ok(result.entries.every(entry => entries.includes(entry)))
    assert.equal(selection.sort, sort)
  }
  assert.deepEqual(activityApplications(entries), ['site'])
  assert.equal(activityRuntime(entries.find(entry => entry.kind === 'operation'), now), 60_000)
  assert.equal(JSON.stringify({ operations, events }), before)
  assert.equal(entries.find(entry => entry.kind === 'operation').operation, op)
  assert.equal(entries.find(entry => entry.kind === 'event').event, ev)
})

test('global application identity remains empty, while application names are preserved exactly', async () => {
  const { buildActivity, activityApplications, selectActivity } = await import('./activity.ts')
  const entries = buildActivity([
    operation({ id: 'empty', project: '' }), operation({ id: 'missing', project: undefined }),
    operation({ id: 'spaced', project: ' all ' }), operation({ id: 'literal', project: 'all' }),
  ], [event({ project: undefined })])
  assert.equal(entries.filter(entry => entry.application === '').length, 3)
  assert.deepEqual(activityApplications(entries), [' all ', 'all'])
  assert.deepEqual(ids(selectActivity(entries, options({ application: ' all ' }), now)), ['operation:spaced'])
})

test('runtime is execution-only for valid complete/error and actively running operations', async () => {
  const { buildActivity, activityRuntime } = await import('./activity.ts')
  const runtime = changes => activityRuntime(buildActivity([operation(changes)], [])[0], now)
  for (const state of ['complete', 'error']) assert.equal(runtime({ state }), 60_000)
  assert.equal(runtime({ state: 'running', finishedAt: undefined }), 540_000)
  assert.equal(runtime({ state: 'running', finishedAt: 'invalid' }), null)
  assert.equal(runtime({ finishedAt: operation().startedAt }), 0)
  for (const changes of [
    { state: 'queued' }, { phase: 'interrupted' }, { needsAttention: true },
    { startedAt: undefined }, { startedAt: '' }, { startedAt: 'invalid' },
    { finishedAt: undefined }, { finishedAt: 'invalid' },
    { finishedAt: operation().queuedAt },
    { state: 'running', startedAt: '2026-11-11T10:11:00Z', finishedAt: undefined },
    { startedAt: '2026-11-11T10:11:00Z', finishedAt: '2026-11-11T10:12:00Z' },
  ]) assert.equal(runtime(changes), null, JSON.stringify(changes))
  assert.equal(activityRuntime(buildActivity([], [event()])[0], now), null)
  assert.equal(activityRuntime(buildActivity([operation()], [])[0], NaN), null)
})

test('buildActivity unifies operations and events with distinct prefixed identities and original records', async () => {
  const { buildActivity } = await import('./activity.ts')
  const op = operation({ id: 'shared' })
  const ev = event({ id: 'shared', project: '' })
  assert.deepEqual(buildActivity([op], [ev]), [
    { id: 'operation:shared', kind: 'operation', application: 'site', timestamp: Date.parse(op.finishedAt), operation: op },
    { id: 'event:shared', kind: 'event', application: '', timestamp: Date.parse(ev.time), event: ev },
  ])
})

test('timestamps use the first valid finish/start/queue date and unknown events stay null', async () => {
  const { buildActivity } = await import('./activity.ts')
  for (const [changes, expected] of [
    [{ finishedAt: 'invalid' }, Date.parse(operation().startedAt)],
    [{ finishedAt: '', startedAt: 'invalid' }, Date.parse(operation().queuedAt)],
    [{ finishedAt: undefined, startedAt: undefined, queuedAt: undefined }, null],
    [{ finishedAt: 'invalid', startedAt: 'invalid', queuedAt: 'invalid' }, null],
  ]) assert.equal(buildActivity([operation(changes)], [])[0].timestamp, expected)
  for (const time of ['', undefined, 'invalid']) assert.equal(buildActivity([], [event({ time })])[0].timestamp, null)
})

test('deduplicates within each kind, retains cross-kind IDs, and chooses newest duplicate independent of input order', async () => {
  const { buildActivity } = await import('./activity.ts')
  const older = operation({ id: 'same', finishedAt: '2026-11-11T10:01:30Z' })
  const newer = operation({ id: 'same' })
  const ops = [older, operation({ id: 'a' }), newer, newer]
  const events = [event({ id: 'same' }), event({ id: 'same' })]
  const result = buildActivity(ops, events)
  assert.equal(result.length, 3)
  assert.equal(result.find(e => e.id === 'operation:same').operation, newer)
  assert.deepEqual(result, buildActivity([...ops].reverse(), [...events].reverse()))
  const tieA = operation({ id: 'tie', error: 'a' })
  const tieB = operation({ id: 'tie', error: 'b' })
  assert.deepEqual(buildActivity([tieA, tieB], []), buildActivity([tieB, tieA], []))
})
