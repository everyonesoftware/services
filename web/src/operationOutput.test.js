import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createServer } from 'vite'

const server = await createServer({ server: { middlewareMode: true, hmr: false, ws: false }, appType: 'custom' })
after(() => server.close())
const output = await server.ssrLoadModule('/src/operationOutput.ts')

test('layer refreshes replace their own row without losing other layers or ordinary output', () => {
  const messages = ['Running: docker compose pull', 'stderr: abc123def456 Downloading [==> ] 2MB/10MB', 'stderr: 123456abcdef Downloading [=> ] 1MB/20MB', 'stderr: abc123def456 Downloading [====> ] 6MB/10MB', 'stderr: warning: keep this <message>']
  const rows = output.compactOperationOutput(messages)
  assert.equal(rows.length, 4)
  assert.equal(rows[1].id, 'abc123def456')
  assert.equal(rows[1].status, 'Downloading')
  assert.equal(rows[1].percent, 60)
  assert.equal(rows[1].detail, '6MB/10MB')
  assert.equal(rows[2].id, '123456abcdef')
  assert.equal(rows[3].text, 'warning: keep this <message>')
  assert.equal(messages.length, 5)
})

test('completion replaces transfer state without declaring the whole operation successful', () => {
  const rows = output.compactOperationOutput(['stderr: abc123def456 Extracting [==>] 2MB/10MB', 'stderr: abc123def456 Pull complete', 'stderr: Error response from daemon: denied'])
  assert.equal(rows.length, 2)
  assert.equal(rows[0].status, 'Pull complete')
  assert.equal(rows[0].percent, undefined)
  assert.equal(rows[0].detail, '')
  assert.equal(rows[1].text, 'Error response from daemon: denied')
})

test('byte units, unknown sizes, control sequences and carriage-return refreshes are safe', () => {
  const rows = output.compactOperationOutput(['stderr: \u001b[2K\u001b[1Aabc123def456 Downloading [=>] 512kB/1MB\rabc123def456 Downloading [==>] 750kB/1MB', 'stdout: 123456abcdef Waiting', 'stderr: deadbeef1234 Downloading [=>] 4MB/0B', 'stderr: fedcba123456 Downloading [=>] 4MB/2MB', 'stderr: cafe1234abcd Downloading [=>] 512KiB/1MiB'])
  assert.equal(rows.length, 5)
  assert.equal(rows[0].percent, 75)
  assert.equal(rows[1].percent, undefined)
  assert.equal(rows[2].percent, undefined)
  assert.equal(rows[3].percent, 100)
  assert.equal(rows[4].percent, 50)
  assert.doesNotMatch(JSON.stringify(rows), /\\u001b/)
})

test('Compose service state refreshes coalesce but new commands and streams remain separate', () => {
  const rows = output.compactOperationOutput(['stderr: Image nginx:stable Pulling', 'stderr: Container site-web-1 Recreate', 'stderr: Container site-web-1 Recreated', 'stdout: abc123def456 Waiting', 'stderr: abc123def456 Downloading [=>] 1MB/2MB', 'Running: docker compose up', 'stderr: abc123def456 Waiting'])
  assert.equal(rows.length, 6)
  assert.equal(rows[1].id, 'Container site-web-1')
  assert.equal(rows[1].status, 'Recreated')
  assert.equal(rows.at(-1).status, 'Waiting')
})

test('activity defaults to compact progress with an optional raw output control and escaped text', async () => {
  const { DockerActivity } = await server.ssrLoadModule('/src/DockerActivity.tsx')
  const operation = { id: 'demo', project: 'site', action: 'update project', state: 'running', phase: 'pulling', queuedAt: '', messages: ['stderr: abc123def456 Downloading [=>] 1MB/10MB', 'stderr: abc123def456 Downloading [=====>] 5MB/10MB', 'stderr: warning: <script>'] }
  const html = renderToStaticMarkup(createElement(DockerActivity, { operations: [operation], loading: false, error: '' }))
  assert.match(html, /Show raw output/)
  assert.match(html, /<progress[^>]*value="50"[^>]*max="100"/)
  assert.equal((html.match(/abc123def456/g) || []).length, 2) // row identity and accessible progress label
  assert.doesNotMatch(html, /1MB\/10MB|stderr:|\[=====/)
  assert.match(html, /warning: &lt;script&gt;/)
  assert.doesNotMatch(html, /<script>/)
})
