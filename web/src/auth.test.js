import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createServer } from 'vite'
import { readFileSync } from 'node:fs'

const server = await createServer({ server: { middlewareMode: true, hmr: false, ws: false }, appType: 'custom' })
after(() => server.close())
const api = await server.ssrLoadModule('/src/authApi.ts').catch(() => ({}))
const auth = await server.ssrLoadModule('/src/AuthGate.tsx').catch(() => ({}))
const accounts = await server.ssrLoadModule('/src/AuthAccounts.tsx').catch(() => ({}))
const model = await server.ssrLoadModule('/src/authAccountModel.ts')

test('entry mounts auth gate and dashboard exposes optional account actions without breaking direct imports', async () => {
  const main = readFileSync(new URL('./main.tsx', import.meta.url), 'utf8')
  assert.match(main, /<AuthGate\s*\/>/)
  const { default: App } = await server.ssrLoadModule('/src/App.tsx')
  const html = renderToStaticMarkup(createElement(App, { user: { username: 'admin' }, onAccounts() {}, onLogout() {} }))
  assert.match(html, /aria-label="Settings"/)
  assert.match(html, />Sign out</)
  assert.match(html, />admin</)
  assert.doesNotMatch(renderToStaticMarkup(createElement(App)), />Sign out</)
})

test('accounts exposes account/password/key forms and one-time key notice', () => {
  assert.equal(typeof accounts.AuthAccounts, 'function')
  const html = renderToStaticMarkup(createElement(accounts.AuthAccounts, { user: { id: 'one', username: 'admin', role: 'admin' }, onBack() {}, onLogout() {}, onPasswordChanged() {} }))
  for (const label of ['Add administrator', 'Change password', 'Create API key', 'services:read', 'publications:write', 'Exact image repositories', 'Expiry']) assert.ok(html.includes(label), label)
  assert.match(html, /minLength="15"/)
  assert.equal((html.match(/At least 15 Unicode characters; at most 1024 UTF-8 bytes\./g) || []).length, 2)
  assert.match(html, /shown only once/i)
})

test('publisher payload requires exact repositories and converts optional expiry', () => {
  assert.equal(typeof model.keyPayload, 'function')
  const form = new FormData()
  form.set('name', 'publisher'); form.append('scopes', 'publications:write'); form.set('repositories', 'ghcr.io/acme/site,\nghcr.io/acme/worker\nghcr.io/acme/site')
  assert.deepEqual(model.keyPayload(form), { name: 'publisher', scopes: ['publications:write'], repositories: ['ghcr.io/acme/site', 'ghcr.io/acme/worker'] })
  form.set('repositories', '')
  assert.throws(() => model.keyPayload(form), /repository/i)
  for (const repository of ['ghcr.io/acme/*', 'ghcr.io/acme/site:latest', 'https://ghcr.io/acme/site']) { form.set('repositories', repository); assert.throws(() => model.keyPayload(form), /exact/i) }
  form.set('repositories', 'ghcr.io/acme/site'); form.set('expiresAt', '2030-01-01T00:00')
  assert.equal(model.keyPayload(form).expiresAt, new Date('2030-01-01T00:00').toISOString())
  form.delete('scopes'); assert.throws(() => model.keyPayload(form), /scope/i)
})

test('publication repository validation matches the GHCR receiver, not arbitrary registries', () => {
  for (const repository of ['ghcr.io/acme/my__site', 'ghcr.io/acme/my--site']) {
    const form = new FormData(); form.set('name', 'publisher'); form.append('scopes', 'publications:write'); form.set('repositories', repository)
    assert.deepEqual(model.keyPayload(form).repositories, [repository])
  }
  for (const repository of ['nginx', 'acme/my__site', 'registry.test:5000/acme/my--site', 'GHCR.IO/acme/app']) {
    const form = new FormData(); form.set('name', 'publisher'); form.append('scopes', 'publications:write'); form.set('repositories', repository)
    assert.throws(() => model.keyPayload(form), /exact|GHCR/i)
  }
})

test('read-only keys reject irrelevant repository restrictions before submission', () => {
  const form = new FormData(); form.set('name', 'reader'); form.append('scopes', 'services:read'); form.set('repositories', 'ghcr.io/acme/site')
  assert.throws(() => model.keyPayload(form), /publications:write/i)
})

test('admin deletion is unavailable for self or the last admin', () => {
  assert.equal(typeof model.canDeleteUser, 'function')
  const one = { id: 'one', username: 'admin', role: 'admin' }
  const two = { id: 'two', username: 'other', role: 'admin' }
  assert.equal(model.canDeleteUser(one, 'one', [one, two]), false)
  assert.equal(model.canDeleteUser(two, 'one', [two]), false)
  assert.equal(model.canDeleteUser(two, 'one', [one, two]), true)
})

test('session observer catches unauthorized API polling and mutations, not unrelated origins or pages', async () => {
  assert.equal(typeof api.sessionFetch, 'function')
  let expired = 0
  const fetcher = api.sessionFetch(async () => new Response(null, { status: 401 }), () => expired++, 'https://services.test')
  await fetcher('/api/projects'); await fetcher('/api/auth/keys', { method: 'POST' })
  assert.equal(expired, 2)
  await fetcher('https://other.test/api/projects'); await fetcher('/favicon.ico')
  assert.equal(expired, 2)
})

test('auth errors preserve JSON server explanation and status', async () => {
  await assert.rejects(api.authRequest('/login', { method: 'POST' }, async () => Response.json({ error: 'Invalid credentials' }, { status: 401 })), error => error.status === 401 && error.message === 'Invalid credentials')
})

test('gate initially renders no dashboard and setup form explains the local token with strong password limits', () => {
  assert.equal(typeof auth.AuthGate, 'function')
  const gate = renderToStaticMarkup(createElement(auth.AuthGate))
  assert.match(gate, /Checking session/)
  assert.doesNotMatch(gate, /Monitor|Accounts/)
  const setup = renderToStaticMarkup(createElement(auth.AuthForm, { setup: true, onAuthenticated() {} }))
  assert.match(setup, /\/data\/admin-setup-token/)
  assert.match(setup, /name="setupToken"/)
  assert.match(setup, /minLength="15"/)
  assert.match(setup, /maxLength="1024"/)
  assert.match(setup, /At least 15 Unicode characters; at most 1024 UTF-8 bytes\./)
  assert.match(setup, /autoComplete="new-password"/)
  const login = renderToStaticMarkup(createElement(auth.AuthForm, { setup: false, onAuthenticated() {} }))
  assert.doesNotMatch(login, /name="setupToken"/)
  assert.match(login, /autoComplete="current-password"/)
})

test('auth requests send explicit same-origin JSON credentials and accept empty 204 responses', async () => {
  assert.equal(typeof api.authRequest, 'function')
  const requests = []
  const fetcher = async (url, options) => { requests.push([url, options]); return Response.json({ user: { id: 'one', username: 'admin', role: 'admin' } }, { status: 201 }) }
  const payload = { username: 'admin', password: 'fifteen characters!', setupToken: 'setup-secret' }
  assert.equal((await api.authRequest('/setup', { method: 'POST', body: payload }, fetcher)).user.username, 'admin')
  assert.equal(requests[0][0], '/api/auth/setup')
  assert.equal(requests[0][1].credentials, 'same-origin')
  assert.equal(requests[0][1].headers['Content-Type'], 'application/json')
  assert.deepEqual(JSON.parse(requests[0][1].body), payload)
  assert.equal(await api.authRequest('/logout', { method: 'POST' }, async () => new Response(null, { status: 204 })), undefined)
})
