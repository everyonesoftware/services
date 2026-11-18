// Browser-only fixtures. No live GitHub, Tailscale, or Services writes.
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from '../node_modules/vite/dist/node/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch({ ...(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : { channel: process.env.BROWSER_CHANNEL || 'msedge' }), headless: true })
const page = await browser.newPage({ viewport: { width: 1200, height: 1000 } })
const errors = []
page.on('pageerror', error => errors.push(error.message))
const repos = [
  { id: 14, fullName: 'Demo/Finance', defaultBranch: 'main', installationId: 8 },
  { id: 15, fullName: 'Demo/Services', defaultBranch: 'main', installationId: 8 },
]
const status = { available: true, github: { connected: true, name: 'Browser fixture App', permissionsChecked: true, variablesWrite: true, installUrl: 'https://github.com/apps/demo/installations/new' }, tailnet: { clientId: 'legacy-services-only', audience: 'legacy', tags: 'tag:legacy' }, tailnets: {}, connections: [], endpoint: 'https://services.example.test/api/images/published' }
const calls = []
let guidanceFailure = false
let customSubject = false
let saveFailure = false
let planFailure = false
await page.route('**/api/**', async route => {
  const req = route.request()
  const path = new URL(req.url()).pathname
  const body = req.method() === 'GET' ? undefined : req.postDataJSON()
  calls.push({ path, method: req.method(), body })
  if (path === '/api/auth/status') return route.fulfill({ json: { setupRequired: false } })
  if (path === '/api/auth/me') return route.fulfill({ json: { user: { id: 'fixture-admin', username: 'Browser fixture', role: 'admin' } } })
  if (path === '/api/publishers') {
    if (req.method() === 'POST') {
      const connection = { id: 'fixture-publisher', state: 'awaiting_callback', repository: body.repository, imageRepository: body.imageRepository, createdAt: '2026-01-01T00:00:00Z' }
      status.connections = [connection]
      return route.fulfill({ json: { connection } })
    }
    assert.equal(req.method(), 'GET')
    return route.fulfill({ json: status })
  }
  if (path === '/api/publishers/repositories') return route.fulfill({ json: { repositories: repos } })
  if (path === '/api/publishers/tailnet/guidance') {
    if (guidanceFailure) return route.fulfill({ status: 502, body: 'Fixture identity lookup failed' })
    return route.fulfill({ json: { issuer: 'https://token.actions.githubusercontent.com', subject: customSubject ? '' : `repo:Demo@42/${body.repository.fullName.split('/')[1]}@${body.repository.id}:ref:refs/heads/main`, ...(customSubject ? { subjectError: 'Custom claims require manual review' } : {}), tailnet: status.tailnets[body.repository.id] || { clientId: '', audience: '', tags: '' } } })
  }
  if (path === '/api/publishers/tailnet') {
    if (saveFailure) return route.fulfill({ status: 409, body: 'Fixture credential conflict' })
    const { repository, ...tailnet } = body
    status.tailnets[repository.id] = tailnet
    return route.fulfill({ json: status })
  }
  if (path === '/api/publishers/plan') {
    if (planFailure) return route.fulfill({ status: 502, body: 'Fixture plan failed after save' })
    return route.fulfill({ json: { supported: true, problems: [], repository: body.repository, imageRepository: body.imageRepository, approval: 'fixture-approval', secretName: 'SERVICES_PUBLISH_TOKEN', variables: { TAILSCALE_CLIENT_ID: status.tailnets[body.repository.id].clientId } } })
  }
  // Non-publisher dashboard APIs are read-only browser startup fixtures.
  if (path === '/api/projects') return route.fulfill({ json: { projects: [] } })
  if (path === '/api/monitor') return route.fulfill({ json: { connected: true, incidents: [], events: [] } })
  if (path === '/api/operations') return route.fulfill({ json: { operations: [] } })
  if (path === '/api/icons') return route.fulfill({ json: { available: false, icons: [] } })
  throw new Error(`Unexpected request ${req.method()} ${path}`)
})
const button = name => page.getByRole('button', { name, exact: true })
const primary = () => page.locator('.publisher-footer .primary')
const checkedScrollSteps = new Set()
const checkIndependentScroll = async step => {
  assert.equal(await page.locator('.publisher-step-content').evaluate(el => el.scrollTop), 0, `step ${step}: new step starts at the top`)
  for (const viewport of [{ width: 1440, height: 900 }, { width: 1200, height: 600 }, { width: 390, height: 800 }, { width: 390, height: 600 }]) {
    await page.setViewportSize(viewport)
    // Force long content on every real step, without changing provider data.
    await page.locator('.publisher-step-content').evaluate(el => {
      const filler = document.createElement('div')
      filler.id = 'scroll-layout-fixture'
      filler.style.height = '1600px'
      el.append(filler)
      el.scrollTop = 0
    })
    const before = await page.evaluate(() => {
      const box = selector => { const { x, y, width, height } = document.querySelector(selector).getBoundingClientRect(); return { x, y, width, height } }
      return { steps: box('.publisher-steps'), footer: box('.publisher-footer'), heading: box('.page-heading') }
    })
    const content = page.locator('.publisher-step-content')
    assert.ok(await content.evaluate(el => el.clientHeight > 80 && el.scrollHeight > el.clientHeight), `step ${step}: bounded right panel at ${viewport.width}x${viewport.height}`)
    await content.hover()
    await page.mouse.wheel(0, 500)
    await page.waitForFunction(() => document.querySelector('.publisher-step-content').scrollTop > 0, undefined, { timeout: 3000 })
    await content.evaluate(el => { el.scrollTop = 0 })
    await page.getByRole('region', { name: 'Publisher step content', exact: true }).focus()
    await page.keyboard.press('PageDown')
    await page.waitForFunction(() => document.querySelector('.publisher-step-content').scrollTop > 0, undefined, { timeout: 3000 })
    const after = await page.evaluate(() => {
      const box = selector => { const { x, y, width, height } = document.querySelector(selector).getBoundingClientRect(); return { x, y, width, height } }
      const outer = document.querySelector('.app-content')
      const content = document.querySelector('.publisher-step-content')
      return { steps: box('.publisher-steps'), footer: box('.publisher-footer'), heading: box('.page-heading'), outerScroll: outer.scrollTop, outerOverflow: outer.scrollHeight - outer.clientHeight, documentOverflow: document.documentElement.scrollHeight - innerHeight, horizontalOverflow: document.documentElement.scrollWidth - innerWidth, contentOverflow: content.scrollWidth - content.clientWidth }
    })
    assert.deepEqual(after.steps, before.steps, `step ${step}: step navigation must not move`)
    assert.deepEqual(after.footer, before.footer, `step ${step}: actions must not move`)
    assert.deepEqual(after.heading, before.heading)
    assert.equal(after.outerScroll, 0)
    for (const name of ['outerOverflow', 'documentOverflow', 'horizontalOverflow', 'contentOverflow']) assert.ok(after[name] <= 1, `step ${step}: ${name} = ${after[name]}`)
    assert.ok(before.footer.y + before.footer.height <= viewport.height, 'actions remain within viewport')
    await content.evaluate(el => { document.getElementById('scroll-layout-fixture').remove(); el.scrollTop = 0 })
  }
  checkedScrollSteps.add(step)
  await page.setViewportSize({ width: 1200, height: 1000 })
}
const enterRepository = async name => {
  await button('Select repository →').click()
  if (!checkedScrollSteps.has(1)) await checkIndependentScroll(1)
  await page.getByRole('radio', { name: new RegExp(name) }).check()
  await button('Set up Tailscale →').click()
}
const fillCredential = async () => {
  await page.getByLabel('Client ID', { exact: true }).fill('finance-client')
  await page.getByLabel('Audience', { exact: true }).fill('api.tailscale.com/finance-client')
  await page.getByLabel('Runner tags', { exact: true }).fill('tag:services-publisher')
}
try {
  await page.goto(`${server.resolvedUrls.local[0]}#publishers`)
  await button('Connect repository').click()
  await page.getByRole('heading', { name: 'Connect your GitHub App' }).waitFor()
  await checkIndependentScroll(0)
  assert.equal(await page.getByLabel('Client ID', { exact: true }).count(), 0)
  await enterRepository('Demo/Finance')
  await page.getByText('repo:Demo@42/Finance@14:ref:refs/heads/main', { exact: true }).waitFor()
  assert.equal(await page.getByLabel('Client ID', { exact: true }).inputValue(), '', 'global Services identity must not prefill Finance')
  await checkIndependentScroll(2)
  assert.equal(await primary().isDisabled(), true)
  assert.equal(await page.locator('a[href="https://login.tailscale.com/admin/settings/trust-credentials"]').count(), 1)
  const advanced = page.getByText('Advanced identity guidance & troubleshooting', { exact: true })
  assert.equal(await page.locator('.publisher-identity-details').getAttribute('open'), null)
  const expectedCopies = [['description', 'Demo_Finance'], ['issuer URL', 'https://token.actions.githubusercontent.com'], ['subject', 'repo:Demo@42/Finance@14:ref:refs/heads/main'], ['runner tag', 'tag:services-publisher']]
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await button('Copy description').click()
  await button('Copied description').waitFor()
  assert.equal(await page.evaluate(() => navigator.clipboard.readText()), 'Demo_Finance')
  await button('Copy description').waitFor({ timeout: 4000 })
  await page.evaluate(() => { window.realClipboard = navigator.clipboard; Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined }) })
  await button('Copy issuer URL').click()
  await button('Copied issuer URL').waitFor()
  assert.equal(await page.evaluate(() => window.realClipboard.readText()), expectedCopies[1][1], 'legacy copy fallback writes the actual clipboard')
  await button('Copy issuer URL').waitFor({ timeout: 4000 })
  await page.evaluate(() => { window.copiedValues = []; Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async value => { window.copiedValues.push(value) } } }) })
  for (const [label, value] of expectedCopies) {
    const copy = button(`Copy ${label}`)
    await copy.click()
    await page.getByRole('button', { name: `Copied ${label}`, exact: true }).waitFor()
    assert.equal(await page.evaluate(() => window.copiedValues.at(-1)), value)
    assert.equal(await page.locator('.publisher-copy-value[data-copied="true"] svg').count() > 0, true)
  }
  await button('Copy description').waitFor({ timeout: 4000 })
  await page.evaluate(() => { navigator.clipboard.writeText = async () => { throw new Error('denied') } })
  await button('Copy description').click()
  await page.getByRole('alert').getByText('Could not copy. Select and copy the text manually.', { exact: true }).waitFor()
  assert.equal(await button('Copied description').count(), 0)
  await page.evaluate(() => { navigator.clipboard.writeText = async value => { window.copiedValues.push(value) } })
  await button('Copy description').focus()
  await page.keyboard.press('Enter')
  await button('Copied description').waitFor()
  await advanced.click()
  const steps = await page.locator('.publisher-steps li:not(.publisher-aside)').allTextContents()
  assert.equal(steps.length, 5)
  assert.ok(steps[0].includes('GitHub App') && steps[2].includes('Tailscale'))
  await fillCredential()
  assert.equal(await primary().isEnabled(), true)
  await button('Back').click()
  await button('Set up Tailscale →').click()
  await page.getByText('repo:Demo@42/Finance@14:ref:refs/heads/main', { exact: true }).waitFor()
  assert.equal(await page.getByLabel('Client ID', { exact: true }).inputValue(), 'finance-client', 'unsaved same-repository draft survives Back/forward')
  await button('Refresh GitHub identity').click()
  await page.getByText('repo:Demo@42/Finance@14:ref:refs/heads/main', { exact: true }).waitFor()
  assert.equal(await page.getByLabel('Audience', { exact: true }).inputValue(), 'api.tailscale.com/finance-client', 'identity refresh must not discard unsaved credential')
  if (process.env.TAILNET_DESKTOP_SCREENSHOT) await page.screenshot({ path: process.env.TAILNET_DESKTOP_SCREENSHOT, fullPage: true })
  await page.setViewportSize({ width: 390, height: 800 })
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), '390px document must not overflow')
  assert.ok(await page.locator('.publisher-tailnet').evaluate(el => el.scrollWidth <= el.clientWidth))
  if (process.env.TAILNET_MOBILE_SCREENSHOT) await page.screenshot({ path: process.env.TAILNET_MOBILE_SCREENSHOT, fullPage: true })
  // Failed saves remain on the editable step and cannot request a plan.
  saveFailure = true
  await primary().click()
  await page.getByRole('alert').getByText('Fixture credential conflict', { exact: true }).waitFor()
  assert.equal(calls.filter(c => c.path === '/api/publishers/plan').length, 0)
  saveFailure = false
  planFailure = true
  await primary().click()
  await page.getByRole('alert').getByText('Fixture plan failed after save', { exact: true }).waitFor()
  await page.locator('.publisher-steps li[aria-current="step"]').filter({ hasText: 'Review & configure' }).waitFor()
  assert.equal(await primary().innerText(), 'Refresh review', 'failed inspection offers review without another credential save')
  await button('Back').click()
  assert.equal(await page.getByLabel('Client ID', { exact: true }).inputValue(), 'finance-client')
  planFailure = false
  await primary().click()
  await page.getByRole('heading', { name: 'Review publisher setup' }).waitFor()
  await checkIndependentScroll(3)
  const saves = calls.filter(c => c.path === '/api/publishers/tailnet')
  assert.deepEqual(saves.at(-1).body, { repository: repos[0], clientId: 'finance-client', audience: 'api.tailscale.com/finance-client', tags: 'tag:services-publisher' })
  assert.equal(status.tailnet.clientId, 'legacy-services-only')
  // Back preserves this repository's inputs; a different selection does not inherit them.
  await button('Back').click()
  assert.equal(await page.getByLabel('Client ID', { exact: true }).inputValue(), 'finance-client')
  await button('Back').click()
  await page.getByRole('radio', { name: /Demo\/Services/ }).check()
  guidanceFailure = true
  await button('Set up Tailscale →').click()
  await page.getByRole('alert').getByText('Fixture identity lookup failed', { exact: true }).waitFor()
  assert.equal(await primary().isDisabled(), true)
  assert.equal(await page.getByLabel('Client ID', { exact: true }).inputValue(), '')
  guidanceFailure = false
  customSubject = true
  await button('Refresh GitHub identity').click()
  await page.getByRole('alert').getByText('Custom claims require manual review', { exact: true }).waitFor()
  assert.equal(await button('Copy subject').count(), 0)
  await fillCredential()
  assert.equal(await primary().isDisabled(), true)
  customSubject = false
  await button('Refresh GitHub identity').click()
  await page.getByText('repo:Demo@42/Services@15:ref:refs/heads/main', { exact: true }).waitFor()
  assert.equal(await page.getByLabel('Client ID', { exact: true }).inputValue(), 'finance-client', 'refresh preserves explicitly entered draft, not a different repository saved value')
  const savesBeforeCancel = calls.filter(c => c.path === '/api/publishers/tailnet').length
  await button('Cancel setup').click()
  await page.getByRole('heading', { name: 'Publishers', exact: true }).waitFor()
  assert.equal(calls.filter(c => c.path === '/api/publishers/tailnet').length, savesBeforeCancel)
  // Re-entering the saved repository restores only its own credential.
  await button('Connect repository').click()
  await enterRepository('Demo/Finance')
  await page.getByText('repo:Demo@42/Finance@14:ref:refs/heads/main', { exact: true }).waitFor()
  assert.equal(await page.getByLabel('Client ID', { exact: true }).inputValue(), 'finance-client')
  await primary().click()
  await page.getByRole('heading', { name: 'Review publisher setup' }).waitFor()
  await primary().click()
  await page.locator('.publisher-steps li[aria-current="step"]').filter({ hasText: 'Test connection' }).waitFor()
  await checkIndependentScroll(4)
  assert.deepEqual([...checkedScrollSteps].sort(), [0, 1, 2, 3, 4])
  assert.deepEqual(errors, [])
  console.log('PASS: independent wheel/keyboard scrolling with fixed step navigation, heading and actions across all five steps at desktop, short desktop, phone and short phone sizes; new-step scroll reset; actual clipboard and legacy fallback; copy success/failure/retry; identity safeguards; repository-bound save/readback; cancellation; saved-state restore. Browser fixtures only, no live provider writes.')
} finally { await browser.close(); await server.close() }
