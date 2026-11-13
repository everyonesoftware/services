// Isolated actual-app browser fixture: no Docker or production API writes.
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from '../node_modules/vite/dist/node/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false } })
await server.listen()
const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || 'msedge', headless: true })
const page = await browser.newPage({ viewport: { width: 1200, height: 900 } })
let submissions = 0
let reject = true
let operations = []
let health = 'healthy'
let service = 'web'
let connected = true
let releaseProjects
let projectGate = Promise.resolve()
const errors = []
page.on('pageerror', error => errors.push(error.message))
await page.route('**/api/**', async route => {
  const path = new URL(route.request().url()).pathname
  if (path === '/api/auth/status') return route.fulfill({ json: { setupRequired: false } })
  if (path === '/api/auth/me') return route.fulfill({ json: { user: { id: 'fixture', username: 'Fixture', role: 'admin' } } })
  if (path === '/api/projects/media/update') {
    submissions++
    await new Promise(resolve => setTimeout(resolve, 200))
    return reject ? route.fulfill({ status: 409, body: 'Fixture rejection' }) : route.fulfill({ json: { id: 'fixture-operation', project: 'media', action: 'update', state: 'queued', messages: [] } })
  }
  if (path === '/api/projects') {
    await projectGate
    return route.fulfill({ json: { projects: [{ name: 'media', status: 'healthy', containers: [{ id: 'media-web-1', name: 'media-web-1', service, project: 'media', image: 'nginx:stable', state: 'running', health, updateAvailable: true, currentDigest: 'sha256:old', proposedDigest: 'sha256:new' }] }] } })
  }
  if (path === '/api/monitor') return route.fulfill({ json: { connected, lastChecked: new Date().toISOString(), incidents: [], events: [] } })
  if (path === '/api/operations') return route.fulfill({ json: { operations } })
  if (path === '/api/icons') return route.fulfill({ json: { available: true, icons: [] } })
  return route.fulfill({ status: 404 })
})
try {
  await page.goto(server.resolvedUrls.local[0])
  await page.locator('.project-group').waitFor()
  const monitorURL = page.url()
  assert.equal(await page.locator('article.project-group > div.group-summary.project-summary').count(), 1)
  assert.equal(await page.locator('a.project-link').getAttribute('href'), '#service/media')
  assert.equal(await page.locator('a.project-link button').count(), 0)
  const count = page.locator('.group-summary').getByRole('button', { name: 'Review 1 update', exact: true })
  const assertUpdateBeforeArrow = async () => {
    const button = await count.boundingBox()
    const arrow = await page.locator('.project-summary .navigation-arrow').boundingBox()
    assert.ok(button.x + button.width < arrow.x, 'Update button sits to the left of the navigation chevron')
    assert.ok(Math.abs(button.y + button.height / 2 - arrow.y - arrow.height / 2) < 2, 'Update button and chevron remain vertically aligned')
  }
  await assertUpdateBeforeArrow()
  if (process.env.PROJECT_UPDATE_SCREENSHOT) await page.screenshot({ path: process.env.PROJECT_UPDATE_SCREENSHOT.replace(/\.png$/, '-desktop.png') })
  assert.equal(await count.count(), 1, 'The summary update count must be an actionable review button')
  assert.equal(await page.locator('.project-group details, .project-group .container-row').count(), 0)
  assert.equal(page.url(), monitorURL, 'Independent actions do not navigate')
  await count.click()
  assert.equal(await page.locator('.project-group details, .project-group .container-row').count(), 0)
  assert.equal(page.url(), monitorURL, 'Independent actions do not navigate')
  assert.equal(submissions, 0, 'Review never submits without approval')
  assert.equal(await page.getByRole('button', { name: 'Cancel', exact: true }).evaluate(el => el === document.activeElement), true)
  await page.keyboard.press('Escape')
  assert.equal(await count.evaluate(el => el === document.activeElement), true)
  await count.focus()
  await page.keyboard.press('Enter')
  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  assert.equal(await count.evaluate(el => el === document.activeElement), true)
  assert.equal(await page.locator('.project-group details, .project-group .container-row').count(), 0)
  assert.equal(page.url(), monitorURL, 'Independent actions do not navigate')
  await count.focus()
  await page.keyboard.press('Space')
  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  assert.equal(await count.evaluate(el => el === document.activeElement), true)
  assert.equal(await page.locator('.project-group details, .project-group .container-row').count(), 0)
  assert.equal(page.url(), monitorURL, 'Independent actions do not navigate')
  console.log('PASS: compact service summary actionable count, no navigation, explicit approval, Cancel autofocus, Escape/Cancel focus restoration.')
  for (const width of [390, 320]) {
    await page.setViewportSize({ width, height: 640 })
    await assertUpdateBeforeArrow()
    assert.equal(await page.locator('.project-summary .identity-name').evaluate(el => getComputedStyle(el).fontSize), '18px')
    assert.equal(await count.evaluate(el => getComputedStyle(el).fontSize), '16px')
    assert.ok((await count.boundingBox()).height >= 48)
    assert.equal(await page.locator('.project-summary-counts').evaluate(el => getComputedStyle(el).fontSize), '14px')
    assert.equal(await page.locator('.project-summary-counts').innerText(), '1 container')
    assert.equal(await page.locator('.project-container-count').count(), 0)
    assert.equal(await page.locator('.project-health .status-badge').count(), 1)
    const metadata = await page.locator('.project-metadata').evaluate(el => {
      const service = el.querySelector('.project-summary-counts').getBoundingClientRect()
      const health = el.querySelector('.status-badge').getBoundingClientRect()
      const healthWrapper = el.querySelector('.project-health').getBoundingClientRect()
      return { serviceY: service.y, healthY: health.y, delimiterY: healthWrapper.y, healthX: health.x, serviceRight: service.right }
    })
    assert.ok(metadata.healthY >= metadata.serviceY && metadata.healthY - metadata.serviceY <= 24, `Count and health stay adjacent, wrapping at most one readable line at ${width}px: ${JSON.stringify(metadata)}`)
    assert.ok(Math.abs(metadata.healthY - metadata.delimiterY) <= 1, 'The delimiter stays on the health baseline, never on a ghost line')
    if (Math.abs(metadata.serviceY - metadata.healthY) <= 1) {
      assert.ok(metadata.healthX > metadata.serviceRight && metadata.healthX - metadata.serviceRight < 36, 'Health follows service count without overlap or a far-right gap')
    }
    if (width === 390 && process.env.PROJECT_UPDATE_SCREENSHOT) await page.screenshot({ path: process.env.PROJECT_UPDATE_SCREENSHOT.replace(/\.png$/, '-monitor.png') })
    assert.equal(await page.getByRole('button', { name: 'Edit media project icon', exact: true }).isVisible(), true)
    await page.getByRole('button', { name: 'Edit media project icon', exact: true }).click()
    await page.getByRole('heading', { name: 'Edit media icon', exact: true }).waitFor()
    await page.getByRole('button', { name: 'Cancel', exact: true }).click()
    assert.equal(await page.locator('.project-group details, .project-group .container-row').count(), 0)
    assert.equal(page.url(), monitorURL, 'Icon editing does not navigate')
    await count.click()
    assert.equal(await page.locator('.update-dialog h2').evaluate(el => getComputedStyle(el).fontSize), '24px', 'Mobile popup title must be 24px')
    assert.equal(await page.locator('.update-warning p').evaluate(el => getComputedStyle(el).fontSize), '16px')
    assert.equal(await page.locator('.review-image').evaluate(el => getComputedStyle(el).fontSize), '14px')
    await page.locator('.update-dialog').getByText('Containers will be recreated. Expect downtime.', { exact: true }).waitFor()
    await page.locator('.update-dialog').getByText('sha256:old', { exact: true }).waitFor()
    await page.locator('.update-dialog').getByText('sha256:new', { exact: true }).waitFor()
    if (width === 390 && process.env.PROJECT_UPDATE_SCREENSHOT) await page.screenshot({ path: process.env.PROJECT_UPDATE_SCREENSHOT.replace(/\.png$/, '-popup-top.png') })
    const footerBefore = await page.locator('.update-dialog .dialog-actions').boundingBox()
    await page.locator('.update-dialog .dialog-scroll').evaluate(el => { el.scrollTop = el.scrollHeight })
    const footerAfter = await page.locator('.update-dialog .dialog-actions').boundingBox()
    assert.equal(footerAfter.y, footerBefore.y, 'Footer remains fixed while technical content scrolls')
    assert.ok(footerAfter.y + footerAfter.height <= 640)
    assert.ok(await page.locator('.update-dialog').evaluate(el => el.scrollWidth <= el.clientWidth))
    if (width === 390 && process.env.PROJECT_UPDATE_SCREENSHOT) await page.screenshot({ path: process.env.PROJECT_UPDATE_SCREENSHOT })
    await page.keyboard.press('Escape')
    assert.equal(await count.evaluate(el => el === document.activeElement), true)
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
  }
  await page.setViewportSize({ width: 1200, height: 900 })
  await page.locator('.project-summary .navigation-arrow').click()
  await page.getByRole('heading', { name: 'media', exact: true }).waitFor()
  await page.goBack()
  await count.waitFor()
  console.log('PASS: 390px/320px typography, editable project icons, real health metadata, technical scope, fixed visible footer and no horizontal overflow.')
  await count.click()
  await page.getByRole('button', { name: 'Confirm & update', exact: true }).click()
  await page.getByRole('alert').getByText('Fixture rejection', { exact: true }).waitFor()
  const retry = page.getByRole('button', { name: 'Retry update', exact: true })
  assert.equal(await retry.count(), 1, 'A rejected submission must allow an explicitly approved retry')
  reject = false
  await retry.evaluate(el => { el.click(); el.click() })
  await page.locator('.update-dialog').waitFor({ state: 'detached' })
  assert.equal(submissions, 2, 'One rejection and exactly one accepted retry; no double submit')
  console.log('PASS: failure remains visible, explicit retry, safety lock prevents double submission.')
  operations = [{ id: 'fixture-operation', project: 'media', action: 'update', state: 'running', messages: [] }]
  await page.reload()
  await page.locator('.project-health').getByText('Updating', { exact: true }).waitFor()
  assert.equal(await count.count(), 0)
  projectGate = new Promise(resolve => { releaseProjects = resolve })
  operations = [{ ...operations[0], state: 'error', error: 'Fixture execution failed', needsAttention: true }]
  await page.locator('.project-health').getByText('Refreshing', { exact: true }).waitFor()
  assert.equal(await count.count(), 0, 'Terminal operation does not expose stale updates before fresh inventory')
  releaseProjects()
  await count.waitFor()
  await count.click()
  operations = [{ ...operations[0], state: 'queued' }]
  await page.locator('.update-dialog').waitFor({ state: 'detached' })
  await page.locator('.project-health').getByText('Queued', { exact: true }).waitFor()
  assert.equal(await count.count(), 0)
  assert.equal(submissions, 2, 'An externally queued operation invalidates an open review without submitting')
  operations = []
  health = 'unhealthy'
  service = ''
  await page.reload()
  await page.locator('.project-health').getByText('Unhealthy', { exact: true }).waitFor()
  assert.equal(await count.isDisabled(), true, 'Missing Compose service metadata cannot update')
  assert.equal(await page.locator('.project-summary .status-badge').count(), 1)
  service = 'web'
  connected = false
  await page.reload()
  await page.locator('.project-group').waitFor()
  assert.equal(await count.isDisabled(), true, 'Disconnected engine cannot open a new review')
  console.log('PASS: dynamic real health, Compose eligibility, disconnected guard, queued/running/refreshing suppression and externally queued review invalidation; zero extra submissions.')
  assert.deepEqual(errors, [])
} finally { releaseProjects?.(); await browser.close(); await server.close() }
