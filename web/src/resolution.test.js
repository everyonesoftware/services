import test from 'node:test'
import assert from 'node:assert/strict'
import * as api from './api.ts'
import * as monitor from './monitor.ts'

const model = await import('./resolution.ts').catch(() => ({}))
const old = { id: 'old', name: 'old-web', project: 'site', service: 'web', image: 'nginx:stable', state: 'missing', monitorStatus: 'missing' }
const current = { ...old, id: 'new', name: 'new-web', state: 'running', monitorStatus: 'healthy' }
const incident = { id: 'old', project: 'site', name: 'old-web', service: 'web', status: 'missing', reason: 'Absent', since: '2026-11-11T12:00:00Z' }
const projects = [{ name: 'site', status: 'missing', containers: [old, current] }]

test('replacement evidence is suggested only for one actual same-project same-image nonmissing container', () => {
  assert.equal(typeof model.resolutionEntries, 'function')
  const entries = model.resolutionEntries([incident], projects, [])
  assert.equal(entries[0].previous.image, 'nginx:stable')
  assert.equal(entries[0].suggestion.id, 'new')
  assert.equal(model.resolutionEntries([incident], [{ ...projects[0], containers: [old, current, { ...current, id: 'second' }] }], [])[0].suggestion, undefined)
  assert.equal(model.resolutionEntries([incident], [{ ...projects[0], containers: [old, { ...current, project: 'other' }] }], [])[0].suggestion, undefined)
  assert.equal(model.resolutionEntries([incident], [{ ...projects[0], containers: [old, { ...current, image: 'other' }] }], [])[0].suggestion, undefined)
  assert.equal(model.resolutionEntries([incident], [], [])[0].suggestion, undefined)
  const newerSnapshot = [{ name: 'site', status: 'healthy', containers: [{ ...current, name: old.name, service: old.service }] }]
  assert.equal(model.resolutionEntries([incident], newerSnapshot, [])[0].previous, undefined, 'do not label a newer current container as old disappearance evidence')
  assert.deepEqual(entries[0].events, [])
})

test('evidence uses real retained events and matches empty Compose service fields without inventing observations', () => {
  const standalone = { ...old, project: 'Standalone', service: undefined }
  const i = { ...incident, project: 'Standalone', service: '' }
  const event = { id: 'event', project: 'Standalone', name: old.name, kind: 'problem', time: incident.since, message: 'Actual disappearance' }
  const entries = model.resolutionEntries([i], [{ name: 'Standalone', status: 'unhealthy', containers: [standalone] }], [event, { ...event, id: 'sibling', name: 'other' }])
  assert.equal(entries[0].previous.image, old.image)
  assert.deepEqual(entries[0].events, [event])
  assert.equal(entries[0].suggestion, undefined)
})

test('replacement choices exclude containers outside the missing service project', () => {
  const other = { ...current, id: 'unrelated', project: 'other-project' }
  const entries = model.resolutionEntries([incident], [...projects, { name: 'other-project', status: 'healthy', containers: [other] }], [])
  assert.deepEqual(entries[0].candidates.map(c => c.id), ['new'])
  const drafts = model.applyResolutionAction(model.initialResolutionDrafts(entries), 'replace')
  drafts.old.replacementId = 'unrelated'
  assert.equal(model.validateResolution(entries, drafts).valid, false)
})

test('bulk actions affect included drafts only, per-item overrides submit exact scoped decisions and reject duplicates', () => {
  assert.equal(typeof model.initialResolutionDrafts, 'function')
  const second = { ...incident, id: 'other', name: 'other-old' }
  const entries = model.resolutionEntries([incident, second], projects, [])
  let drafts = model.initialResolutionDrafts(entries)
  assert.equal(model.validateResolution(entries, drafts).valid, false)
  drafts.other.included = false
  drafts = model.applyResolutionAction(drafts, 'forget')
  assert.equal(drafts.other.action, '')
  assert.deepEqual(model.validateResolution(entries, drafts).decisions, [{ incidentId: 'old', since: incident.since, action: 'forget' }])
  drafts.other.included = true
  drafts = model.applyResolutionAction(drafts, 'replace')
  drafts.old.replacementId = 'new'
  drafts.other.replacementId = 'new'
  assert.match(model.validateResolution(entries, drafts).errors.old, /different replacement/)
  drafts.other.action = 'snooze'
  drafts.other.snoozeSeconds = 900
  const result = model.validateResolution(entries, drafts)
  assert.equal(result.valid, true)
  assert.deepEqual(result.decisions, [{ incidentId: 'old', since: incident.since, action: 'replace', replacementId: 'new' }, { incidentId: 'other', since: incident.since, action: 'snooze', snoozeSeconds: 900 }])
  drafts.old.included = false
  drafts.other.included = false
  assert.equal(model.validateResolution(entries, drafts).valid, false)
})

test('oversized bulk selections explain the server limit before submission', () => {
  const incidents = Array.from({ length: 101 }, (_, index) => ({ ...incident, id: `missing-${index}` }))
  const entries = model.resolutionEntries(incidents, projects, [])
  const drafts = model.applyResolutionAction(model.initialResolutionDrafts(entries), 'forget')
  const result = model.validateResolution(entries, drafts)
  assert.equal(result.valid, false)
  assert.match(result.errors.selection, /at most 100/)
  drafts['missing-100'].included = false
  assert.equal(model.validateResolution(entries, drafts).valid, true)
})

test('snoozed resolution preserves the exact current record version in every decision', () => {
  const snoozedUntil = '2026-11-11T19:00:00.123456789Z'
  const entries = model.resolutionEntries([{ ...incident, snoozedUntil }], projects, [])
  for (const action of ['forget', 'replace', 'snooze']) {
    const drafts = model.applyResolutionAction(model.initialResolutionDrafts(entries), action)
    assert.equal(model.validateResolution(entries, drafts).decisions[0].snoozedUntil, snoozedUntil)
  }
})

test('resolution API sends exact decisions and server Undo token; failures never succeed', async () => {
  assert.equal(typeof api.resolveIncidents, 'function')
  const decisions = [{ incidentId: 'old', since: incident.since, action: 'replace', replacementId: 'new' }]
  const requests = []
  const fetcher = async (url, options) => { requests.push([url, options.method, JSON.parse(options.body)]); return url.endsWith('/undo') ? new Response(null, { status: 204 }) : Response.json({ undoToken: 'token-1' }) }
  assert.deepEqual(await api.resolveIncidents(decisions, fetcher), { undoToken: 'token-1' })
  await api.undoResolution('token-1', fetcher)
  assert.deepEqual(requests, [['/api/monitor/resolve', 'POST', { decisions }], ['/api/monitor/undo', 'POST', { token: 'token-1' }]])
  await assert.rejects(api.resolveIncidents(decisions, async () => new Response('Incident changed', { status: 409 })), /Incident changed/)
  await assert.rejects(api.resolveIncidents(decisions, async () => Response.json({})), /Undo token/i)
})

test('resolution deadlines bound stalled transport and response bodies without claiming cancellation', { timeout: 1000 }, async () => {
  let signal
  const fetcher = async (_url, options) => { signal = options.signal; return new Promise(() => {}) }
  await assert.rejects(api.resolveIncidents([], fetcher, 20), /timed out.*unconfirmed/i)
  assert.equal(signal.aborted, true)
  await assert.rejects(api.resolveIncidents([], async () => ({ ok: true, status: 200, json: () => new Promise(() => {}) }), 20), /timed out.*unconfirmed/i)
  await assert.rejects(api.undoResolution('token', fetcher, 20), /timed out.*unconfirmed/i)
})

test('snoozed missing remains missing but drops out of active attention, never hiding sibling failures', () => {
  const snoozed = { ...old, monitorSnoozedUntil: '2099-01-01T00:00:00Z' }
  const p = { ...projects[0], containers: [snoozed, current] }
  assert.equal(monitor.containerStatus(snoozed), 'missing')
  assert.equal(monitor.needsAttention(p), false)
  assert.deepEqual(monitor.selectProjects([p], '', 'attention'), [])
  assert.equal(monitor.needsAttention({ ...p, containers: [snoozed, { ...current, monitorStatus: 'unhealthy' }] }), true)
  assert.equal(monitor.needsAttention({ ...p, containers: [{ ...snoozed, monitorSnoozedUntil: '2000-01-01T00:00:00Z' }] }), true)
})
