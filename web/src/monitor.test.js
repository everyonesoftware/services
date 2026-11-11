import test from 'node:test'
import assert from 'node:assert/strict'
import * as monitor from './monitor.ts'

const container = (changes = {}) => ({ id: 'a', name: 'web-1', project: 'site', service: 'web', image: 'nginx:stable', state: 'running', health: 'healthy', monitorStatus: 'healthy', ...changes })
const project = (name, containers, status = 'healthy') => ({ name, containers, status })

test('queued and running updates move only their project from Updates to Needs attention until finished', () => {
  assert.equal(typeof monitor.projectsWithOperations, 'function')
  const site = project('site', [container({ updateAvailable: true })])
  const other = project('other', [container({ updateAvailable: true })])
  for (const state of ['queued', 'running']) {
    const projects = monitor.projectsWithOperations([site, other], [{ id: 'active', project: 'site', state }])
    assert.deepEqual(monitor.selectProjects(projects, '', 'updates').map(p => p.name), ['other'])
    assert.deepEqual(monitor.selectProjects(projects, '', 'attention').map(p => p.name), ['site'])
    assert.equal(monitor.projectHealthStatus(projects[0]), state === 'queued' ? 'queued' : 'updating')
    assert.deepEqual(monitor.availableServices(projects[0]), [])
    assert.equal(monitor.canUpdateProject(projects[0]), false)
    assert.equal(monitor.availableServices(projects[1]).length, 1)
  }
  for (const state of ['complete', 'error']) {
    const projects = monitor.projectsWithOperations([site], [{ id: 'done', project: 'site', state }])
    assert.deepEqual(monitor.selectProjects(projects, '', 'updates'), [site])
    assert.equal(monitor.canUpdateProject(projects[0]), true)
  }
  assert.equal(site.updateState, undefined, 'do not mutate the API snapshot')
  const refreshed = project('site', [container()])
  assert.deepEqual(monitor.selectProjects(monitor.projectsWithOperations([refreshed], []), '', 'updates'), [])
})

test('search covers service, image, name and reasons; filters preserve problem-first ordering without mutating input', () => {
  assert.equal(typeof monitor.selectProjects, 'function')
  const healthy = project('alpha', [container()])
  const updates = project('bravo', [container({ updateAvailable: true })])
  const problem = project('zulu', [container({ monitorStatus: 'unhealthy', monitorReason: 'Probe timeout' })])
  const projects = [healthy, updates, problem]
  assert.deepEqual(monitor.selectProjects(projects, '', 'all').map(p => p.name), ['zulu', 'bravo', 'alpha'])
  assert.deepEqual(monitor.selectProjects(projects, '  PROBE ', 'attention'), [problem])
  assert.deepEqual(monitor.selectProjects(projects, 'nginx', 'updates'), [updates])
  assert.deepEqual(monitor.selectProjects(projects, 'web-1', 'all').length, 3)
  assert.deepEqual(monitor.selectProjects(projects, 'missing', 'all'), [])
  assert.equal(projects[0], healthy)
})

test('available services deduplicate replicas but retain exact digests; standalone and missing Compose metadata cannot update', () => {
  assert.equal(typeof monitor.availableServices, 'function')
  const c = container({ currentDigest: 'sha256:old', proposedDigest: 'sha256:new', updateAvailable: true })
  const site = project('site', [c, { ...c, id: 'b', currentDigest: 'sha256:older' }, container({ service: 'db' })])
  assert.deepEqual(monitor.availableServices(site), [{ service: 'web', image: 'nginx:stable', currentDigests: ['sha256:old', 'sha256:older'], proposedDigests: ['sha256:new'], replicas: 2 }])
  assert.equal(monitor.canUpdateProject(site), true)
  assert.equal(monitor.canUpdateProject(project('Standalone', [c])), false)
  assert.equal(monitor.canUpdateProject(project('site', [{ ...c, service: '' }])), false)
  assert.equal(monitor.canUpdateProject(project('site', [{ ...c, project: '' }])), false)
  assert.equal(monitor.canUpdateProject(project('site', [container()])), false)
})

test('update scope includes every replica of an available service, even replicas not flagged for an update', () => {
  const first = container({ updateAvailable: true, currentDigest: 'old-a', proposedDigest: 'new' })
  const second = container({ id: 'b', updateAvailable: false, currentDigest: 'old-b', proposedDigest: '' })
  const services = monitor.availableServices(project('site', [first, second]))
  assert.deepEqual(services[0].currentDigests, ['old-a', 'old-b'])
  assert.deepEqual(services[0].proposedDigests, ['new'])
  assert.equal(services[0].replicas, 2)
})

test('group status prioritizes failures over unverified replicas and completed one-off tasks are not incidents', () => {
  assert.equal(typeof monitor.projectHealthStatus, 'function')
  const unverified = container({ monitorStatus: 'running', health: '' })
  const complete = container({ monitorStatus: 'completed', state: 'exited' })
  const bad = container({ monitorStatus: 'restart-loop' })
  assert.equal(monitor.containerStatus(unverified), 'running-unverified')
  assert.equal(monitor.needsAttention(project('task', [complete], 'running')), false)
  assert.equal(monitor.projectHealthStatus(project('mixed', [unverified, bad], 'unhealthy')), 'restart-loop')
  assert.equal(monitor.projectHealthStatus(project('task', [complete], 'running')), 'completed')
  const projects = [project('alpha', [unverified], 'running'), project('zulu', [bad], 'unhealthy')]
  assert.deepEqual(monitor.selectProjects(projects, '', 'attention').map(p => p.name), ['zulu'])
  assert.equal(monitor.needsAttention(project('alpha', [unverified], 'running')), false)
})

test('recent events are newest first and capped without mutating the API snapshot', () => {
  assert.equal(typeof monitor.recentEvents, 'function')
  const events = [{ id: 'old', time: '2026-11-07T10:00:00Z' }, { id: 'new', time: '2026-11-07T12:00:00Z' }, { id: 'middle', time: '2026-11-07T11:00:00Z' }]
  assert.deepEqual(monitor.recentEvents(events, 2).map(e => e.id), ['new', 'middle'])
  assert.equal(events[0].id, 'old')
})

test('connection is never healthy before a confirmed fresh monitor response and marks retained data stale', () => {
  assert.equal(typeof monitor.connectionState, 'function')
  const now = Date.parse('2026-11-07T12:00:00Z')
  const data = { connected: true, lastChecked: '2026-11-07T11:59:50Z', error: null }
  assert.equal(monitor.connectionState(null, '', now), 'connecting')
  assert.equal(monitor.connectionState(data, '', now), 'connected')
  assert.equal(monitor.connectionState(data, 'Network failed', now), 'stale')
  assert.equal(monitor.connectionState({ ...data, connected: false }, '', now), 'disconnected')
  assert.equal(monitor.connectionState({ ...data, lastChecked: '2026-11-07T11:58:00Z' }, '', now), 'stale')
  assert.equal(monitor.connectionState({ ...data, lastChecked: null }, '', now), 'stale')
  assert.equal(monitor.connectionState(null, 'Offline', now), 'disconnected')
})

test('attention reflects monitor problems, absent health checks, and stopped containers rather than only Docker health', () => {
  assert.equal(typeof monitor.containerStatus, 'function')
  assert.equal(monitor.containerStatus(container()), 'healthy')
  assert.equal(monitor.containerStatus(container({ monitorStatus: 'unhealthy', monitorReason: 'Restart loop' })), 'unhealthy')
  assert.equal(monitor.containerStatus(container({ monitorStatus: undefined, health: '', state: 'running' })), 'running-unverified')
  assert.equal(monitor.containerStatus(container({ monitorStatus: undefined, state: 'exited' })), 'unhealthy')
  assert.equal(monitor.needsAttention(project('site', [container({ monitorStatus: 'starting' })])), true)
  assert.equal(monitor.needsAttention(project('site', [container()])), false)
})
