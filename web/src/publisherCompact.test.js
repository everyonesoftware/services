import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'vite'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'

const server = await createServer({ server: { middlewareMode: true, hmr: false, ws: false }, appType: 'custom' })
after(() => server.close())
const ui = await server.ssrLoadModule('/src/Publishers.tsx')
const publication = { version: '<release>', digest: 'sha256:' + 'a'.repeat(64), tags: ['latest'], commit: 'abc123', runUrl: 'https://github.com/Acme/Website/actions/runs/42', publishedAt: '2026-01-01T00:00:00Z', receivedAt: '2026-01-01T00:01:00Z', result: 'accepted' }
const connection = { id: 'saved/key', state: 'verified', repository: { id: 14, fullName: 'Acme/Website', defaultBranch: 'main', installationId: 8 }, imageRepository: 'ghcr.io/acme/custom', createdAt: publication.publishedAt, lastPublication: publication }
const props = { connection, busy: false, onTest() {}, onRecover() {}, onManage() {} }
const render = (component, values) => renderToStaticMarkup(createElement(component, values))

test('publisher overview rows start collapsed with a named disclosure and no detail actions', () => {
  assert.equal(typeof ui.PublisherConnectionRow, 'function')
  const html = render(ui.PublisherConnectionRow, { ...props, expanded: false, view: 'publication', onToggle() {}, onView() {} })
  assert.match(html, /aria-label="Publisher Acme\/Website"/)
  assert.match(html, /class="publisher-row-summary"[^>]*aria-expanded="false"[^>]*aria-controls="[^"]+"/)
  assert.match(html, /Acme\/Website/)
  assert.match(html, /Image received/)
  assert.doesNotMatch(html, /role="tabpanel"|Rotate key|Disconnect<|Test push<|Manage connection/)
  assert.doesNotMatch(html, new RegExp(publication.digest))
})

test('expanded publisher rows connect the disclosure, tabs and active panel by IDs', () => {
  assert.equal(typeof ui.PublisherConnectionRow, 'function')
  const html = render(ui.PublisherConnectionRow, { ...props, expanded: true, view: 'connection', onToggle() {}, onView() {} })
  const controlled = html.match(/aria-controls="([^"]+-details)"/)?.[1]
  assert.ok(controlled)
  assert.ok(html.includes(`id="${controlled}"`))
  assert.match(html, /role="tablist"/)
  assert.match(html, /role="tab"[^>]*aria-selected="false"[^>]*tabindex="-1"[^>]*>Latest image</)
  assert.match(html, /role="tab"[^>]*aria-selected="true"[^>]*tabindex="0"[^>]*>Connection</)
  assert.match(html, /role="tabpanel"[^>]*aria-labelledby="[^"]+-connection"/)
})

test('Latest image shows only real metadata and a direct safe workflow link, never connection actions', () => {
  const html = render(ui.PublisherConnection, { ...props, view: 'publication' })
  assert.match(html, /&lt;release&gt;/)
  assert.match(html, /<details[^>]*>[\s\S]*Technical metadata/)
  assert.match(html, /<\/details>[\s\S]*href="https:\/\/github.com\/Acme\/Website\/actions\/runs\/42"/)
  assert.match(html, /Installation.*confirmation/)
  assert.doesNotMatch(html, /Rotate key|Disconnect<|Test push|Manage connection|Default branch/)
  for (const state of ['configured', 'awaiting_callback', 'error', 'disconnected']) {
    const absent = render(ui.PublisherConnection, { ...props, connection: { ...connection, state }, view: 'publication' })
    assert.match(absent, /No image received/)
    assert.doesNotMatch(absent, new RegExp(publication.digest))
  }
})

test('Connection view keeps direct management actions separate from image metadata', () => {
  const html = render(ui.PublisherConnection, { ...props, view: 'connection' })
  for (const label of ['Test push', 'Rotate key', 'Disconnect']) assert.ok(html.includes(`>${label}<`), label)
  assert.doesNotMatch(html, /Latest publication|Manage connection|<summary>[^<]*Disconnect/)
  assert.doesNotMatch(html, new RegExp(publication.digest))
  assert.match(html, /class="button publisher-disconnect"/)
  assert.match(html, /<details[^>]*>[\s\S]*Setup &amp; diagnostic details/)
})
