// Browser-only renderer fixture; no GitHub, Tailscale, or Services writes.
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const server = await createServer({ root: fileURLToPath(new URL('../', import.meta.url)), server: { host: '127.0.0.1', port: 0, hmr: false }, appType: 'custom' })
await server.listen()
const { PublisherReview } = await server.ssrLoadModule('/src/Publishers.tsx')
const reason = 'No recognized numeric version build argument was found.'
const plan = { supported: false, problems: [reason], repository: { id: 14, fullName: 'Demo/Agent', defaultBranch: 'main', installationId: 8 }, imageRepository: 'ghcr.io/demo/agent', variables: {}, secretName: 'SERVICES_PUBLISH_TOKEN', workflow: { supported: false, problems: [reason], files: [] } }
const markup = renderToStaticMarkup(createElement(PublisherReview, { plan }))
server.middlewares.use('/__workflow_review_fixture', (_req, res) => {
  res.setHeader('Content-Type', 'text/html')
  res.end(`<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/src/index.css"><link rel="stylesheet" href="/src/Publishers.css"></head><body><main style="padding:16px;min-width:0"><p>Browser-only workflow review fixture</p>${markup}</main></body></html>`)
})
let browser
try {
  browser = await chromium.launch({ ...(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : { channel: process.env.BROWSER_CHANNEL || 'msedge' }), headless: true })
  const page = await browser.newPage()
  for (const width of [1200, 390]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto(`${server.resolvedUrls.local[0]}__workflow_review_fixture`)
    await page.getByRole('alert').waitFor()
    assert.equal(await page.getByRole('listitem').filter({ hasText: reason }).count(), 1)
    assert.match(await page.getByRole('alert').innerText(), /Services cannot automatically adapt this workflow/)
    assert.match(await page.getByRole('alert').innerText(), /does not mean your publishing workflow is broken/)
    assert.doesNotMatch(await page.getByRole('alert').innerText(), /Merge/)
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `no horizontal overflow at ${width}px`)
  }
  console.log('Workflow review browser fixture passed at 1200px and 390px (deduplication, actionable warning, no horizontal overflow).')
} finally {
  await browser?.close()
  await server.close()
}
