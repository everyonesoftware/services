import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createServer } from 'vite'

const server = await createServer({ server: { middlewareMode: true, hmr: false, ws: false }, appType: 'custom' })
after(() => server.close())
const { default: App } = await server.ssrLoadModule('/src/App.tsx')
const { Publishers } = await server.ssrLoadModule('/src/Publishers.tsx')
const { AuthAccounts } = await server.ssrLoadModule('/src/AuthAccounts.tsx')
const props = { user: { id: 'admin', username: '<Admin & user>', role: 'admin' }, onBack() {}, onAccounts() {}, onPublishers() {}, onLogout() {}, onPasswordChanged() {} }

for (const [component, active] of [[App, 'monitor'], [Publishers, 'publishers'], [AuthAccounts, 'accounts']]) {
  test(`${active} uses the shared sidebar with branding, navigation and signed-in user footer`, () => {
    const html = renderToStaticMarkup(createElement(component, props))
    assert.match(html, /class="app-shell"/)
    const sidebar = html.match(/<aside class="app-sidebar"[^>]*>([\s\S]*?)<\/aside>/)?.[1]
    assert.ok(sidebar, 'shared sidebar must exist')
    assert.match(sidebar, /aria-label="Services home"/)
    assert.match(sidebar, /class="brand-mark"/)
    const links = [...sidebar.matchAll(/<a [^>]*href="#(monitor|publishers|accounts)"[^>]*>/g)]
      .filter(match => !match[0].includes('class="brand"'))
    assert.deepEqual(links.map(match => match[1]), ['monitor', 'publishers', 'accounts'])
    assert.equal(links.filter(match => match[0].includes('aria-current="page"')).length, 1)
    assert.ok(links.find(match => match[1] === active)[0].includes('aria-current="page"'))
    assert.match(sidebar, /class="sidebar-user"[^>]*>&lt;Admin &amp; user&gt;</)
    assert.match(sidebar, /class="sidebar-signout"/)
    assert.ok(sidebar.indexOf('sidebar-user') > sidebar.indexOf('</nav>'))
    assert.ok(sidebar.indexOf('sidebar-signout') > sidebar.indexOf('sidebar-user'))
    assert.match(html, /class="app-content"/)
    assert.doesNotMatch(html, /class="app-header/)
  })
}

test('signed-out direct monitor rendering does not fabricate a user or show sign out', () => {
  const html = renderToStaticMarkup(createElement(App))
  assert.doesNotMatch(html, /sidebar-user|sidebar-signout|Engine connected/)
  assert.match(html, /Connecting/)
})

test('accounts starts with administrator settings selected and other credential panels hidden', () => {
  const html = renderToStaticMarkup(createElement(AuthAccounts, props))
  const sections = html.match(/<nav[^>]*aria-label="Account sections"[^>]*>([\s\S]*?)<\/nav>/)?.[1]
  assert.ok(sections, 'account sections must have a labeled switcher')
  assert.deepEqual([...sections.matchAll(/aria-pressed="(true|false)"[^>]*>([^<]+)</g)].map(match => [match[2], match[1]]), [['Administrators', 'true'], ['API keys', 'false'], ['Password', 'false']])
  assert.match(html, /<section[^>]*aria-labelledby="admins-title"[^>]*>/)
  assert.doesNotMatch(html, /<section[^>]*aria-labelledby="admins-title"[^>]*hidden/)
  assert.match(html, /<section[^>]*aria-labelledby="password-title"[^>]*hidden=""/)
  assert.match(html, /<section[^>]*aria-labelledby="keys-title"[^>]*hidden=""/)
})
