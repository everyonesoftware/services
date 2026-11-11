import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

test('publisher heading omits the horizontal divider without changing shared page headings', () => {
  const css = readFileSync(new URL('./Publishers.css', import.meta.url), 'utf8')
  assert.match(css, /\.app-content \.publishers-workspace \.page-heading\s*\{[^}]*border-bottom:\s*0\s*;/)
})
