import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createServer } from 'vite'
import { readFileSync } from 'node:fs'

const server = await createServer({ server: { middlewareMode: true, hmr: false, ws: false }, appType: 'custom' })
after(() => server.close())
const components = await server.ssrLoadModule('/src/App.tsx')
const fixture = { name: 'site', status: 'unhealthy', containers: [{ id: 'web', name: 'site-web-1', project: 'site', service: 'web', image: 'nginx:stable', state: 'running', health: 'unhealthy', monitorStatus: 'unhealthy', monitorReason: 'Probe failed', restartCount: 4, healthOutput: 'Connection refused <script>', updateAvailable: true, currentDigest: 'sha256:old', proposedDigest: 'sha256:new' }] }

test('missing container rows expose one optional Resolve callback without replacing icons or update review', () => {
  const missing = { ...fixture, containers: [{ ...fixture.containers[0], monitorStatus: 'missing', state: 'missing', monitorSnoozedUntil: '2099-01-01T00:00:00Z' }] }
  const props = { project: missing, onReview() {}, actionsDisabled: false, onResolve() {} }
  const html = renderToStaticMarkup(createElement(components.ProjectGroup, props))
  assert.match(html, /Resolve…/)
  assert.match(html, /Missing · Snoozed/)
  assert.match(html, /Review 1 update/)
  assert.match(html, /class="service-icon/)
  assert.doesNotMatch(html, /Investigate/)
  const absent = renderToStaticMarkup(createElement(components.ProjectGroup, { ...props, onResolve: undefined }))
  assert.doesNotMatch(absent, /Resolve…/)
})

test('project and container icons share a light gray circular background without cropping artwork', () => {
  const css = readFileSync(new URL('./App.css', import.meta.url), 'utf8')
  const iconStyle = css.match(/\.service-icon\s*\{([^}]+)\}/)[1]
  assert.match(iconStyle, /background:\s*#eeeeee\s*;/)
  assert.match(iconStyle, /border-radius:\s*50%\s*;/)
  assert.match(iconStyle, /object-fit:\s*contain\s*;/)
  assert.match(iconStyle, /padding:\s*3px\s*;/)
  const html = renderToStaticMarkup(createElement(components.ProjectGroup, { project: fixture, onReview() {}, actionsDisabled: false }))
  assert.equal((html.match(/class="service-icon/g) || []).length, 2)
  assert.equal((html.match(/<img /g) || []).length, 2)
})

test('icon editing uses independent project and stable container-name overrides only when ready', () => {
  const props = { project: fixture, onReview() {}, actionsDisabled: false, onEditIcon() {}, iconsAvailable: true, icons: [
    { kind: 'project', key: 'site', url: '/api/icons/images/project.png', sourceUrl: 'https://example.com/p.png' },
    { kind: 'container', key: 'site-web-1', url: '/api/icons/images/container.png', sourceUrl: 'https://example.com/c.png' },
    { kind: 'container', key: 'web', url: '/api/icons/images/wrong-id.png', sourceUrl: 'https://example.com/wrong.png' },
  ] }
  const html = renderToStaticMarkup(createElement(components.ProjectGroup, props))
  assert.match(html, /src="\/api\/icons\/images\/project.png"/)
  assert.match(html, /src="\/api\/icons\/images\/container.png"/)
  assert.doesNotMatch(html, /wrong-id|https:\/\/example.com/)
  assert.match(html, /<button[^>]*class="icon-button"[^>]*aria-label="Edit site project icon"/)
  assert.match(html, /aria-label="Edit site-web-1 container icon"/)
  assert.equal((html.match(/class="icon-button"/g) || []).length, 2)
  const unavailable = renderToStaticMarkup(createElement(components.ProjectGroup, { ...props, iconsAvailable: false }))
  assert.doesNotMatch(unavailable, /class="icon-button"/)
  assert.match(unavailable, /site-web-1/)
})

test('pencil is hidden except on the icon button hover or keyboard focus, and dialog is compact', () => {
  const css = readFileSync(new URL('./App.css', import.meta.url), 'utf8')
  assert.match(css, /\.icon-button::after\s*\{[^}]*opacity:\s*0/)
  assert.match(css, /\.icon-button:hover::after,\s*\.icon-button:focus-visible::after\s*\{[^}]*opacity:\s*1/)
  assert.doesNotMatch(css, /(?:summary|identity|row)[^{}]*:hover[^{}]*\.icon-button/)
  assert.match(css, /\.icon-dialog\s*\{[^}]*max-width:\s*460px/)
})

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

const publication = {
  schemaVersion: 1, repository: 'ghcr.io/acme/site', tags: ['latest', 'v2.1'], digest: 'sha256:new',
  version: 'v2.1', sourceRepository: 'acme/site', commit: 'a'.repeat(40), ref: 'refs/heads/main',
  runId: '12345', runAttempt: 1, runUrl: 'https://github.com/acme/site/actions/runs/12345',
  publishedAt: '2026-11-09T12:34:56Z',
}
const publicationProject = (value) => ({ ...fixture, containers: [{ ...fixture.containers[0], updatePublication: value }] })

test('refreshing project labels reconciliation and hides stale publication details and actions', () => {
  const project = { ...publicationProject(publication), updateState: 'refreshing' }
  const html = renderToStaticMarkup(createElement(components.ProjectGroup, { project, onReview() {}, actionsDisabled: false }))
  assert.match(html, />Refreshing</)
  assert.doesNotMatch(html, />Updating<|Review 1 update|Publish notification|v2\.1|Workflow run/)
  assert.match(html, /Health output/)
})
const reviewMarkup = (project) => renderToStaticMarkup(createElement(components.UpdateDialog, { project, onClose() {}, onUpdated() {} }))

test('manual update review renders publication provenance, version, publication time and safe GitHub links', () => {
  const html = reviewMarkup(publicationProject(publication))
  assert.match(html, /Publish notification/)
  assert.match(html, /Available version/)
  assert.match(html, />v2\.1</)
  assert.match(html, /Published/)
  assert.match(html, /<time dateTime="2026-11-09T12:34:56Z">/)
  assert.ok(html.includes(new Date(publication.publishedAt).toLocaleString()))
  assert.match(html, /href="https:\/\/github.com\/acme\/site\/commit\/a{40}"[^>]*target="_blank"[^>]*rel="noopener noreferrer"/)
  assert.match(html, /href="https:\/\/github.com\/acme\/site\/actions\/runs\/12345"[^>]*target="_blank"[^>]*rel="noopener noreferrer"/)
  assert.match(html, /Workflow run/)
  assert.match(html, /Confirm &amp; update/)
  assert.match(html, />Cancel</)
})

test('publication links reject untrusted GitHub URLs and escape injected display text', () => {
  for (const runUrl of [
    'javascript:alert(1)', 'https://github.com.evil.test/acme/site/actions/runs/12345',
    'https://github.com@evil.test/acme/site/actions/runs/12345',
    'https://github.com/acme/other/actions/runs/12345', 'https://github.com/acme/site/actions/runs/999',
    'https://github.com/acme/site/actions/runs/12345?next=evil',
    'https://github.com/acme/site/actions/runs/12345#evil',
    'http://github.com/acme/site/actions/runs/12345',
    'https://github.com:443/acme/site/actions/runs/12345',
  ]) {
    const html = reviewMarkup(publicationProject({ ...publication, runUrl, version: '<img src=x onerror=alert(1)>' }))
    assert.doesNotMatch(html, /<a[^>]*>Workflow run/)
    assert.match(html, /&lt;img src=x onerror=alert\(1\)&gt;/)
    assert.doesNotMatch(html, /<img|onerror="/)
  }
  for (const sourceRepository of ['acme/../evil', 'evil.test/path/more', 'acme/site?x', 'acme/%2e%2e', 'acme/..', 'acme\\site', 'acme/site\n']) {
    assert.doesNotMatch(reviewMarkup(publicationProject({ ...publication, sourceRepository })), /<a /)
  }
  assert.doesNotMatch(reviewMarkup(publicationProject({ ...publication, commit: 'not-a-sha<script>' })), /<a[^>]*\/commit\//)
})

test('missing, partial and unsupported publication data keep manual digest review usable', () => {
  for (const value of [undefined, null, {}, { schemaVersion: 99 }, { ...publication, schemaVersion: 2 }, 'unexpected']) {
    const html = reviewMarkup(publicationProject(value))
    assert.doesNotMatch(html, /Publish notification|Invalid Date|href=/)
    assert.match(html, /sha256:new/)
    assert.match(html, /Confirm &amp; update/)
  }
  const partial = reviewMarkup(publicationProject({ schemaVersion: 1, publishedAt: 'invalid', version: { invalid: true } }))
  assert.match(partial, /Publish notification/)
  assert.match(partial, /Not reported/)
  assert.doesNotMatch(partial, /Invalid Date|href=|Available version/)
  const noVersion = reviewMarkup(publicationProject({ ...publication, version: undefined }))
  assert.doesNotMatch(noVersion, /Available version/)
  assert.match(noVersion, /Workflow run/)
})

test('replicas share one publication detail without leaking another service or stale metadata', () => {
  const first = publicationProject(publication).containers[0]
  const project = { ...fixture, containers: [first, { ...first, id: 'replica' }, { ...first, id: 'db', service: 'db', updateAvailable: false, updatePublication: { ...publication, version: 'stale-db-version' } }] }
  const html = reviewMarkup(project)
  assert.equal((html.match(/Publish notification/g) || []).length, 1)
  assert.doesNotMatch(html, /stale-db-version/)
  assert.match(html, /2 replicas/)
})

test('publication details stay in expanded container inspection and leave manual actions unchanged', () => {
  const requests = []
  let reviewed = 0
  let updated = 0
  const originalFetch = globalThis.fetch
  globalThis.fetch = (...args) => { requests.push(args); throw new Error('Rendering must not submit an update') }
  try {
    const project = publicationProject(publication)
    const html = renderToStaticMarkup(createElement(components.ProjectGroup, { project, onReview() { reviewed++ }, actionsDisabled: false }))
    const summary = html.match(/<summary class="container-summary">([\s\S]*?)<\/summary>/)[1]
    assert.doesNotMatch(summary, /Publish notification|v2\.1|Workflow run/)
    assert.match(html, /Publish notification/)
    assert.match(html, /Review 1 update/)
    const dialog = renderToStaticMarkup(createElement(components.UpdateDialog, { project, onClose() {}, onUpdated() { updated++ } }))
    assert.deepEqual([...dialog.matchAll(/<button[^>]*>(.*?)<\/button>/g)].map(match => match[1]), ['Cancel', 'Confirm &amp; update'])
    assert.equal(reviewed, 0)
    assert.equal(updated, 0)
    assert.deepEqual(requests, [])
    assert.doesNotMatch(html + dialog, /auto.?update|automatically (update|install)|registry check/i)
    const fallback = renderToStaticMarkup(createElement(components.ProjectGroup, { project: fixture, onReview() {}, actionsDisabled: false }))
    assert.doesNotMatch(fallback, /Publish notification/)
  } finally { globalThis.fetch = originalFetch }
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

test('monitor omits routine summary and details while retaining its service workspace', () => {
  const html = renderToStaticMarkup(createElement(components.default))
  assert.doesNotMatch(html, /monitor-details|health-banner|page-description/)
  assert.match(html, /<h1[^>]*>Services<\/h1>/)
  assert.match(html, /class="sticky-panel"/)
  assert.match(html, /Service filter: All services/)
  assert.doesNotMatch(html, /docker-activity|activity-summary|Recent activity/)
  assert.doesNotMatch(html, /YOUR INFRASTRUCTURE|Service health, changes|Problems first|A quieter view/)
})

test('Docker activity keeps explanatory copy collapsed without hiding live state or recovery controls', () => {
  const html = renderToStaticMarkup(createElement(components.DockerActivity, { operations: [], loading: false, error: '' }))
  const explanation = html.match(/<details class="activity-help"><summary>How Docker activity works<\/summary>([\s\S]*?)<\/details>/)?.[1]
  assert.ok(explanation, 'secondary explanation belongs in a closed disclosure')
  assert.match(explanation, /Services-managed Compose actions only/)
  assert.match(explanation, /Queue and recent output survive Services restarts/)
  assert.ok(html.indexOf('No Compose work running or queued.') < html.indexOf('activity-help'))
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

test('project and container names have bundled decorative brand images and unknowns have neutral icons', () => {
  const container = { ...fixture.containers[0], image: 'tailscale/tailscale:stable', service: 'vpn' }
  const render = (image) => renderToStaticMarkup(createElement(components.ProjectGroup, { project: { ...fixture, name: 'my-vpn', containers: [{ ...container, image }] }, onReview() {}, actionsDisabled: false }))
  const html = render(container.image)
  assert.equal((html.match(/data-brand="tailscale"/g) || []).length, 2)
  for (const name of ['my-vpn', 'vpn']) {
    assert.match(html, new RegExp(`class="identity-name"><img[^>]*aria-hidden="true"[^>]*><span>${name}</span>`))
  }
  assert.equal((html.match(/src="\/icons\/tailscale.png"/g) || []).length, 2)
  assert.equal((html.match(/alt=""/g) || []).length, 2)
  assert.doesNotMatch(html, /https?:\/\//)
  const unknown = render('custom/unknown:latest')
  assert.match(unknown, /data-brand="stack"/)
  assert.match(unknown, /data-brand="container"/)
  assert.match(unknown, /site-web-1/)
})

test('image artwork is contained and neutral SVG viewports retain breathing room', () => {
  for (const image of ['tailscale/tailscale:stable', 'lscr.io/linuxserver/qbittorrent:latest', 'custom/unknown:latest']) {
    const html = renderToStaticMarkup(createElement(components.ProjectGroup, { project: { ...fixture, containers: [{ ...fixture.containers[0], image }] }, onReview() {}, actionsDisabled: false }))
    const icons = [...html.matchAll(/<svg class="service-icon[^>]*viewBox="([^"]+)"/g)]
    assert.equal(icons.length, image === 'custom/unknown:latest' ? 2 : 0)
    for (const [, viewBox] of icons) {
      const [x, y, width, height] = viewBox.split(' ').map(Number)
      assert.ok(x <= -3 && y <= -3 && x + width >= 27 && y + height >= 27, `${image}: ${viewBox} must surround the 0–24 artwork with a safe inset`)
      assert.equal(width, height)
    }
  }
})

test('monitor starts honestly connecting, with accessible search and filters, not a connected claim or fabricated counts', () => {
  const html = renderToStaticMarkup(createElement(components.default))
  assert.match(html, /<h1[^>]*>Services<\/h1>/)
  assert.match(html, /Connecting/)
  assert.match(html, /Search services/)
  assert.match(html, /Needs attention/)
  assert.match(html, /Check for updates/)
  assert.doesNotMatch(html, /Engine connected|All systems healthy|LIVE/)
  assert.match(html, /Loading services/)
  assert.doesNotMatch(html, /Recent activity|Docker activity/)
})

test('Activity has its own navigation destination and the approved Essentials toolbar without a count or state filter', () => {
  const html = renderToStaticMarkup(createElement(components.default, { activePage: 'activity' }))
  assert.match(html, /<h1>Activity<\/h1>/)
  assert.match(html, /aria-label="Search activity"/)
  for (const label of ['Application:', 'Activity type:', 'Sort activity:']) assert.ok(html.includes(label))
  assert.doesNotMatch(html, /More filters|activity-filter-summary|data-state-filter|Check for updates/)
  assert.match(html, /href="#activity"[^>]*aria-current="page"/)
})
