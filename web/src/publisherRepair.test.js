import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { createServer } from 'vite'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
const server = await createServer({ server: { middlewareMode: true, hmr: false, ws: false }, appType: 'custom' })
after(() => server.close())
const api = await server.ssrLoadModule('/src/publisherApi.ts')
const ui = await server.ssrLoadModule('/src/Publishers.tsx')
const repairUi = await server.ssrLoadModule('/src/PublisherRepair.tsx').catch(() => ({}))
const fingerprint = createHash('sha256').update('exact repair review').digest('hex')
const plan = { repository: { id: 1, fullName: 'Acme/Website', defaultBranch: 'main', installationId: 8 }, imageRepository: 'ghcr.io/acme/website', configuration: { fingerprint, secretExists: true, variables: [
  { name: 'SERVICES_PUBLISH_URL', exists: false, currentValue: '', desiredValue: 'https://services.example/api/images/published' },
  { name: 'SERVICES_PUBLISH_NOTIFICATIONS', exists: true, currentValue: 'false', desiredValue: 'false' },
  { name: 'TAILSCALE_CLIENT_ID', exists: true, currentValue: 'old exact', desiredValue: 'new exact' },
  { name: 'TAILSCALE_AUDIENCE', exists: true, currentValue: 'same', desiredValue: 'same' },
  { name: 'TAILSCALE_TAGS', exists: true, currentValue: 'tag:publisher', desiredValue: 'tag:publisher' },
] } }
test('missing credential recovery offers review rather than impossible rotation or generic saved setup advice', () => {
  const render = change => renderToStaticMarkup(createElement(ui.PublisherConnection, { connection: { ...connection, ...change }, busy: false, onTest() {}, onRecover() {}, onManage() {}, onRepair() {} }))
  for (const state of ['error', 'disconnected']) {
    const html = render({ state })
    assert.match(html, /Repair &amp; retry push/)
    assert.doesNotMatch(html, /Rotate key|Repair reuses the saved setup|Configuration is saved|Reconnect to resume/)
    assert.match(html, /review/i)
  }
  assert.doesNotMatch(render({ deletion: { tailscaleRevocationAttested: true } }), /Repair &amp; retry push|Rotate key|>Reconnect</)
})
test('modern error and disconnected publishers offer reviewed recovery even when saved credentials remain', () => {
  for (const state of ['error', 'disconnected']) {
    const html = renderToStaticMarkup(createElement(ui.PublisherConnection, { connection: { ...connection, state, configured: true, credentialAvailable: true }, busy: false, onTest() {}, onRecover() {}, onManage() {}, onRepair() {} }))
    assert.match(html, /Repair &amp; retry push/)
    assert.doesNotMatch(html, />Reconnect</)
  }
})
test('repair review displays exact nonsecret values and replacement impact; invalid plans stay unusable', () => {
  assert.equal(typeof repairUi.PublisherRepairReview, 'function')
  const html = renderToStaticMarkup(createElement(repairUi.PublisherRepairReview, { plan }))
  for (const value of ['SERVICES_PUBLISH_URL', 'Missing', 'SERVICES_PUBLISH_NOTIFICATIONS', 'Unchanged', 'TAILSCALE_CLIENT_ID', 'Replace', 'old exact', 'new exact', 'SERVICES_PUBLISH_TOKEN', 'Notifications remain disabled', 'No image', 'workflow files']) assert.ok(html.includes(value), value)
  assert.equal(typeof api.canRepairPlan, 'function')
  assert.equal(api.canRepairPlan(plan, connection), true)
  for (const invalid of [null, {}, { ...plan, configuration: null }, { ...plan, configuration: { ...plan.configuration, variables: null } }, { ...plan, configuration: { ...plan.configuration, variables: [null] } }, { ...plan, imageRepository: 'ghcr.io/other/image' }]) assert.equal(api.canRepairPlan(invalid, connection), false)
})
const connection = { id: 'saved/key', repository: { id: 1, fullName: 'Acme/Website', defaultBranch: 'main', installationId: 8 }, imageRepository: 'ghcr.io/acme/website', state: 'error', configured: false, credentialAvailable: false, createdAt: '' }
test('repair accepts fresh repository metadata by stable ownership and displays it for approval', () => {
  const fresh = { ...plan, repository: { ...plan.repository, fullName: 'Acme/Renamed', defaultBranch: 'release/next' } }
  assert.equal(api.canRepairPlan(fresh, connection), true)
  const html = renderToStaticMarkup(createElement(repairUi.PublisherRepairReview, { plan: fresh }))
  assert.match(html, /Acme\/Renamed/)
  assert.match(html, /release\/next/)
  for (const repository of [
    { ...fresh.repository, id: 2 }, { ...fresh.repository, installationId: 9 },
    { ...fresh.repository, fullName: '' }, { ...fresh.repository, fullName: 'not-owner-slash-name' },
    { ...fresh.repository, fullName: 'Acme/Repo\nspoof' },
    { ...fresh.repository, defaultBranch: '' }, { ...fresh.repository, defaultBranch: 'main\nspoof' },
  ]) assert.equal(api.canRepairPlan({ ...fresh, repository }, connection), false, JSON.stringify(repository))
  assert.equal(api.canRepairPlan({ ...fresh, imageRepository: `${connection.imageRepository}-other` }, connection), false)
})
for (const [name, variables] of [
  ['empty', []], ['partial', plan.configuration.variables.slice(1)],
  ['duplicate', [...plan.configuration.variables.slice(0, 4), plan.configuration.variables[0]]],
  ['unknown', [...plan.configuration.variables.slice(0, 4), { ...plan.configuration.variables[4], name: 'UNKNOWN' }]],
]) test(`repair rejects ${name} variable collections`, () => {
  const invalid = { ...plan, configuration: { ...plan.configuration, variables } }
  assert.equal(api.canRepairPlan(invalid, connection), false)
  const html = renderToStaticMarkup(createElement(repairUi.PublisherRepairReview, { plan: invalid }))
  assert.match(html, /role="alert"/)
  assert.doesNotMatch(html, /Desired value/)
})
for (const bad of ['', 'exact', 'a'.repeat(63), 'a'.repeat(65), 'A'.repeat(64), 'g'.repeat(64), `${'a'.repeat(64)}\n`]) {
  test(`repair rejects fingerprint ${JSON.stringify(bad)}`, () => {
    assert.equal(api.canRepairPlan({ ...plan, configuration: { ...plan.configuration, fingerprint: bad } }, connection), false)
  })
}
test('repair only accepts notifications explicitly disabled, with variables in any order', () => {
  assert.equal(api.canRepairPlan({ ...plan, configuration: { ...plan.configuration, variables: [...plan.configuration.variables].reverse() } }, connection), true)
  for (const desiredValue of ['true', '', 'False', ' false ', false]) {
    const variables = plan.configuration.variables.map(item => item.name === 'SERVICES_PUBLISH_NOTIFICATIONS' ? { ...item, desiredValue } : item)
    assert.equal(api.canRepairPlan({ ...plan, configuration: { ...plan.configuration, variables } }, connection), false, JSON.stringify(desiredValue))
  }
})
test('repair API separates fresh read-only review from fingerprint-bound approval', async () => {
  const calls = []
  const client = api.createPublisherApi(async (url, options) => { calls.push([url, options.method, JSON.parse(options.body)]); return Response.json({}) })
  assert.equal(typeof client.repairPlan, 'function')
  await client.repairPlan(connection.id)
  await client.repair(connection.id, fingerprint)
  assert.deepEqual(calls, [['/api/publishers/saved%2Fkey/repair/plan', 'POST', {}], ['/api/publishers/saved%2Fkey/repair', 'POST', { fingerprint }]])
})
