import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdir, mkdtemp, writeFile, readFile, rm } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'

async function fixture(directory) {
  await mkdir(join(directory, 'assets/lazy'), { recursive: true })
  await mkdir(join(directory, 'icons'))
  for (const [name, bytes] of [
    ['index.html', '<html><head></head><body>Services</body></html>'],
    ['assets/app.js', 'console.log("Services")'], ['assets/app.css', 'body{}'],
    ['assets/lazy/chunk.js', 'export default 1'], ['assets/lazy/chunk.css', '.lazy{}'],
    ['icons/service.svg', '<svg/>'], ['monitor.svg', '<svg/>'],
  ]) await writeFile(join(directory, name), bytes)
}

test('web identity is repeatable, matches HTML and does not generate a worker', async () => {
  const { buildWeb } = await import('../scripts/build-web.mjs')
  const directory = await mkdtemp(join(process.env.TMPDIR || tmpdir(), 'services-build-'))
  try {
    await fixture(directory)
    await buildWeb(directory)
    const metadata = await readFile(join(directory, 'web-build.json'), 'utf8')
    const { id } = JSON.parse(metadata)
    assert.match(id, /^[a-f0-9]{20}$/)
    assert.deepEqual(JSON.parse(metadata), { id })
    const html = await readFile(join(directory, 'index.html'), 'utf8')
    assert.match(html, new RegExp(`<meta name="services-build" content="${id}">`))
    assert.equal((html.match(/name="services-build"/g) || []).length, 1)
    await buildWeb(directory)
    assert.equal(await readFile(join(directory, 'web-build.json'), 'utf8'), metadata)
    assert.equal(await readFile(join(directory, 'index.html'), 'utf8'), html)
    await assert.rejects(readFile(join(directory, 'sw.js')), { code: 'ENOENT' })
  } finally { await rm(directory, { recursive: true, force: true }) }
})

test('web identity follows frontend bytes, not paths or generated artifacts', async () => {
  const { buildWeb } = await import('../scripts/build-web.mjs')
  const directories = await Promise.all([1, 2].map(() => mkdtemp(join(process.env.TMPDIR || tmpdir(), 'services-identity-'))))
  const [directory, other] = directories
  const identity = async dir => {
    await buildWeb(dir)
    return JSON.parse(await readFile(join(dir, 'web-build.json'), 'utf8')).id
  }
  try {
    await Promise.all(directories.map(fixture))
    const original = await identity(directory)
    assert.equal(await identity(other), original)
    for (const name of ['index.html', 'assets/app.js', 'assets/app.css', 'assets/lazy/chunk.js', 'assets/lazy/chunk.css', 'icons/service.svg', 'monitor.svg']) {
      const bytes = await readFile(join(directory, name))
      await writeFile(join(directory, name), Buffer.concat([bytes, Buffer.from('\nchanged')]))
      assert.notEqual(await identity(directory), original, `${name} changes identity`)
      await writeFile(join(directory, name), bytes)
      assert.equal(await identity(directory), original)
    }
    await writeFile(join(directory, 'web-build.json'), '{"id":"stale"}')
    await writeFile(join(directory, 'sw.js'), '// retirement worker')
    await writeFile(join(directory, 'assets/app.js.map'), 'ignored sourcemap')
    assert.equal(await identity(directory), original)
    assert.equal(await readFile(join(directory, 'sw.js'), 'utf8'), '// retirement worker')
    let html = await readFile(join(directory, 'index.html'), 'utf8')
    html = html.replace(/<meta name="services-build"[^>]*>/, '<META content="stale" NAME=\'services-build\'><meta name=services-build content=stale>')
    await writeFile(join(directory, 'index.html'), html)
    assert.equal(await identity(directory), original)
    await writeFile(join(directory, 'index.html'), html.replace('</head>', '<meta data-name="services-build" content="unrelated"></head>'))
    assert.notEqual(await identity(directory), original)
  } finally { await Promise.all(directories.map(dir => rm(dir, { recursive: true, force: true }))) }
})
