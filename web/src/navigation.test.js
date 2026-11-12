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

test('breadcrumb has exactly one current item, encoded parent links and escaped labels', () => {
  const html = renderToStaticMarkup(createElement(Breadcrumb, { project: 'media / archive', container: '<script>container</script>' }))
  assert.match(html, /aria-label="Breadcrumb"/)
  assert.equal((html.match(/aria-current="page"/g) || []).length, 1)
  assert.match(html, /href="#monitor"/)
  assert.match(html, /href="#service\/media%20%2F%20archive"/)
  assert.match(html, /&lt;script&gt;container&lt;\/script&gt;/)
  assert.doesNotMatch(html, /<script>/)
  const root = renderToStaticMarkup(createElement(Breadcrumb))
  assert.match(root, /aria-current="page">Monitor/)
  assert.doesNotMatch(root, /<a/)
})
