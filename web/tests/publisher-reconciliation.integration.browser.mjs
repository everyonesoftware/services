// Production bundle + real Go API/SQLite; only provider/inventory are test fixtures.
import assert from 'node:assert/strict'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const base = process.env.RECONCILIATION_URL
const currentName = process.env.RECONCILIATION_CURRENT_NAME || 'acme/renamed'
const renamed = currentName !== 'acme/app'
assert.ok(base && process.env.RECONCILIATION_SESSION && process.env.RECONCILIATION_COOKIE, 'Run through TestPublisherReconciliationProductionBrowserIntegration')
const browser = await chromium.launch({ headless: true, ...(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : {}) })
try {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } })
  await context.addCookies([{ name: process.env.RECONCILIATION_COOKIE, value: process.env.RECONCILIATION_SESSION, url: base, httpOnly: true, sameSite: 'Strict' }])
  const page = await context.newPage()
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(base)
  const attention = page.getByRole('region', { name: 'Publisher attention' })
  await attention.getByText('Finance', { exact: true }).waitFor()
  await page.locator('.project-group').getByText('Healthy', { exact: true }).waitFor()
  await attention.getByRole('link', { name: 'Review reconciliation' }).click()
  const card = page.getByRole('article', { name: `Publisher ${currentName}`, exact: true })
  await card.waitFor()
  const publisherHash = new URL(page.url()).hash
  await card.getByRole('button', { name: renamed ? 'Review rename' : 'Review identity', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: renamed ? 'Review repository rename' : 'Review repository identity', exact: true })
  await dialog.getByText('acme/app', { exact: true }).first().waitFor()
  await dialog.getByText(currentName, { exact: true }).first().waitFor()
  assert.equal(await dialog.getByRole('button', { name: 'Apply', exact: true }).isEnabled(), false)
  await dialog.getByRole('checkbox').check()
  await dialog.getByRole('button', { name: 'Apply', exact: true }).click()
  if (process.env.RECONCILIATION_PARTIAL_WRITE === '1') {
    await dialog.getByRole('alert').waitFor()
    assert.equal(await dialog.getByRole('button', { name: 'Apply', exact: true }).isEnabled(), false)
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
    const resume = await context.request.post(base + '/__test/resume', { data: {} })
    assert.equal(resume.status(), 204)
    await card.locator('.publisher-rename-actions .primary').click()
    await dialog.getByRole('checkbox').waitFor()
    assert.equal(await dialog.getByRole('button', { name: 'Apply', exact: true }).isEnabled(), false)
    await dialog.getByRole('checkbox').check()
    await dialog.getByRole('button', { name: 'Apply', exact: true }).click()
  }
  await dialog.waitFor({ state: 'hidden' })
  await card.getByRole('region', { name: 'Repository reconciliation' }).getByText('Awaiting next publication', { exact: true }).waitFor()
  await page.getByRole('link', { name: 'Monitor', exact: true }).first().click()
  await attention.getByText('Finance', { exact: true }).waitFor()
  await attention.getByText('Publisher awaiting publication. Running containers are unaffected by publisher reconciliation.', { exact: true }).waitFor()
  await page.locator('.project-group').getByText('Healthy', { exact: true }).waitFor()
  const statusLink = attention.getByRole('link', { name: 'View publisher status', exact: true })
  assert.equal(await statusLink.getAttribute('href'), publisherHash)
  await statusLink.click()
  await card.getByRole('region', { name: 'Repository reconciliation' }).getByText('Awaiting next publication', { exact: true }).waitFor()
  const publication = await context.request.post(base + '/__test/publication', { data: {} })
  assert.equal(publication.status(), 200)
  assert.equal((await publication.json()).result, 'accepted')
  await card.getByRole('button', { name: 'Refresh status', exact: true }).click()
  await card.getByRole('region', { name: 'Repository reconciliation' }).getByText(/reconciled/i).first().waitFor()
  await page.getByRole('link', { name: 'Monitor', exact: true }).first().click()
  await page.getByRole('heading', { name: 'Services', exact: true }).waitFor()
  assert.equal(await page.getByRole('region', { name: 'Publisher attention' }).count(), 0)
  assert.equal(errors.length, 0, errors.join('\n'))
  console.log('PASS production bundle → actual authenticated Go review/apply API → SQLite migration → real publication ingestion/receipt → Monitor attention cleared. No browser API mocks or live external writes.')
} finally {
  await browser.close()
}
