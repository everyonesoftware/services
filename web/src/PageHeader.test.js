import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createServer } from 'vite'

const server = await createServer({ server: { middlewareMode: true, hmr: false, ws: false }, appType: 'custom' })
after(() => server.close())

const renderHeader = async props => {
  const { PageHeader } = await server.ssrLoadModule('/src/PageHeader.tsx')
  return renderToStaticMarkup(createElement(PageHeader, props))
}

test('header associates the optional id with its title and renders real action content', async () => {
  const action = createElement('button', { type: 'button', disabled: true }, 'Refresh')
  const html = await renderHeader({ title: 'Accounts', id: 'accounts-title', actions: action })
  assert.equal(html, '<header class="page-heading unified-page-header"><h1 id="accounts-title">Accounts</h1><div class="heading-actions"><button type="button" disabled="">Refresh</button></div></header>')
})

test('shared header stylesheet preserves approved geometry with shell-resistant selectors', () => {
  const source = readFileSync(new URL('./PageHeader.tsx', import.meta.url), 'utf8')
  assert.match(source, /import '\.\/PageHeader\.css'/)
  const css = readFileSync(new URL('./PageHeader.css', import.meta.url), 'utf8')
  const rule = selector => {
    const start = css.indexOf(`${selector} {`)
    assert.notEqual(start, -1, `missing ${selector}`)
    return css.slice(start, css.indexOf('}', start))
  }
  const header = rule('.app-content .page-heading.unified-page-header')
  for (const declaration of ['display: flex;', 'align-items: center;', 'justify-content: space-between;', 'flex-direction: row;', 'flex-wrap: wrap;', 'gap: 12px 24px;', 'min-height: 44px;', 'padding: 0;', 'margin: 0 0 16px;', 'border: 0;']) assert.ok(header.includes(declaration), declaration)
  const title = rule('.app-content .page-heading.unified-page-header h1')
  for (const declaration of ["font-family: 'Segoe UI', -apple-system, BlinkMacSystemFont, sans-serif;", 'margin: 0;', 'min-height: 44px;', 'display: flex;', 'align-items: center;', 'font-size: 24px;', 'line-height: 30px;', 'font-weight: 600;', 'letter-spacing: -1px;', 'color: #25332b;']) assert.ok(title.includes(declaration), declaration)
  const actions = rule('.app-content .page-heading.unified-page-header .heading-actions')
  for (const declaration of ['display: flex;', 'flex-direction: row;', 'flex-wrap: wrap;', 'align-items: center;', 'gap: 8px;', 'width: auto;']) assert.ok(actions.includes(declaration), declaration)
  assert.match(css, /@media\s*\(max-width:\s*640px\)\s*\{[\s\S]*\.app-content \.page-heading\.unified-page-header \.heading-actions\s*\{\s*flex-basis:\s*100%;/)
})

const pageProps = { user: { id: 'admin', username: 'Administrator', role: 'admin' }, onBack() {}, onAccounts() {}, onPublishers() {}, onLogout() {}, onPasswordChanged() {} }

for (const [module, name, title, actions] of [
  ['Publishers', 'Publishers', 'Publishers', ['Refresh', 'Connect repository']],
  ['AuthAccounts', 'AuthAccounts', 'Accounts', ['Refresh']],
]) {
  test(`${name} uses the shared title-only header with its existing actions`, async () => {
    const loaded = await server.ssrLoadModule(`/src/${module}.tsx`)
    const html = renderToStaticMarkup(createElement(loaded[name], pageProps))
    const header = html.match(/<header class="page-heading unified-page-header">([\s\S]*?)<\/header>/)?.[1]
    assert.ok(header, 'page must use the semantic shared header')
    assert.ok(header.startsWith(`<h1>${title}</h1><div class="heading-actions">`))
    assert.deepEqual([...header.matchAll(/<button[^>]*>([^<]+)<\/button>/g)].map(match => match[1]), actions)
    assert.doesNotMatch(header, /<p|page-description|publisher-muted/)
    assert.doesNotMatch(html, /Manage administrator access and API credentials\./)
    if (name === 'Publishers') {
      const source = readFileSync(new URL('./Publishers.tsx', import.meta.url), 'utf8')
      assert.match(source, /<PageHeader title=\{step === null \? 'Publishers' : 'Connect an image publisher'\}/)
      assert.doesNotMatch(source, /Receive image updates directly from your publishing workflows\./)
    }
  })
}

test('title-only header is semantic, escapes its title and omits the actions wrapper', async () => {
  const html = await renderHeader({ title: 'Services & <status>' })
  assert.equal(html, '<header class="page-heading unified-page-header"><h1>Services &amp; &lt;status&gt;</h1></header>')
})
