import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createServer } from 'vite'

const server = await createServer({ server: { middlewareMode: true, hmr: false, ws: false }, appType: 'custom' })
after(() => server.close())
const { serviceHref, serviceRoute } = await server.ssrLoadModule('/src/navigation.ts')
const { Breadcrumb } = await server.ssrLoadModule('/src/Breadcrumb.tsx')

test('service links encode each stable name exactly once and preserve separators inside names', () => {
  for (const project of ['agent', 'media / archive #1', '100% literal', '日本語', '__proto__']) {
    for (const container of [undefined, 'web-1', 'copy / replica #1', 'literal%2Fname']) {
      const hash = serviceHref(project, container)
      assert.deepEqual(serviceRoute(hash), { kind: 'service', project, ...(container === undefined ? {} : { container }) })
      assert.equal(hash.split('/').length, container === undefined ? 2 : 3)
    }
  }
})

test('unknown, incomplete and malformed detail URLs fail closed without decoding repairs', () => {
  for (const hash of ['#service', '#service/', '#service/agent/', '#service/a/b/c', '#service/%E0%A4%A', '#service/agent/%FF', '#not-a-route']) assert.deepEqual(serviceRoute(hash), { kind: 'invalid' }, hash)
  for (const hash of ['', '#monitor', '#activity']) assert.deepEqual(serviceRoute(hash), { kind: 'monitor' })
})

test('breadcrumb contains only parent links with encoded URLs and escaped labels', () => {
  const parentProject = 'media / <script>'
  const html = renderToStaticMarkup(createElement(Breadcrumb, { parentProject }))
  assert.match(html, /aria-label="Breadcrumb"/)
  assert.deepEqual([...html.matchAll(/<a href="([^"]+)">([^<]+)<\/a>/g)].map(match => match.slice(1)), [
    ['#monitor', 'Monitor'],
    [serviceHref(parentProject), 'media / &lt;script&gt;'],
  ])
  assert.doesNotMatch(html, /<script>|aria-current/)
  const root = renderToStaticMarkup(createElement(Breadcrumb))
  assert.match(root, /<ol><li><a href="#monitor">Monitor<\/a><\/li><\/ol>/)
})
