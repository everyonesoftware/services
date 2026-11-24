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

for (const [component, active] of [[App, 'monitor'], [App, 'activity'], [Publishers, 'publishers'], [AuthAccounts, 'settings']]) {
  test(`${active} uses the shared sidebar with branding, navigation and signed-in user footer`, () => {
    const html = renderToStaticMarkup(createElement(component, { ...props, activePage: active }))
    assert.match(html, /class="app-shell"/)
    const sidebar = html.match(/<aside class="app-sidebar"[^>]*>([\s\S]*?)<\/aside>/)?.[1]
    assert.ok(sidebar, 'shared sidebar must exist')
    assert.match(sidebar, /aria-label="Services home"/)
    assert.match(sidebar, /class="brand-mark"/)
    assert.doesNotMatch(sidebar, />\s*Workspace\s*</)
    const links = [...sidebar.matchAll(/<a [^>]*href="#(monitor|activity|publishers|settings)"[^>]*>/g)]
      .filter(match => !match[0].includes('class="brand"'))
    assert.deepEqual(links.map(match => match[1]), ['monitor', 'activity', 'publishers', 'settings'])
    assert.doesNotMatch(sidebar.match(/<nav[^>]*>([\s\S]*?)<\/nav>/)[1], /Settings|Accounts/)
    assert.match(sidebar, /class="sidebar-settings"[^>]*href="#settings"[^>]*aria-label="Settings"/)
    assert.ok(sidebar.indexOf('sidebar-settings') > sidebar.indexOf('sidebar-user'))
    assert.equal(links.filter(match => match[0].includes('aria-current="page"')).length, 1)
    assert.ok(links.find(match => match[1] === active)[0].includes('aria-current="page"'))
    assert.match(sidebar, /class="sidebar-user"[^>]*>&lt;Admin &amp; user&gt;</)
    assert.doesNotMatch(sidebar, /Sign out/)
    assert.ok(sidebar.indexOf('sidebar-user') > sidebar.indexOf('</nav>'))
    if (active !== 'settings') assert.doesNotMatch(html, /Sign out/)
    assert.match(html, /class="app-content"/)
    assert.doesNotMatch(html, /class="app-header/)
  })
}

test('signed-out direct monitor rendering does not fabricate a user or show sign out', () => {
  const html = renderToStaticMarkup(createElement(App))
  assert.doesNotMatch(html, /sidebar-user|Sign out|Engine connected/)
  assert.match(html, /Connecting/)
})

test('Settings renders its page title without a duplicate breadcrumb', () => {
  const html = renderToStaticMarkup(createElement(AuthAccounts, props))
  assert.match(html, /<h1>Settings<\/h1>/)
  assert.doesNotMatch(html, /aria-label="Breadcrumb"/)
})

test('Settings has a visible Sign out panel for the current user', () => {
  const html = renderToStaticMarkup(createElement(AuthAccounts, props))
  const panel = html.match(/<section[^>]*aria-labelledby="signout-title"[^>]*>([\s\S]*?)<\/section>/)
  assert.ok(panel, 'Sign out must have a dedicated panel')
  assert.doesNotMatch(panel[0], /\bhidden=/)
  assert.match(panel[1], /<h2 id="signout-title">Sign out<\/h2>/)
  assert.match(panel[1], /Signed in as <strong>&lt;Admin &amp; user&gt;<\/strong>/)
  assert.match(panel[1], /<button[^>]*>Sign out<\/button>/)
})

test('Settings opens Updates while preserving hidden administrator and credential panels', () => {
  const html = renderToStaticMarkup(createElement(AuthAccounts, props))
  const sections = html.match(/<nav[^>]*aria-label="Settings sections"[^>]*>([\s\S]*?)<\/nav>/)?.[1]
  assert.ok(sections, 'account sections must have a labeled switcher')
  assert.deepEqual([...sections.matchAll(/aria-pressed="(true|false)"[^>]*>([^<]+)</g)].map(match => [match[2], match[1]]), [['Updates', 'true'], ['Administrators', 'false'], ['API keys', 'false'], ['Password', 'false']])
  assert.match(html, /Web app updates/)
  assert.match(html, /Check for updates/)
  assert.match(html, /Finish or save any unsaved changes before reloading/)
  assert.match(html, /<section[^>]*aria-labelledby="admins-title"[^>]*>/)
  assert.match(html, /<section[^>]*aria-labelledby="admins-title"[^>]*hidden/)
  assert.match(html, /<section[^>]*aria-labelledby="password-title"[^>]*hidden=""/)
  assert.match(html, /<section[^>]*aria-labelledby="keys-title"[^>]*hidden=""/)
})
