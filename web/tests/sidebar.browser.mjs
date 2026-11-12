// Browser-only fixtures. No Services backend, Docker, GitHub, or live writes.
// PLAYWRIGHT_MODULE may be an external file:// URL; no test dependency is added.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from '../node_modules/vite/dist/node/index.js'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const screenshotDirectory = process.env.SIDEBAR_SCREENSHOT_DIR
if (screenshotDirectory) await mkdir(screenshotDirectory, { recursive: true })
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
let browser
const failures = []
const user = { id: 'fixture-admin', username: 'Browser fixture', role: 'admin' }
const routes = ['monitor', 'activity', 'publishers', 'settings']
const labels = ['Monitor', 'Activity', 'Publishers', 'Settings']
const wait = async (predicate, message) => {
  for (let attempt = 0; attempt < 100; attempt++) {
    if (await predicate()) return
    await new Promise(resolve => setTimeout(resolve, 30))
  }
  assert.fail(message)
}

async function exercise(viewport, name) {
  const context = await browser.newContext({ viewport, serviceWorkers: 'block' })
  const page = await context.newPage()
  page.setDefaultTimeout(4000)
  const errors = [], unexpected = [], mutations = []
  let signedIn = true, logoutAttempts = 0
  page.on('pageerror', error => errors.push(error.message))
  const fixtureUsers = [user, ...Array.from({ length: 24 }, (_, index) => ({ id: `other-${index}`, username: `Fixture administrator ${index + 1}`, role: 'admin' }))]
  const key = { id: 'fixture-key', name: 'Browser fixture read key', prefix: 'svc_fixture', scopes: ['services:read'], repositories: [], createdAt: '2026-01-01T00:00:00Z' }
  const projects = Array.from({ length: 32 }, (_, index) => ({
    name: `fixture-project-${String(index + 1).padStart(2, '0')}`, status: 'healthy',
    containers: [{ id: `fixture-container-${index}`, name: `fixture-project-${index + 1}-web-1`, service: 'web', project: `fixture-project-${String(index + 1).padStart(2, '0')}`, image: 'nginx:stable', state: 'running', health: 'healthy', updateAvailable: false }],
  }))
  await context.route('**/api/**', async route => {
    const request = route.request(), path = new URL(request.url()).pathname, method = request.method()
    if (!['GET', 'HEAD'].includes(method)) mutations.push(`${method} ${path}`)
    if (path === '/api/auth/logout' && method === 'POST') {
      logoutAttempts++
      if (logoutAttempts === 1) return route.fulfill({ status: 503, json: { error: 'Fixture signout unavailable; try again.' } })
      signedIn = false
      return route.fulfill({ status: 204 })
    }
    const fixtures = {
      '/api/auth/status': { setupRequired: false },
      '/api/auth/me': { user },
      '/api/auth/users': { users: fixtureUsers },
      '/api/auth/keys': { keys: [key] },
      '/api/projects': { projects },
      '/api/monitor': { connected: true, lastChecked: new Date().toISOString(), error: null, incidents: [], events: [], notificationConfigured: false },
      '/api/operations': { operations: [] },
      '/api/icons': { available: false, icons: [] },
      '/api/publishers': { available: true, github: { connected: true, name: 'Browser fixture App', permissionsChecked: true, variablesWrite: true }, tailnet: { clientId: '', audience: '', tags: '' }, connections: [], endpoint: 'https://services.example.test/api/images/published' },
    }
    if (method === 'GET' && Object.hasOwn(fixtures, path)) {
      if (path === '/api/auth/me' && !signedIn) return route.fulfill({ status: 401, json: { error: 'Not signed in' } })
      return route.fulfill({ json: fixtures[path] })
    }
    unexpected.push(`${method} ${path}`)
    return route.fulfill({ status: 501, json: { error: `Unexpected browser fixture request: ${method} ${path}` } })
  })
  const sidebar = page.locator('.app-shell > .app-sidebar')
  const content = page.locator('.app-shell > .app-content')
  const nav = () => sidebar.getByRole('navigation', { name: 'Main navigation', exact: true })
  const screenshot = suffix => screenshotDirectory ? page.screenshot({ path: join(screenshotDirectory, `${name}-${suffix}.png`) }) : Promise.resolve()
  const marker = `sidebar-fixture-${name}`
  const mark = () => page.evaluate(value => { window.__sidebarRegressionMarker = value }, marker)
  const markerSurvives = async () => assert.equal(await page.evaluate(() => window.__sidebarRegressionMarker), marker, 'SPA navigation must not reload the document')
  const assertLayout = async () => {
    const geometry = await page.evaluate(() => {
      const side = document.querySelector('.app-sidebar'), body = document.querySelector('.app-content')
      const s = side.getBoundingClientRect(), c = body.getBoundingClientRect()
      return { width: innerWidth, height: innerHeight, scrollWidth: document.documentElement.scrollWidth, scrollHeight: document.documentElement.scrollHeight, contentScrollWidth: body.scrollWidth, contentWidth: body.clientWidth, side: { x: s.x, y: s.y, width: s.width, height: s.height }, contentX: c.x, color: getComputedStyle(side).backgroundColor }
    })
    assert.ok(geometry.scrollWidth <= viewport.width + 1, `${name}: document must not overflow horizontally`)
    assert.ok(geometry.scrollHeight <= viewport.height + 1, `${name}: only the content pane should scroll vertically, got ${geometry.scrollHeight}`)
    assert.ok(geometry.contentScrollWidth <= geometry.contentWidth + 1, `${name}: content must not overflow horizontally`)
    assert.ok(Math.abs(geometry.side.x) <= 1 && Math.abs(geometry.side.y) <= 1, 'Sidebar starts at viewport top-left')
    assert.ok(Math.abs(geometry.side.height - viewport.height) <= 1, 'Sidebar fills viewport height')
    assert.ok(Math.abs(geometry.side.width - (name === 'desktop' ? 208 : 64)) <= 1, 'Desktop sidebar / mobile icon rail has approved width')
    assert.ok(geometry.contentX >= geometry.side.width - 1, 'Content must not overlap the sidebar')
    assert.equal(geometry.color, 'rgb(29, 48, 43)', 'Approved dark green sidebar')
    const signout = sidebar.locator('.sidebar-signout')
    assert.equal(await signout.isVisible(), true, 'Sidebar signout remains visible')
    const box = await signout.boundingBox()
    assert.ok(box.y > viewport.height * 0.7 && box.y + box.height <= viewport.height + 1, 'Signout is anchored at sidebar bottom')
    assert.equal((await sidebar.locator('.sidebar-user').textContent()).trim(), user.username)
  }
  const assertPage = async route => {
    await sidebar.waitFor({ state: 'visible' })
    const heading = content.getByRole('heading', { level: 1 })
    await wait(async () => {
      const text = (await heading.allTextContents()).map(value => value.trim())
      return text.length === 1 && (route === 'publishers' ? /^(Image publishers|Publishers)$/.test(text[0]) : text[0] === (route === 'monitor' ? 'Services' : labels[routes.indexOf(route)]))
    }, `${route}: expected its page title inside .app-content`)
    assert.equal(await page.evaluate(() => location.hash), `#${route}`)
    const links = nav().getByRole('link')
    assert.deepEqual(await links.evaluateAll(elements => elements.map(element => element.getAttribute('href'))), routes.slice(0, 3).map(value => `#${value}`), 'Main navigation excludes Settings')
    assert.deepEqual(await links.evaluateAll((elements, expected) => elements.map((element, index) => [...element.querySelectorAll('span')].some(span => span.textContent.trim() === expected[index])), labels.slice(0, 3)), [true, true, true], 'Main links retain their text labels')
    assert.equal(await sidebar.locator('[aria-current]').count(), 1, 'Exactly one sidebar link has aria-current')
    assert.equal(await sidebar.locator('[aria-current]').getAttribute('href'), `#${route}`)
    assert.equal(await sidebar.locator('[aria-current]').getAttribute('aria-current'), 'page')
    const gear = sidebar.locator('.sidebar-profile').getByRole('link', { name: 'Settings', exact: true })
    assert.equal(await gear.isVisible(), true)
    const gearBox = await gear.boundingBox()
    assert.ok(gearBox.width >= 44 && gearBox.height >= 44, 'Settings has a touch-sized target')
    for (const [index, label] of labels.entries()) {
      const link = sidebar.getByRole('link', { name: label, exact: true })
      assert.equal(await link.isVisible(), true, `${label} is accessible at ${name} width`)
      assert.equal(await link.getAttribute('href'), `#${routes[index]}`)
      if (name !== 'desktop') {
        assert.equal(await link.getAttribute('title'), label, 'Icon-only links retain a discoverable tooltip')
        assert.ok(await link.locator('svg, img, [role="img"]').count() > 0, 'Phone rail retains navigation icons')
      }
    }
    await assertLayout()
  }
  const go = async route => {
    await sidebar.getByRole('link', { name: labels[routes.indexOf(route)], exact: true }).click()
    await assertPage(route)
    await markerSurvives()
  }
  try {
    await page.goto(`${server.resolvedUrls.local[0]}#monitor`)
    await assertPage('monitor')
    await mark()
    // Click every link in the approved order, including the already-current link.
    for (const route of routes) { await go(route); await screenshot(route) }
    // Navigation only: no add-user, key, publisher, or service mutation is permitted.
    assert.deepEqual(mutations, [])
    await page.goBack(); await assertPage('publishers'); await markerSurvives()
    await page.goBack(); await assertPage('activity'); await markerSurvives()
    await page.goBack(); await assertPage('monitor'); await markerSurvives()
    await page.goForward(); await assertPage('activity'); await markerSurvives()
    await page.goForward(); await assertPage('publishers'); await markerSurvives()
    await page.goForward(); await assertPage('settings'); await markerSurvives()

    // Direct entries and full reloads must restore the correct page and active link.
    for (const route of routes) {
      await page.goto(`${server.resolvedUrls.local[0]}#${route}`)
      await assertPage(route)
      await mark()
      await page.reload()
      await assertPage(route)
      assert.equal(await page.evaluate(() => window.__sidebarRegressionMarker), undefined, 'Reload assertion really replaced the document')
    }
    // Preserve saved Accounts deep links while new navigation uses Settings.
    await page.goto(`${server.resolvedUrls.local[0]}#accounts`)
    await content.getByRole('heading', { level: 1, name: 'Settings', exact: true }).waitFor()
    await page.reload()
    await content.getByRole('heading', { level: 1, name: 'Settings', exact: true }).waitFor()
    await sidebar.getByRole('link', { name: 'Settings', exact: true }).click()
    await assertPage('settings')
    await mark()
    const sections = content.getByRole('navigation', { name: 'Settings sections', exact: true })
    const sectionLabels = ['Administrators', 'API keys', 'Password']
    assert.equal(await sections.count(), 1, 'Accounts needs navigation aria-label="Account sections" with Administrators / API keys / Password switch buttons')
    assert.deepEqual(await sections.getByRole('button').allTextContents().then(text => text.map(value => value.trim())), ['Updates', ...sectionLabels])
    const cards = ['admins-title', 'keys-title', 'password-title'].map(id => content.locator(`section[aria-labelledby="${id}"]`))
    const selectSection = async index => {
      await sections.getByRole('button', { name: sectionLabels[index], exact: true }).click()
      await wait(async () => await sections.getByRole('button', { name: sectionLabels[index], exact: true }).getAttribute('aria-pressed') === 'true', 'Selected account section must be pressed')
      for (let other = 0; other < cards.length; other++) {
        assert.equal(await cards[other].count(), 1, 'Account forms stay mounted')
        assert.equal(await cards[other].isVisible(), other === index, 'Only selected account section is visible')
        assert.equal(await cards[other].evaluate(element => element.hidden), other !== index, 'Inactive account sections use hidden attribute')
        assert.equal(await sections.getByRole('button', { name: sectionLabels[other], exact: true }).getAttribute('aria-pressed'), String(other === index))
      }
      await markerSurvives()
      await assertLayout()
    }
    assert.equal(await sections.getByRole('button', { name: 'Updates', exact: true }).getAttribute('aria-pressed'), 'true')
    await selectSection(0)
    await cards[0].locator('strong').getByText(user.username, { exact: true }).waitFor()
    assert.equal(await cards[0].getByRole('button', { name: `Delete ${user.username}`, exact: true }).isDisabled(), true, 'Cannot delete signed-in administrator')
    const adminForm = cards[0].locator('form')
    await adminForm.getByLabel('Username', { exact: true }).fill('Unsubmitted fixture admin')
    await adminForm.getByLabel('Password', { exact: true }).fill('Fixture-only password 123!')
    assert.equal(await adminForm.getByRole('button', { name: 'Add administrator', exact: true }).isEnabled(), true)
    assert.equal(await adminForm.evaluate(form => form.checkValidity()), true)
    await adminForm.evaluate(form => { form.dataset.sidebarPersistence = 'mounted' })
    await selectSection(1)
    await cards[1].locator('strong').getByText(key.name, { exact: true }).waitFor()
    assert.equal(await cards[1].getByRole('button', { name: `Revoke ${key.name}`, exact: true }).isEnabled(), true)
    const keyForm = cards[1].locator('form')
    await keyForm.getByLabel('Key name', { exact: true }).fill('Unsubmitted fixture key')
    await keyForm.getByLabel('services:read', { exact: true }).check()
    await keyForm.getByLabel('publications:write', { exact: true }).check()
    const repositories = keyForm.getByLabel('Exact image repositories', { exact: true })
    assert.equal(await repositories.getAttribute('required'), '')
    await repositories.fill('ghcr.io/fixture/services')
    await keyForm.getByLabel('Expiry (optional, local time)', { exact: true }).fill('2030-01-01T12:00')
    assert.equal(await keyForm.evaluate(form => form.checkValidity()), true)
    assert.equal(await keyForm.getByRole('button', { name: 'Create API key', exact: true }).isEnabled(), true)
    await selectSection(2)
    const passwordForm = cards[2].locator('form')
    await passwordForm.getByLabel('New password', { exact: true }).fill('Fixture-only replacement 123!')
    assert.equal(await passwordForm.evaluate(form => form.checkValidity()), true)
    assert.equal(await passwordForm.getByRole('button', { name: 'Change password', exact: true }).isEnabled(), true)
    await screenshot('account-password')
    await selectSection(0)
    assert.equal(await adminForm.getAttribute('data-sidebar-persistence'), 'mounted', 'Switching account sections preserves the real form node')
    assert.equal(await adminForm.getByLabel('Username', { exact: true }).inputValue(), 'Unsubmitted fixture admin')
    await selectSection(1)
    assert.equal(await keyForm.getByLabel('Key name', { exact: true }).inputValue(), 'Unsubmitted fixture key')
    await screenshot('account-keys')
    assert.deepEqual(mutations, [], 'Filling forms and switching sections must not submit them')

    await sidebar.getByRole('link', { name: 'Services home', exact: true }).click()
    await assertPage('monitor'); await markerSurvives()
    await page.locator('.project-group').first().waitFor()
    const before = await sidebar.boundingBox()
    const scroll = await content.evaluate(element => {
      const max = element.scrollHeight - element.clientHeight
      element.scrollTop = Math.min(600, max)
      return { max, top: element.scrollTop }
    })
    assert.ok(scroll.max > 0 && scroll.top > 0, 'Fixture content scrolls independently inside .app-content')
    const after = await sidebar.boundingBox()
    assert.deepEqual(after, before, 'Scrolling app content must not move the viewport-anchored sidebar')
    assert.equal(await page.evaluate(() => window.scrollY), 0, 'Document remains stationary when app content scrolls')
    await assertLayout(); await screenshot('scrolled')
    assert.deepEqual(mutations, [], 'Navigation and logo must never mutate backend state')

    await sidebar.locator('.sidebar-signout').click()
    await page.getByRole('alert').getByText('Fixture signout unavailable; try again.', { exact: true }).waitFor()
    await assertPage('monitor')
    const alertBox = await page.getByRole('alert').boundingBox()
    assert.ok(alertBox.y >= 0 && alertBox.y + alertBox.height <= viewport.height, 'Signout failure stays in view even after content was scrolled')
    assert.equal(signedIn, true, 'Rejected logout preserves fixture session')
    assert.equal(await page.getByRole('heading', { name: 'Sign in', exact: true }).count(), 0)
    await screenshot('signout-failure')
    await sidebar.locator('.sidebar-signout').click()
    await page.getByRole('heading', { name: 'Sign in', exact: true }).waitFor()
    assert.equal(await sidebar.count(), 0, 'Successful logout removes authenticated shell')
    assert.equal(await page.getByLabel('Username', { exact: true }).isVisible(), true)
    assert.equal(await page.getByLabel('Password', { exact: true }).isVisible(), true)
    assert.equal(signedIn, false)
    assert.equal(logoutAttempts, 2)
    await page.reload()
    await page.getByRole('heading', { name: 'Sign in', exact: true }).waitFor()
    assert.deepEqual(mutations, ['POST /api/auth/logout', 'POST /api/auth/logout'])
    assert.deepEqual(unexpected, [], 'Every API request must match a browser fixture')
    assert.deepEqual(errors, [], 'No uncaught browser errors')
    await screenshot('signed-out')
    console.log(`PASS ${name} ${viewport.width}x${viewport.height}: navigation/history/deep links, account forms, scrolling, and signout failure/success; no live writes`)
  } catch (error) {
    await screenshot('failure').catch(() => {})
    console.error(`FAIL ${name} ${viewport.width}x${viewport.height}: ${error.stack}`)
    if (unexpected.length) console.error('Unexpected fixture requests:', unexpected)
    failures.push({ viewport: name, message: error.message })
  } finally { await context.close() }
}

try {
  await server.listen()
  browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || 'msedge', headless: true })
  await exercise({ width: 1440, height: 1000 }, 'desktop')
  await exercise({ width: 390, height: 844 }, 'phone')
  await exercise({ width: 320, height: 560 }, 'small-phone')
  assert.deepEqual(failures, [], 'Focused Console sidebar browser regressions')
  if (screenshotDirectory) console.log(`Screenshots: ${screenshotDirectory}`)
} finally {
  if (browser) await browser.close()
  await server.close()
}
