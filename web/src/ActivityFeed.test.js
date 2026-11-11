import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createServer } from 'vite'

const server = await createServer({ server: { middlewareMode: true, hmr: false, ws: false }, appType: 'custom' })
after(() => server.close())
const { ActivityFeed } = await server.ssrLoadModule('/src/ActivityFeed.tsx')
const now = Date.parse('2026-11-11T12:00:00Z')
const base = { operations: [], events: [], operationsLoading: false, eventsLoading: false, operationsError: '', eventsError: '', now, onAcknowledged() {} }
const render = props => renderToStaticMarkup(createElement(ActivityFeed, { ...base, ...props }))
const operation = { id: 'fixture-operation', project: '<project>', action: 'update project', state: 'complete', queuedAt: '2026-11-11T11:55:00Z', startedAt: '2026-11-11T11:56:00Z', finishedAt: '2026-11-11T11:57:00Z', messages: ['<script>unsafe output</script>'] }

test('activity distinguishes initial loading, known empty history and unavailable activity', () => {
  assert.match(render({ operationsLoading: true }), /Loading activity…/)
  assert.doesNotMatch(render({ operationsLoading: true }), /No activity yet/)
  assert.match(render({}), /No activity yet/)
  for (const props of [{ operationsError: 'Worker unavailable' }, { eventsError: 'Monitor disconnected' }]) {
    const html = render(props)
    assert.match(html, /Activity unavailable/)
    assert.doesNotMatch(html, /No activity yet/)
    assert.match(html, /current state is unknown/i)
  }
})

test('unified activity escapes both event and operation identities, messages and dropdown choices', () => {
  const html = render({ operations: [operation], events: [{ id: 'fixture-event', project: '<project>', name: '<web>', kind: 'unhealthy', message: '<script>unsafe event</script>', time: '2026-11-11T11:58:00Z' }] })
  assert.doesNotMatch(html, /<script>|<project>|<web>/)
  assert.match(html, /&lt;project&gt;/)
  assert.match(html, /&lt;script&gt;unsafe event&lt;\/script&gt;/)
  assert.match(html, /&lt;script&gt;unsafe output&lt;\/script&gt;/)
  assert.ok(html.indexOf('data-entry-id="event:fixture-event"') < html.indexOf('data-entry-id="operation:fixture-operation"'))
  assert.match(html, /1m 00s/)
  assert.doesNotMatch(html, /activity-filter-summary|More filters|data-state-filter/)
})

test('activity keeps queue recovery notices outside filtering and never calls uncertain work completed', () => {
  const html = render({ operations: [{ ...operation, state: 'error', phase: 'interrupted', needsAttention: true, finishedAt: undefined, error: 'Outcome uncertain' }] })
  assert.match(html, /Queue paused/)
  assert.match(html, /Acknowledge interruption/)
  assert.ok(html.indexOf('Queue paused') < html.indexOf('class="activity-filters"'))
  assert.match(html, /Interrupted/)
  assert.match(html, /Unknown runtime/)
  assert.doesNotMatch(html, />Completed</)
  const stale = render({ operations: [{ ...operation, needsAttention: true }], operationsError: 'Worker disconnected' })
  assert.match(stale, /disabled=""[^>]*>Acknowledge interruption/)
})

test('a self-update outage keeps last observed output and explains reconnecting without claiming success', () => {
  const html = render({ operations: [{ ...operation, state: 'running', phase: 'verifying', selfUpdate: true, finishedAt: undefined }], operationsError: 'Network unavailable' })
  assert.match(html, /Services is restarting or temporarily unreachable/)
  assert.match(html, /Reconnecting automatically/)
  assert.match(html, /Verifying/)
  assert.match(html, /so far/)
  assert.match(html, /unsafe output/)
  assert.doesNotMatch(html, />Completed<|update succeeded/)
})
