import assert from 'node:assert/strict'

// Test-only Docker Engine -> actual Go JSON API -> built React -> Chromium.
// No page.route/context.route, Vite proxy or fabricated /api responses.
const url = process.env.SERVICES_LOG_URL
assert.ok(url && process.env.SERVICES_LOG_SESSION, 'Run via TestLogsProductionBrowserIntegration')
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const browser = await chromium.launch({ headless: true, ...(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : {}) })
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
const page = await context.newPage()
page.setDefaultTimeout(10000)
const errors = [], responses = []
page.on('pageerror', error => errors.push(error.message))
page.on('response', async response => {
  if (new URL(response.url()).pathname !== '/api/logs' || response.status() !== 200) return
  try { responses.push({ query: new URL(response.url()).searchParams, body: await response.json() }) } catch { /* Cancellation on unmount is legitimate. */ }
})
const wait = async (predicate, message) => {
  for (let n = 0; n < 250; n++) {
    if (await predicate()) return
    await new Promise(resolve => setTimeout(resolve, 30))
  }
  assert.fail(message)
}
const ids = () => page.locator('[data-log-id]').evaluateAll(nodes => nodes.map(node => node.dataset.logId))
const assertRows = async entries => {
  await wait(async () => (await ids()).length === entries.length, `Expected ${entries.length} rendered rows`)
  assert.deepEqual(await ids(), entries.map(entry => entry.id), 'DOM preserves real API chronological order and IDs')
  assert.equal(new Set(await ids()).size, entries.length, 'No duplicate IDs')
  const rendered = await page.locator('[data-log-id]').allTextContents()
  for (const [index, entry] of entries.entries()) {
    assert.ok(rendered[index].includes(entry.message), `Real decoded message rendered at ${index}`)
    assert.ok(rendered[index].includes(entry.container), `Real source name rendered at ${index}`)
    assert.ok(rendered[index].includes(entry.stream), `Real stdout/stderr rendered at ${index}`)
  }
}
try {
  const anonymous = await fetch(`${url}/api/logs?project=browser-fixture`)
  assert.equal(anonymous.status, 401)
  const reader = await fetch(`${url}/api/logs?project=browser-fixture`, { headers: { Authorization: `Bearer ${process.env.SERVICES_LOG_READ_KEY}` } })
  assert.equal(reader.status, 403, 'services:read key cannot read logs')
  await context.addCookies([{ name: process.env.SERVICES_LOG_COOKIE, value: process.env.SERVICES_LOG_SESSION, url, httpOnly: true, sameSite: 'Strict' }])
  await page.goto(`${url}/#service/browser-fixture`)
  await page.getByRole('heading', { name: 'browser-fixture', exact: true }).waitFor()
  assert.equal(responses.length, 0, 'Overview does not fetch logs')
  await page.getByRole('tab', { name: 'Logs', exact: true }).click()
  await wait(() => responses.length > 0, 'Initial real API response')
  const initial = responses[0]
  assert.deepEqual(Object.fromEntries(initial.query), { project: 'browser-fixture', limit: '200' })
  assert.equal(initial.body.entries.length, 200)
  assert.ok(initial.body.hasMore && initial.body.nextCursor && initial.body.nextAfter)
  assert.deepEqual([...new Set(initial.body.entries.map(entry => entry.containerId))].sort(), ['web-exact-id', 'worker-exact-id'])
  assert.deepEqual([...new Set(initial.body.entries.map(entry => entry.stream))].sort(), ['stderr', 'stdout'])
  assert.equal(initial.body.entries.filter(entry => entry.message === 'fixture identical timestamp duplicate').length, 3, 'Recent tail cuts through a timestamp tie')
  await assertRows(initial.body.entries)
  const duplicateIDs = initial.body.entries.filter(entry => entry.message === 'fixture identical timestamp duplicate').map(entry => entry.id)
  await page.getByRole('button', { name: 'Load older', exact: true }).click()
  await wait(() => responses.some(response => response.query.has('cursor')), 'Real historical cursor request')
  const older = responses.find(response => response.query.has('cursor'))
  assert.equal(older.query.get('cursor'), initial.body.nextCursor)
  assert.equal(older.body.entries.length, 62)
  const history = [...older.body.entries, ...initial.body.entries]
  await assertRows(history)
  const duplicates = history.filter(entry => entry.message === 'fixture identical timestamp duplicate')
  assert.equal(duplicates.length, 5, 'Identical timestamp/messages are not collapsed')
  assert.equal(new Set(duplicates.map(entry => entry.id)).size, 5)
  assert.ok(duplicateIDs.every(id => duplicates.some(entry => entry.id === id)), 'IDs remain stable across history')
  await page.getByRole('region', { name: 'Log output', exact: true }).evaluate(element => { element.scrollTop = 100; element.dispatchEvent(new Event('scroll')) })
  await page.getByText('Paused', { exact: true }).waitFor()
  assert.equal((await context.request.post(`${url}/__test/append`)).status(), 204)
  await new Promise(resolve => setTimeout(resolve, 1800))
  assert.equal((await ids()).length, 262, 'Paused browser does not consume appended fixture logs')
  const beforeJump = responses.length
  await page.getByRole('button', { name: 'Jump to latest', exact: true }).click()
  await wait(async () => (await ids()).length === 713, 'Jump latest drains 451 real appended records across pages')
  const incremental = responses.slice(beforeJump).filter(response => response.query.has('after'))
  assert.ok(incremental.length >= 3, '450+ appended rows require multiple real after pages')
  assert.equal(incremental[0].query.get('after'), initial.body.nextAfter, 'Resume from latest initial checkpoint, not older-page checkpoint')
  assert.ok(incremental[0].body.hasNewer && incremental[1].body.hasNewer)
  assert.equal(incremental[1].query.get('after'), incremental[0].body.nextAfter)
  assert.equal(incremental[2].query.get('after'), incremental[1].body.nextAfter)
  assert.ok(responses.slice(beforeJump).every(response => response.query.has('after')), 'Jump does not silently retail recent logs')
  const appended = incremental.flatMap(response => response.body.entries)
  assert.equal(appended.length, 451)
  assert.equal(appended.filter(entry => entry.message === 'fixture worker equal-time appended').length, 1, 'Per-container equal-timestamp continuation survives project merge')
  // Equal-time worker append follows the last historical row, before new web rows.
  await assertRows([...history, ...appended])
  await page.getByText('Following', { exact: true }).waitFor()
  const beforeScope = responses.length
  await page.getByLabel('Container', { exact: true }).selectOption('worker-exact-id')
  await wait(() => responses.slice(beforeScope).some(response => response.query.get('container') === 'worker-exact-id' && !response.query.has('after')), 'Exact container request')
  const scoped = responses.slice(beforeScope).find(response => response.query.get('container') === 'worker-exact-id' && !response.query.has('after'))
  assert.equal(scoped.body.entries.length, 3)
  assert.ok(scoped.body.entries.every(entry => entry.containerId === 'worker-exact-id'))
  await assertRows(scoped.body.entries)
  const invalid = await context.request.get(`${url}/api/logs?project=browser-fixture&container=web-exact`)
  assert.equal(invalid.status(), 404, 'Container prefix is not an exact scope')
  const wrongProject = await context.request.get(`${url}/api/logs?project=browser-fixture&container=web-exact-id-other`)
  assert.equal(wrongProject.status(), 404, 'Exact ID from another project is rejected')
  await page.getByRole('tab', { name: 'Overview', exact: true }).click()
  await page.evaluate(() => { location.hash = '#service/browser-fixture/fixture-worker-1' })
  await page.getByRole('heading', { name: 'fixture-worker-1', exact: true }).waitFor()
  await page.getByRole('tab', { name: 'Logs', exact: true }).click()
  await assertRows(scoped.body.entries)
  assert.equal(await page.getByLabel('Container', { exact: true }).count(), 0)
  await page.getByRole('tab', { name: 'Overview', exact: true }).click()
  assert.deepEqual(errors, [])
  console.log('PASS real Go API + Docker inventory/multiplex decoder + built React: recent/older/tied duplicate IDs/multi-page after/project streams/exact scope/auth')
} finally {
  await context.close()
  await browser.close()
}
