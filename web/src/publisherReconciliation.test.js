import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'vite'
const server = await createServer({ server: { middlewareMode: true, hmr: false, ws: false }, appType: 'custom' })
after(() => server.close())
const api = await server.ssrLoadModule('/src/publisherApi.ts')
const repository = { id: 14, installationId: 8, fullName: 'Acme/Current', defaultBranch: 'main' }
const reconciliation = { state: 'review_required', previousName: 'Acme/Previous', currentName: repository.fullName, repositoryId: 14, installationId: 8, message: 'Review repository identity' }
const connection = { id: 'saved/id', repository, imageRepository: 'ghcr.io/acme/app', state: 'verified', reconciliation }
const plan = { repository, imageRepository: connection.imageRepository, previousName: reconciliation.previousName, fingerprint: 'a'.repeat(64), requiresApproval: true }
test('rename approval binds immutable identity, exact scope, current review names and fingerprint', () => {
  assert.equal(typeof api.canReconcilePlan, 'function')
  assert.equal(api.canReconcilePlan(plan, connection), true)
  for (const invalid of [null, {}, { ...plan, repository: null }, { ...plan, repository: { ...repository, id: 15 } }, { ...plan, repository: { ...repository, installationId: 9 } }, { ...plan, imageRepository: 'ghcr.io/acme/other' }, { ...plan, previousName: 'Acme/Other' }, { ...plan, previousName: 'invalid' }, { ...plan, repository: { ...repository, fullName: 'Acme/Other' } }, { ...plan, requiresApproval: false }, { ...plan, requiresApproval: 'true' }, ...['', 'a'.repeat(63), 'A'.repeat(64), 'g'.repeat(64), `${'a'.repeat(64)}\n`].map(fingerprint => ({ ...plan, fingerprint }))]) assert.equal(api.canReconcilePlan(invalid, connection), false, JSON.stringify(invalid))
  for (const state of ['identity_mismatch', 'awaiting_publication', 'reconciled']) assert.equal(api.canReconcilePlan(plan, { ...connection, reconciliation: { ...reconciliation, state } }), false)
  for (const change of [{ repositoryId: 15 }, { installationId: 9 }, { currentName: 'Acme/Elsewhere' }]) assert.equal(api.canReconcilePlan(plan, { ...connection, reconciliation: { ...reconciliation, ...change } }), false)
})
test('same-name legacy history requires explicit identity approval', () => {
  const sameName = { ...plan, previousName: repository.fullName }
  const legacy = { ...connection, reconciliation: { ...reconciliation, previousName: repository.fullName } }
  assert.equal(api.canReconcilePlan(sameName, legacy), true)
  assert.equal(api.canReconcilePlan({ ...sameName, requiresApproval: false }, legacy), false)
  assert.equal(api.canReconcilePlan({ ...sameName, fingerprint: '' }, legacy), false)
})
test('unavailable saved state accepts only a fresh fully bound approval plan', () => {
  const unavailable = { ...connection, reconciliation: { ...reconciliation, state: 'unavailable' } }
  assert.equal(api.canReconcilePlan(plan, unavailable), true)
  for (const invalid of [null, { ...plan, requiresApproval: false }, { ...plan, fingerprint: '' }, { ...plan, repository: { ...repository, id: 99 } }, { ...plan, previousName: 'Acme/Other' }]) assert.equal(api.canReconcilePlan(invalid, unavailable), false)
})
test('same-name identity review and unavailable retry use neutral actionable notices', async () => {
  const { createElement } = await import('react')
  const { renderToStaticMarkup } = await import('react-dom/server')
  const { PublisherRenameNotice } = await server.ssrLoadModule('/src/PublisherReconciliation.tsx')
  const render = review => renderToStaticMarkup(createElement(PublisherRenameNotice, { connection: { ...connection, reconciliation: review }, busy: false, onReview() {}, onRefresh() {} }))
  const legacy = render({ ...reconciliation, previousName: repository.fullName })
  assert.match(legacy, /Repository identity review required/)
  assert.match(legacy, />Review identity</)
  assert.doesNotMatch(legacy, /Repository rename detected|Review rename/)
  assert.match(render({ ...reconciliation, state: 'unavailable' }), />Review identity</)
  assert.doesNotMatch(render({ ...reconciliation, state: 'identity_mismatch' }), />Review identity</)
  assert.match(render(reconciliation), /Repository rename detected/)
  assert.match(render(reconciliation), />Review rename</)
})
test('rename API uses separate read-only plan and explicit fingerprint approval', async () => {
  const calls = []
  const client = api.createPublisherApi(async (url, options) => { calls.push([url, options.method, JSON.parse(options.body)]); return Response.json({}) })
  assert.equal(typeof client.reconciliationPlan, 'function')
  await client.reconciliationPlan(connection.id)
  await client.reconcile(connection.id, plan.fingerprint)
  assert.deepEqual(calls, [['/api/publishers/saved%2Fid/reconciliation/plan', 'POST', {}], ['/api/publishers/saved%2Fid/reconciliation', 'POST', { fingerprint: plan.fingerprint, approved: true }]])
})

test('malformed provider review fields fail closed without throwing', () => {
  for (const imageRepository of [null, undefined, 7, {}, []]) assert.equal(api.canReconcilePlan({ ...plan, imageRepository }, connection), false)
  for (const fullName of ['Acme/.', 'Acme/..', 'Acme/Bad\n', 'Acme/a b', null]) assert.equal(api.canReconcilePlan({ ...plan, repository: { ...repository, fullName } }, { ...connection, reconciliation: { ...reconciliation, currentName: fullName } }), false)
  for (const change of [{ deletion: {} }, { state: 'disconnected' }]) assert.equal(api.canReconcilePlan(plan, { ...connection, ...change }), false)
})

test('awaiting delivery stays in attention with non-review copy until receipt reconciliation', async () => {
  const { publisherNeedsAttention, publisherSignal, publisherHref } = await server.ssrLoadModule('/src/publisherReconciliation.ts')
  const pending = { ...connection, reconciliation: { ...reconciliation, state: 'awaiting_publication' } }
  assert.equal(publisherNeedsAttention(pending), true)
  assert.equal(publisherSignal(pending), 'Publisher awaiting publication')
  assert.equal(publisherHref(pending.id), '#publishers/saved%2Fid')
  assert.equal(pending.state, 'verified')
  assert.equal(publisherNeedsAttention({ ...pending, reconciliation: { ...pending.reconciliation, state: 'reconciled' } }), false)
})

test('same-name reconciled signals do not invent a rename', async () => {
  const { publisherSignal } = await server.ssrLoadModule('/src/publisherReconciliation.ts')
  assert.equal(publisherSignal({ ...connection, reconciliation: { ...reconciliation, state: 'reconciled', previousName: repository.fullName } }), 'Publisher identity reconciled')
  assert.equal(publisherSignal({ ...connection, reconciliation: { ...reconciliation, state: 'reconciled' } }), 'Publisher rename reconciled')
})
test('retained receipts do not claim rename delivery while awaiting a new publication', async () => {
  const { createElement } = await import('react')
  const { renderToStaticMarkup } = await import('react-dom/server')
  const { PublisherConnectionRow } = await server.ssrLoadModule('/src/Publishers.tsx')
  const pending = { ...connection, reconciliation: { ...reconciliation, state: 'awaiting_publication' }, lastPublication: { version: '109', digest: 'sha256:old', receivedAt: '2026-11-18T00:00:00Z' } }
  const html = renderToStaticMarkup(createElement(PublisherConnectionRow, { connection: pending, expanded: false, busy: false, onToggle() {}, onView() {}, onTest() {} }))
  assert.match(html, /Awaiting next publication/)
  assert.doesNotMatch(html, />Image received</)
})
