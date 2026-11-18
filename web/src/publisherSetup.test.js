import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'vite'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'

const server = await createServer({ server: { middlewareMode: true, hmr: false, ws: false }, appType: 'custom' })
after(() => server.close())
const api = await server.ssrLoadModule('/src/publisherApi.ts')
const ui = await server.ssrLoadModule('/src/Publishers.tsx')
const repository = { id: 14, fullName: 'everyonesoftware/finance', defaultBranch: 'main', installationId: 8 }
const variables = { SERVICES_PUBLISH_NOTIFICATIONS: 'false', SERVICES_PUBLISH_URL: 'https://new.example/api/images/published', TAILSCALE_AUDIENCE: 'audience', TAILSCALE_CLIENT_ID: 'client', TAILSCALE_TAGS: 'tag:publisher' }
const configuration = { fingerprint: 'a'.repeat(64), secretExists: true, variables: Object.entries(variables).map(([name, desiredValue], index) => ({ name, desiredValue, exists: index !== 2, currentValue: index === 1 ? 'https://old.example/api/images/published' : index === 2 ? '' : desiredValue })) }
const plan = { repository, imageRepository: 'ghcr.io/everyonesoftware/finance', supported: true, problems: [], variables, secretName: 'SERVICES_PUBLISH_TOKEN', approval: 'b'.repeat(64), configuration }

test('setup review exposes exact existing settings and explicit credential replacement approval', () => {
  const html = renderToStaticMarkup(createElement(ui.PublisherReview, { plan }))
  for (const text of ['Existing publisher settings found', 'Current value', 'Desired value', 'Keep', 'Add', 'Replace', 'https://old.example/api/images/published', variables.SERVICES_PUBLISH_URL, 'cannot be read', 'approval', 'publications:write']) assert.ok(html.includes(text), text)
  assert.doesNotMatch(html, /conflicts stop setup|Existing values are not overwritten/)
})

test('setup validates complete configuration snapshot and desired values before allowing apply', () => {
  assert.equal(api.canApplyPlan(plan, repository, plan.imageRepository), true)
  for (const config of [null, { ...configuration, variables: [] }, { ...configuration, variables: null }, { ...configuration, fingerprint: 'stale' }, { ...configuration, variables: configuration.variables.map(item => ({ ...item, desiredValue: 'wrong' })) }]) {
    assert.equal(api.canApplyPlan({ ...plan, configuration: config }, repository, plan.imageRepository), false)
  }
})

test('setup forwards the exact reviewed configuration fingerprint alongside approval', async () => {
  const bodies = []
  const client = api.createPublisherApi(async (_url, options) => { bodies.push(JSON.parse(options.body)); return Response.json({ connection: { id: 'saved' } }) })
  await api.applyAndRefresh({ ...client, status: async () => ({ connections: [] }) }, plan, () => {})
  assert.deepEqual(bodies, [{ repository, imageRepository: plan.imageRepository, approval: plan.approval, configurationFingerprint: configuration.fingerprint }])
})

test('fixed footer exposes recovery, retry and verification actions instead of a dead end', () => {
  const props = { busy: false, available: true, canAdvance: true, workflowNeeded: false, pendingWorkflow: false, onBack() {}, onAdvance() {}, onCancel() {} }
  for (const [step, extra, label] of [[3, {}, 'Apply settings &amp; test connection'], [3, { primaryLabel: 'Review changes' }, 'Review changes'], [4, { primaryLabel: 'Check status' }, 'Check status'], [4, { primaryLabel: 'Retry connection test' }, 'Retry connection test'], [4, { primaryLabel: 'Done' }, 'Done']]) {
    const html = renderToStaticMarkup(createElement(ui.PublisherFooter, { ...props, ...extra, step }))
    assert.match(html, /class="button primary"/)
    assert.ok(html.includes(label), label)
  }
})

test('saved incomplete configuration stays on review, while configured connections can test', () => {
  assert.equal(typeof api.publisherSetupStep, 'function')
  for (const connection of [{ configured: false, credentialAvailable: false, state: 'error' }, { configured: false, credentialAvailable: true, state: 'error' }, { configured: true, credentialAvailable: false, state: 'error' }]) assert.equal(api.publisherSetupStep(connection), 3)
  for (const state of ['configured', 'error', 'awaiting_callback', 'verified']) assert.equal(api.publisherSetupStep({ configured: true, credentialAvailable: true, state }), 4)
})

test('malformed configuration cannot crash review or imply readiness', () => {
  for (const variables of [null, {}, [], [null]]) {
    const html = renderToStaticMarkup(createElement(ui.PublisherReview, { plan: { ...plan, configuration: { ...configuration, secretExists: false, variables } } }))
    assert.match(html, /role="alert"/)
    assert.doesNotMatch(html, /Ready to connect|Current value/)
  }
})
