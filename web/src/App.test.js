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
  assert.match(html, /all replicas/i)
  assert.match(html, /Confirm &amp; update/)
  assert.match(html, />Cancel</)
  assert.doesNotMatch(html, /updated successfully/)
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
