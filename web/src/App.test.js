import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createServer } from 'vite'

const server = await createServer({ server: { middlewareMode: true }, appType: 'custom' })
after(() => server.close())
const components = await server.ssrLoadModule('/src/App.tsx')
const fixture = { name: 'site', status: 'unhealthy', containers: [{ id: 'web', name: 'site-web-1', project: 'site', service: 'web', image: 'nginx:stable', state: 'running', health: 'unhealthy', monitorStatus: 'unhealthy', monitorReason: 'Probe failed', restartCount: 4, healthOutput: 'Connection refused <script>', updateAvailable: true, currentDigest: 'sha256:old', proposedDigest: 'sha256:new' }] }

test('expandable groups expose reasons, health output and restart details; updates use a group review action', () => {
  assert.equal(typeof components.ProjectGroup, 'function')
  const html = renderToStaticMarkup(createElement(components.ProjectGroup, { project: fixture, onReview: () => {}, actionsDisabled: false }))
  assert.match(html, /<details[^>]*open=""/)
  assert.match(html, /Probe failed/)
  assert.match(html, /Connection refused &lt;script&gt;/)
  assert.match(html, /Restarts/)
  assert.match(html, />4</)
  assert.match(html, /Review 1 update/)
  assert.match(html, /Health output/)
  const standalone = renderToStaticMarkup(createElement(components.ProjectGroup, { project: { ...fixture, name: 'Standalone' }, onReview: () => {}, actionsDisabled: false }))
  assert.match(standalone, /disabled=""/)
  assert.match(standalone, /Compose configuration/)
})

test('update review names exact services and digests with downtime warning, native dialog and explicit confirmation', () => {
  assert.equal(typeof components.UpdateDialog, 'function')
  const html = renderToStaticMarkup(createElement(components.UpdateDialog, { project: fixture, onClose: () => {}, onUpdated: () => {} }))
  assert.match(html, /<dialog/)
  assert.match(html, /aria-labelledby="update-title"/)
  assert.match(html, /sha256:old/)
  assert.match(html, /sha256:new/)
  assert.match(html, /downtime/i)
  assert.match(html, /selected services/i)
  assert.match(html, /network-sharing dependents may also be recreated/i)
  assert.match(html, /intentionally stopped services stay stopped/i)
  assert.doesNotMatch(html, /Other services are not selected/)
  assert.match(html, /Confirm &amp; update/)
  assert.match(html, />Cancel</)
  assert.doesNotMatch(html, /updated successfully/)
})

test('busy engine review offers queuing and explains fresh execution-time validation', () => {
  const html = renderToStaticMarkup(createElement(components.UpdateDialog, { project: fixture, onClose: () => {}, onUpdated: () => {}, engineBusy: true }))
  assert.match(html, /Confirm &amp; queue/)
  assert.match(html, /re-checked before/i)
  assert.match(html, /close this dialog/i)
})

test('Docker activity distinguishes running work, FIFO queue, results and stale visibility', () => {
  assert.equal(typeof components.DockerActivity, 'function')
  const operation = { id: 'one', project: 'site', action: 'update project', state: 'running', queuedAt: '2026-11-08T12:00:00Z', startedAt: '2026-11-08T12:00:01Z', messages: ['Pulling <image>'] }
  const operations = [operation, { ...operation, id: 'two', project: 'media', state: 'queued', queuePosition: 1 }, { ...operation, id: 'three', project: 'proxy', state: 'error', error: 'Probe failed', finishedAt: '2026-11-08T12:01:00Z' }]
  const html = renderToStaticMarkup(createElement(components.DockerActivity, { operations, loading: false, error: '' }))
  assert.match(html, /Docker activity/)
  assert.match(html, /Running/)
  assert.match(html, /Queued #1/)
  assert.match(html, /Probe failed/)
  assert.match(html, /Pulling &lt;image&gt;/)
  assert.match(html, /Services-managed Compose actions/)
  const stale = renderToStaticMarkup(createElement(components.DockerActivity, { operations, loading: false, error: 'Network failed' }))
  assert.match(stale, /Activity unavailable/)
  assert.match(stale, /Last observed/)
  const loading = renderToStaticMarkup(createElement(components.DockerActivity, { operations: [], loading: true, error: '' }))
  assert.doesNotMatch(loading, /No Compose work/)
})

test('durable activity explains retention and identifies interrupted operations without claiming success', () => {
  const operation = { id: 'persisted-id', project: 'site', action: 'update project', state: 'error', phase: 'interrupted', queuedAt: '2026-11-08T12:00:00Z', messages: ['Last recorded output'], error: 'Execution outcome could not be confirmed' }
  const html = renderToStaticMarkup(createElement(components.DockerActivity, { operations: [operation], loading: false, error: '' }))
  assert.match(html, /Interrupted/)
  assert.match(html, /Execution outcome could not be confirmed/)
  assert.match(html, /Queue and recent output survive Services restarts/)
  assert.doesNotMatch(html, /reset when Services restarts|Completed/)
})

test('self-update disconnect preserves the operation and explains automatic reconnection honestly', () => {
  const operation = { id: 'self-update-id', project: 'custom-services', action: 'update project', state: 'running', phase: 'verifying', selfUpdate: true, queuedAt: '2026-11-08T12:00:00Z', messages: ['Waiting for replacement readiness'] }
  const html = renderToStaticMarkup(createElement(components.DockerActivity, { operations: [operation], loading: false, error: 'Failed to fetch' }))
  assert.match(html, /Services is restarting or temporarily unreachable/)
  assert.match(html, /Reconnecting automatically/)
  assert.match(html, /Waiting for replacement readiness/)
  assert.match(html, /Verifying/)
  assert.doesNotMatch(html, /Completed|update succeeded/i)
})

test('update review explains durable independent execution without promising automatic retry', () => {
  const html = renderToStaticMarkup(createElement(components.UpdateDialog, { project: fixture, onClose: () => {}, onUpdated: () => {} }))
  assert.match(html, /survive Services restarts/)
  assert.match(html, /independent worker/)
  assert.doesNotMatch(html, /held in memory|lost if Services restarts|automatically retried/)
})

test('interrupted results keep the queue pause and acknowledgement visible outside collapsed history', () => {
  const operation = { id: 'interrupted-id', project: 'site', action: 'update project', state: 'error', phase: 'interrupted', needsAttention: true, queuedAt: '2026-11-08T12:00:00Z', messages: [], error: 'Outcome unknown' }
  const html = renderToStaticMarkup(createElement(components.DockerActivity, { operations: [operation], loading: false, error: '' }))
  assert.match(html, /Queue paused/)
  assert.match(html, /Acknowledge interruption/)
  assert.match(html, /Check the project in Docker before acknowledging/)
  assert.ok(html.indexOf('Queue paused') < html.indexOf('operation-history'))
  const stale = renderToStaticMarkup(createElement(components.DockerActivity, { operations: [operation], loading: false, error: 'Offline' }))
  assert.match(stale, /disabled=""[^>]*>Acknowledge interruption/)
})

test('monitor keeps secondary information collapsed and omits promotional copy', () => {
  const html = renderToStaticMarkup(createElement(components.default))
  assert.match(html, /<details class="monitor-details"><summary>Monitor details<\/summary>/)
  assert.match(html, /<details class="activity"[^>]*><summary[^>]*>Recent activity/)
  assert.doesNotMatch(html, /YOUR INFRASTRUCTURE|Service health, changes|Problems first|A quieter view/)
})

test('healthy rows avoid redundant health prose and duplicate container names', () => {
  const container = { ...fixture.containers[0], name: 'web', health: 'healthy', monitorStatus: 'healthy', monitorReason: 'Health check passing', updateAvailable: false }
  const html = renderToStaticMarkup(createElement(components.ProjectGroup, { project: { name: 'site', containers: [container] }, onReview: () => {}, actionsDisabled: false }))
  const summary = html.match(/<summary class="container-summary">([\s\S]*?)<\/summary>/)[1]
  assert.doesNotMatch(summary, /Health check passing/)
  assert.equal((summary.match(/>web</g) || []).length, 1)
  assert.match(summary, /nginx:stable/)
  assert.match(summary, /Healthy/)
})

test('project and container names have local decorative brand icons and unknowns have neutral icons', () => {
  const container = { ...fixture.containers[0], image: 'tailscale/tailscale:stable', service: 'vpn' }
  const render = (image) => renderToStaticMarkup(createElement(components.ProjectGroup, { project: { ...fixture, name: 'my-vpn', containers: [{ ...container, image }] }, onReview() {}, actionsDisabled: false }))
  const html = render(container.image)
  assert.equal((html.match(/data-brand="tailscale"/g) || []).length, 2)
  for (const name of ['my-vpn', 'vpn']) {
    assert.match(html, new RegExp(`class="identity-name"><svg[^>]*aria-hidden="true"[^>]*>[\\s\\S]*?</svg><span>${name}</span>`))
  }
  assert.doesNotMatch(html, /<img|https?:\/\//)
  const unknown = render('custom/unknown:latest')
  assert.match(unknown, /data-brand="stack"/)
  assert.match(unknown, /data-brand="container"/)
  assert.match(unknown, /site-web-1/)
})

test('brand and fallback SVG viewports leave breathing room around the complete artwork', () => {
  for (const image of ['tailscale/tailscale:stable', 'lscr.io/linuxserver/qbittorrent:latest', 'custom/unknown:latest']) {
    const html = renderToStaticMarkup(createElement(components.ProjectGroup, { project: { ...fixture, containers: [{ ...fixture.containers[0], image }] }, onReview() {}, actionsDisabled: false }))
    const icons = [...html.matchAll(/<svg class="service-icon[^>]*viewBox="([^"]+)"/g)]
    assert.equal(icons.length, 2)
    for (const [, viewBox] of icons) {
      const [x, y, width, height] = viewBox.split(' ').map(Number)
      assert.ok(x <= -3 && y <= -3 && x + width >= 27 && y + height >= 27, `${image}: ${viewBox} must surround the 0–24 artwork with a safe inset`)
      assert.equal(width, height)
    }
  }
})

test('monitor starts honestly connecting, with accessible search and filters, not a connected claim or fabricated counts', () => {
  const html = renderToStaticMarkup(createElement(components.default))
  assert.match(html, /<h1>Monitor<\/h1>/)
  assert.match(html, /Connecting/)
  assert.match(html, /Search services/)
  assert.match(html, /Needs attention/)
  assert.match(html, /Check for updates/)
  assert.doesNotMatch(html, /Engine connected|All systems healthy|LIVE/)
  assert.match(html, /Loading services/)
  assert.match(html, /Recent activity/)
})
