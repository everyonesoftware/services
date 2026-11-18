// Browser-only publisher setup regression. Every /api request is a local fixture;
// non-loopback requests are blocked. No real Services, GitHub, or Tailscale writes.
// Run: CHROMIUM=/opt/data/.local/bin/chromium PLAYWRIGHT_MODULE=/absolute/path/to/playwright/index.mjs node web/tests/publisher-setup.browser.mjs
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from '../node_modules/vite/dist/node/index.js'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
let server, browser, origin
before(async () => {
  server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
  await server.listen()
  origin = new URL(server.resolvedUrls.local[0]).origin
  browser = await chromium.launch({ executablePath: process.env.CHROMIUM || '/opt/data/.local/bin/chromium', headless: true })
})
after(async () => { await browser?.close(); await server?.close() })

const repository = { id: 14, fullName: 'fixture/finance', defaultBranch: 'main', installationId: 8 }
const imageRepository = 'ghcr.io/fixture/finance'
const tailnet = { clientId: 'finance-client', audience: 'api.tailscale.com/finance-client', tags: 'tag:services-publisher' }
const endpoint = 'https://services.example.test/api/images/published'
const applyLabel = 'Apply settings & test connection'
const sha = value => createHash('sha256').update(value).digest('hex')
const screenshot = async (page, name) => {
  if (!process.env.SETUP_SCREENSHOTS) return
  await mkdir(process.env.SETUP_SCREENSHOTS, { recursive: true })
  await page.screenshot({ path: join(process.env.SETUP_SCREENSHOTS, `${name}.png`), fullPage: true })
}

async function fixture(t, viewport = { width: 1280, height: 900 }) {
  const context = await browser.newContext({ viewport, serviceWorkers: 'block' })
  const page = await context.newPage()
  page.setDefaultTimeout(6000)
  const calls = [], errors = [], unexpected = []
  const state = {
    review: 0, repairReview: 0, createFailure: '', repairFailure: '', reviewFailure: '',
    statusFailure: false, optimisticCreate: false, hold: null,
    status: {
      available: true,
      github: { connected: true, name: 'Fixture GitHub App', permissionsChecked: true, variablesWrite: true, installUrl: 'https://github.com/apps/fixture/installations/new' },
      tailnet: { clientId: '', audience: '', tags: '' }, tailnets: { [repository.id]: { ...tailnet } },
      endpoint, connections: [],
    },
  }
  page.on('pageerror', error => errors.push(error.message))
  const configuration = (kind = 'create') => ({
    fingerprint: sha(`${kind}:${kind === 'create' ? state.review : state.repairReview}`), secretExists: true,
    variables: [
      { name: 'SERVICES_PUBLISH_URL', exists: true, currentValue: 'https://old.example.test/api/images/published', desiredValue: endpoint },
      { name: 'SERVICES_PUBLISH_NOTIFICATIONS', exists: true, currentValue: 'false', desiredValue: 'false' },
      { name: 'TAILSCALE_CLIENT_ID', exists: true, currentValue: 'old-finance-client', desiredValue: tailnet.clientId },
      { name: 'TAILSCALE_AUDIENCE', exists: false, currentValue: '', desiredValue: tailnet.audience },
      { name: 'TAILSCALE_TAGS', exists: true, currentValue: tailnet.tags, desiredValue: tailnet.tags },
    ],
  })
  const savedConnection = (configured = false) => ({
    id: 'fixture-finance', repository: { ...repository }, imageRepository,
    state: configured ? 'awaiting_callback' : 'error', configured,
    credentialAvailable: configured, createdAt: '2026-11-17T12:00:00Z',
    error: configured ? '' : 'Fixture configuration was only partly applied; review changes before retrying.',
  })
  const publisherMutations = () => calls.filter(call => call.method !== 'GET' && (
    call.path === '/api/publishers' || /\/(repair|test|recover|workflow|rotate|reconnect|disconnect)$/.test(call.path)
  ))
  await context.route('**/*', async route => {
    const req = route.request(), url = new URL(req.url()), path = url.pathname
    // Block rather than silently reaching a provider if the frontend regresses.
    if (url.origin !== origin) { unexpected.push(`External request: ${req.method()} ${req.url()}`); return route.abort('blockedbyclient') }
    if (!path.startsWith('/api/')) return route.continue()
    const method = req.method(), body = method === 'GET' ? undefined : req.postDataJSON()
    calls.push({ path, method, body })
    try {
      if (path === '/api/auth/status') return route.fulfill({ json: { setupRequired: false } })
      if (path === '/api/auth/me') return route.fulfill({ json: { user: { id: 'fixture-admin', username: 'Browser fixture', role: 'admin' } } })
      if (path === '/api/publishers' && method === 'GET') return state.statusFailure
        ? route.fulfill({ status: 503, body: 'Fixture saved status unavailable' }) : route.fulfill({ json: state.status })
      if (path === '/api/publishers/repositories') {
        assert.equal(method, 'GET'); return route.fulfill({ json: { repositories: [repository] } })
      }
      if (path === '/api/publishers/tailnet/guidance') {
        assert.equal(method, 'POST'); assert.deepEqual(body, { repository })
        return route.fulfill({ json: { issuer: 'https://token.actions.githubusercontent.com', subject: 'repo:fixture/finance:ref:refs/heads/main', tailnet } })
      }
      if (path === '/api/publishers/tailnet') {
        assert.equal(method, 'PUT'); assert.deepEqual(body, { ...tailnet, repository })
        state.status.tailnets[repository.id] = { ...tailnet }
        return route.fulfill({ json: state.status })
      }
      if (path === '/api/publishers/plan') {
        assert.equal(method, 'POST'); assert.deepEqual(body, { repository, imageRepository }); state.review++
        if (state.reviewFailure === 'network') return route.abort('failed')
        if (state.reviewFailure) return route.fulfill({ status: 503, body: 'Fixture review unavailable' })
        const config = configuration()
        return route.fulfill({ json: { supported: true, problems: [], repository, imageRepository,
          approval: `fixture-approval-${state.review}`, configuration: config,
          variables: Object.fromEntries(config.variables.map(value => [value.name, value.desiredValue])),
          secretName: 'SERVICES_PUBLISH_TOKEN', workflow: { supported: true, problems: [], files: [], fingerprint: sha('workflow') },
        } })
      }
      if (path === '/api/publishers' && method === 'POST') {
        assert.deepEqual(body, { repository, imageRepository, approval: `fixture-approval-${state.review}`, configurationFingerprint: configuration().fingerprint })
        if (state.hold) await state.hold
        if (state.createFailure === 'partial' || state.createFailure === 'network-saved') state.status.connections = [savedConnection(false)]
        if (state.createFailure === 'network' || state.createFailure === 'network-saved') return route.abort('failed')
        if (state.createFailure === 'stale') return route.fulfill({ status: 409, body: 'Fixture configuration review is stale; refresh review' })
        if (state.createFailure === 'partial') return route.fulfill({ status: 502, body: 'Fixture configuration failed after saving connection' })
        state.status.connections = [savedConnection(true)]
        if (state.optimisticCreate) {
          state.status.connections = [savedConnection(false)]
          return route.fulfill({ json: { connection: savedConnection(true) } })
        }
        return route.fulfill({ json: { connection: state.status.connections[0] } })
      }
      if (path === '/api/publishers/fixture-finance/repair/plan') {
        assert.equal(method, 'POST'); assert.deepEqual(body, {}); state.repairReview++
        if (state.reviewFailure === 'network') return route.abort('failed')
        if (state.reviewFailure) return route.fulfill({ status: 503, body: 'Fixture review unavailable' })
        return route.fulfill({ json: { repository, imageRepository, configuration: configuration('repair') } })
      }
      if (path === '/api/publishers/fixture-finance/repair') {
        assert.equal(method, 'POST'); assert.deepEqual(body, { fingerprint: configuration('repair').fingerprint })
        if (state.repairFailure === 'network') return route.abort('failed')
        if (state.repairFailure) return route.fulfill({ status: 409, body: 'Fixture repair review is stale; refresh review' })
        state.status.connections = [savedConnection(true)]
        return route.fulfill({ json: { connection: state.status.connections[0] } })
      }
      if (path === '/api/publishers/fixture-finance/test') {
        assert.equal(method, 'POST'); assert.deepEqual(body, {})
        assert.equal(state.status.connections[0]?.configured, true, 'never test an unconfigured saved connection')
        Object.assign(state.status.connections[0], { state: 'awaiting_callback', error: '' })
        return route.fulfill({ json: { connection: state.status.connections[0] } })
      }
      if (path === '/api/projects') return route.fulfill({ json: { projects: [] } })
      if (path === '/api/monitor') return route.fulfill({ json: { connected: true, incidents: [], events: [] } })
      if (path === '/api/operations') return route.fulfill({ json: { operations: [] } })
      if (path === '/api/icons') return route.fulfill({ json: { available: false, icons: [] } })
      throw new Error(`Unexpected API request: ${method} ${path}`)
    } catch (error) {
      unexpected.push(error.stack || error.message)
      await route.fulfill({ status: 500, body: `Fixture contract violation: ${error.message}` })
    }
  })
  t.after(async () => {
    await context.close()
    assert.deepEqual(unexpected, [], 'all API requests must satisfy the fixture contract; providers must never be contacted')
    assert.deepEqual(errors, [], 'no uncaught browser errors')
  })
  const footer = page.locator('.publisher-wizard .publisher-footer')
  const primary = name => footer.getByRole('button', { name, exact: true }).and(footer.locator('button.primary'))
  const active = name => page.locator('.publisher-steps [aria-current="step"]').filter({ hasText: name })
  const currentStep = async name => {
    await active(name).waitFor()
    assert.equal(await page.locator('.publisher-steps [aria-current="step"]').count(), 1)
  }
  const waitIdle = async () => { await page.waitForFunction(() => !document.querySelector('.publisher-controls')?.disabled) }
  const enterReview = async () => {
    await page.goto(`${origin}/#publishers`)
    await page.getByRole('button', { name: 'Connect repository', exact: true }).click()
    await footer.getByRole('button', { name: 'Select repository →', exact: true }).click()
    await page.getByRole('radio', { name: /fixture\/finance/ }).check()
    await footer.getByRole('button', { name: 'Set up Tailscale →', exact: true }).click()
    await page.getByLabel('Client ID', { exact: true }).waitFor()
    await waitIdle() // The Tailscale panel mounts before asynchronous guidance hydrates its saved values.
    assert.equal(await page.getByLabel('Client ID', { exact: true }).inputValue(), tailnet.clientId)
    const review = page.waitForResponse(response => new URL(response.url()).pathname === '/api/publishers/plan')
    await footer.getByRole('button', { name: 'Save & review →', exact: true }).click()
    await review; await waitIdle()
    assert.equal(publisherMutations().length, 0, 'opening review must never mutate provider configuration, credentials, workflow or connection tests')
  }
  const layout = async label => {
    const button = primary(label)
    await button.waitFor()
    const geometry = await button.evaluate(el => {
      const box = el.getBoundingClientRect()
      return { x: box.x, y: box.y, right: box.right, bottom: box.bottom, width: innerWidth, height: innerHeight,
        hit: el.contains(document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2)),
        overflow: document.documentElement.scrollWidth - innerWidth,
        panelOverflow: document.querySelector('.publisher-step-content').scrollWidth - document.querySelector('.publisher-step-content').clientWidth }
    })
    assert.ok(geometry.x >= 0 && geometry.y >= 0 && geometry.right <= geometry.width + 1 && geometry.bottom <= geometry.height + 1,
      `footer primary must be fully visible without scrolling: ${JSON.stringify(geometry)}`)
    assert.equal(geometry.hit, true, 'footer primary is not clipped or obscured')
    assert.ok(geometry.overflow <= 1 && geometry.panelOverflow <= 1, `no horizontal overflow: ${JSON.stringify(geometry)}`)
  }
  const reviewValues = async () => {
    const region = page.getByRole('region', { name: 'Publisher step content', exact: true })
    await region.getByText('Current value', { exact: true }).first().waitFor()
    for (const variable of configuration().variables) {
      const row = region.locator('section').filter({ has: page.getByRole('heading', { name: new RegExp(variable.name) }) }).filter({ has: page.getByText('Current value', { exact: true }) }).last()
      await row.waitFor()
      const current = row.locator('dl > div').filter({ has: page.getByText('Current value', { exact: true }) })
      const desired = row.locator('dl > div').filter({ has: page.getByText('Desired value', { exact: true }) })
      assert.equal((await current.locator('dd').innerText()).trim(), variable.exists ? variable.currentValue : 'Not set', `${variable.name}: exact current value`)
      assert.equal((await desired.locator('dd').innerText()).trim(), variable.desiredValue, `${variable.name}: exact desired value`)
    }
    await region.getByText(/existing encrypted.*secret.*will be replaced/i).waitFor()
    await region.getByText(/cannot be read/i).waitFor()
    await region.getByText(/approval/i).first().waitFor()
  }
  const apply = async () => { await primary(applyLabel).click(); await waitIdle() }
  const refreshReview = async () => { await primary('Refresh review').click(); await waitIdle() }
  const recoverInline = async () => {
    const before = state.repairReview
    await primary('Review changes').click(); await waitIdle()
    assert.equal(state.repairReview, before + 1, 'recovery must request the saved connection repair plan')
    assert.equal(await page.getByRole('dialog').count(), 0, 'wizard recovery review must be inline, never a repair modal')
    await currentStep('Review & configure'); await reviewValues()
  }
  return { page, state, calls, footer, primary, currentStep, enterReview, layout, reviewValues, apply, refreshReview, recoverInline, waitIdle, publisherMutations }
}

test('existing settings are reviewed exactly before explicit fingerprint-bound approval', async t => {
  const f = await fixture(t)
  await f.enterReview()
  await f.currentStep('Review & configure')
  await f.reviewValues()
  assert.equal(f.state.review, 1)
  assert.deepEqual(f.state.status.connections, [])
  await f.layout(applyLabel)
  await screenshot(f.page, 'setup-review-desktop')
  await f.page.locator('.publisher-step-content').evaluate(el => { el.scrollTop = el.scrollHeight })
  await f.layout(applyLabel)
  await f.apply()
  await f.currentStep('Test connection')
  assert.equal(f.state.status.connections[0].configured, true)
  assert.deepEqual(f.publisherMutations().map(call => call.path), ['/api/publishers'])
  await f.layout('Check status')
})

test('390x844 review and callback footer stay visible without page or panel horizontal overflow', async t => {
  const f = await fixture(t, { width: 390, height: 844 })
  await f.enterReview(); await f.currentStep('Review & configure')
  const navigation = await f.page.locator('.publisher-steps').evaluate(el => {
    const box = el.getBoundingClientRect(), active = el.querySelector('[aria-current="step"]').getBoundingClientRect()
    return { left: box.left, right: box.right, activeLeft: active.left, activeRight: active.right }
  })
  assert.ok(navigation.activeLeft >= navigation.left && navigation.activeRight <= navigation.right, `current step remains visible in horizontal navigation: ${JSON.stringify(navigation)}`)
  await f.layout(applyLabel)
  await screenshot(f.page, 'setup-review-phone')
  await f.page.locator('.publisher-step-content').evaluate(el => { el.scrollTop = el.scrollHeight })
  await f.layout(applyLabel)
  await f.apply(); await f.currentStep('Test connection'); await f.layout('Check status')
})

for (const failure of ['stale', 'network']) {
  test(`${failure} create rejection without saved connection consumes approval until Refresh review`, async t => {
    const f = await fixture(t)
    await f.enterReview()
    f.state.createFailure = failure
    const reads = f.calls.filter(call => call.path === '/api/publishers' && call.method === 'GET').length
    await f.apply()
    await f.page.getByRole('alert').first().waitFor()
    await f.currentStep('Review & configure')
    assert.deepEqual(f.state.status.connections, [])
    assert.ok(f.calls.filter(call => call.path === '/api/publishers' && call.method === 'GET').length > reads, 'uncertain or rejected create reads saved status')
    await f.layout('Refresh review')
    assert.equal(await f.primary(applyLabel).count(), 0, 'consumed approval cannot be submitted again')
    assert.equal(await f.page.getByText('Desired value', { exact: true }).count(), 0, 'stale provider values are cleared')
    const before = f.publisherMutations().length
    f.state.reviewFailure = 'http'
    await f.refreshReview()
    await f.page.getByRole('alert').filter({ hasText: 'Fixture review unavailable' }).waitFor()
    await f.layout('Refresh review')
    assert.equal(f.publisherMutations().length, before)
    assert.equal(await f.primary(applyLabel).count(), 0)
    f.state.reviewFailure = ''; f.state.createFailure = ''
    await f.refreshReview(); await f.reviewValues()
    await f.apply(); await f.currentStep('Test connection')
    assert.equal(f.state.review, 3, 'fresh approval follows failed refresh')
  })
}

for (const failure of ['partial', 'network-saved']) {
  test(`${failure} create retains saved connection in review and recovers through inline fingerprint-bound repair`, async t => {
    const f = await fixture(t)
    await f.enterReview()
    f.state.createFailure = failure
    await f.apply()
    await f.page.getByRole('alert').first().waitFor()
    await f.currentStep('Review & configure')
    assert.equal(f.state.status.connections.length, 1)
    assert.equal(f.state.status.connections[0].configured, false)
    await f.layout('Review changes')
    await screenshot(f.page, `setup-${failure}-recovery`)
    assert.equal(await f.page.getByRole('dialog').count(), 0)
    assert.equal(f.publisherMutations().length, 1, 'no automatic repair, repeated create or connection test')
    await f.recoverInline()
    await f.layout(applyLabel)
    await f.apply()
    await f.currentStep('Test connection')
    assert.deepEqual(f.publisherMutations().map(call => call.path), ['/api/publishers', '/api/publishers/fixture-finance/repair'])
    assert.equal(f.state.status.connections[0].configured, true)
    await f.layout('Check status')
  })
}

test('stale repair plus network review failure invalidates prior inline approval', async t => {
  const f = await fixture(t)
  await f.enterReview(); f.state.createFailure = 'partial'; await f.apply()
  await f.recoverInline()
  f.state.repairFailure = 'stale'
  await f.apply(); await f.currentStep('Review & configure')
  await f.layout('Review changes')
  assert.equal(await f.page.getByText('Desired value', { exact: true }).count(), 0)
  assert.equal(await f.primary(applyLabel).count(), 0)
  f.state.reviewFailure = 'network'
  await f.primary('Review changes').click(); await f.waitIdle()
  await f.page.getByRole('alert').first().waitFor()
  await f.layout('Refresh review')
  assert.equal(await f.primary(applyLabel).count(), 0)
  assert.equal(await f.page.getByRole('dialog').count(), 0)
  assert.equal(f.publisherMutations().length, 2)
  f.state.reviewFailure = ''; f.state.repairFailure = ''
  await f.refreshReview(); await f.reviewValues(); await f.apply()
  await f.currentStep('Test connection')
  assert.equal(f.state.repairReview, 3)
})

test('review fetch failure clears an existing approval and requires fixed-footer Refresh review', async t => {
  const f = await fixture(t)
  await f.enterReview(); await f.reviewValues()
  f.state.reviewFailure = 'http'
  // This is the secondary refresh while a valid approval is still displayed.
  await f.footer.getByRole('button', { name: 'Refresh review', exact: true }).click()
  await f.waitIdle()
  await f.page.getByRole('alert').filter({ hasText: 'Fixture review unavailable' }).waitFor()
  await f.currentStep('Review & configure'); await f.layout('Refresh review')
  assert.equal(await f.primary(applyLabel).count(), 0)
  assert.equal(await f.page.getByText('Desired value', { exact: true }).count(), 0)
  assert.equal(f.publisherMutations().length, 0)
  f.state.reviewFailure = ''
  await f.refreshReview(); await f.reviewValues(); await f.apply()
  await f.currentStep('Test connection')
})

test('accepted apply without saved-state readback stays in review instead of claiming configured success', async t => {
  const f = await fixture(t)
  await f.enterReview(); f.state.statusFailure = true; await f.apply()
  await f.page.getByRole('alert').first().waitFor()
  await f.currentStep('Review & configure'); await f.layout('Refresh review')
  assert.equal(await f.primary(applyLabel).count(), 0)
  assert.equal(f.publisherMutations().length, 1)
  // Once readback is restored, the retained, configured connection is discovered;
  // do not issue another create or overwrite settings using the old approval.
  f.state.statusFailure = false
  await f.refreshReview(); await f.currentStep('Test connection')
  await f.layout('Check status')
  assert.equal(f.publisherMutations().length, 1)
})

test('optimistic apply response cannot advance when saved readback is still unconfigured', async t => {
  const f = await fixture(t)
  await f.enterReview(); f.state.optimisticCreate = true; await f.apply()
  await f.currentStep('Review & configure')
  await f.layout('Review changes')
  assert.equal(f.state.status.connections[0].configured, false)
  assert.equal(f.publisherMutations().length, 1)
})

test('320x560 existing-settings review and inline recovery preserve usable content and visible actions', async t => {
  const f = await fixture(t, { width: 320, height: 560 })
  const usable = async label => {
    await f.layout(label)
    assert.ok(await f.page.locator('.publisher-step-content').evaluate(el => el.clientHeight > 80), 'short phone retains a usable review panel')
  }
  await f.enterReview(); await f.reviewValues(); await usable(applyLabel)
  await screenshot(f.page, 'setup-review-short-phone')
  f.state.createFailure = 'partial'
  await f.apply(); await f.currentStep('Review & configure'); await usable('Review changes')
  await f.recoverInline(); await usable(applyLabel)
  await screenshot(f.page, 'setup-inline-recovery-short-phone')
  await f.apply(); await f.currentStep('Test connection'); await usable('Check status')
  assert.deepEqual(f.publisherMutations().map(call => call.path), ['/api/publishers', '/api/publishers/fixture-finance/repair'])
})

test('callback status, configured retry and Done are footer actions, never evidence of image publication', async t => {
  const f = await fixture(t)
  await f.enterReview(); await f.apply(); await f.currentStep('Test connection')
  await f.layout('Check status')
  const mutations = f.publisherMutations().length
  await f.primary('Check status').click(); await f.waitIdle()
  await f.page.getByText('Waiting for authenticated callback', { exact: true }).first().waitFor()
  assert.equal(f.publisherMutations().length, mutations, 'checking saved callback status is read-only')
  Object.assign(f.state.status.connections[0], { state: 'error', configured: true, error: 'Fixture connection callback timed out' })
  await f.primary('Check status').click(); await f.waitIdle()
  await f.layout('Retry connection test')
  await f.primary('Retry connection test').click(); await f.waitIdle()
  assert.equal(f.publisherMutations().at(-1).path, '/api/publishers/fixture-finance/test', 'configured error retries the connection test without rewriting configuration')
  await f.layout('Check status')
  Object.assign(f.state.status.connections[0], { state: 'verified', verifiedAt: '2026-11-18T10:00:00Z', error: '' })
  await f.primary('Check status').click(); await f.waitIdle()
  await f.layout('Done')
  await f.page.getByText('Connectivity verified', { exact: true }).first().waitFor()
  await f.page.getByText(/not a successful publication|no real image announcement|not.*image publication/i).first().waitFor()
  assert.equal(f.state.status.connections[0].lastPublication, undefined)
  assert.equal(await f.page.getByText('Image received', { exact: true }).count(), 0)
  await f.primary('Done').click()
  await f.page.getByRole('heading', { name: 'Publishers', exact: true }).waitFor()
  await f.page.getByText('No image received', { exact: true }).first().waitFor()
})
