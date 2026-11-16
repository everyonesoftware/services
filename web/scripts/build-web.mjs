import { createHash } from 'node:crypto'
import { readFile, readdir, writeFile } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

export async function buildWeb(directory) {
  // Strip our previous output before hashing, so regenerating is idempotent.
  const index = (await readFile(join(directory, 'index.html'), 'utf8')).replace(
    /<meta\b[^>]*>/gi,
    tag => /\sname\s*=\s*(?:"services-build"|'services-build'|services-build(?=\s|\/?>))/i.test(tag) ? '' : tag,
  )
  const files = []
  async function collect(relative = '') {
    for (const entry of await readdir(join(directory, relative), { withFileTypes: true })) {
      const name = relative ? `${relative}/${entry.name}` : entry.name
      if (entry.isDirectory()) await collect(name)
      else if (entry.isFile() && (
        /\.(?:js|css)$/.test(name) && name !== 'sw.js'
        || /^icons\//.test(name) && /\.(?:png|svg|ico|webp|jpe?g)$/.test(name)
        || !name.includes('/') && /\.(?:png|svg|ico)$/.test(name)
      )) files.push(name)
    }
  }
  await collect()
  const hash = createHash('sha256')
  // Relative POSIX names and length framing avoid platform/path ambiguity.
  const add = (name, bytes) => hash.update(`${name}\0${bytes.length}\0`).update(bytes)
  add('index.html', Buffer.from(index))
  for (const name of files.sort()) add(name, await readFile(join(directory, name)))
  const id = hash.digest('hex').slice(0, 20)
  const meta = `<meta name="services-build" content="${id}">`
  if (!/<\/head\s*>/i.test(index)) throw new Error('Build index.html must contain a closing head tag')
  await writeFile(join(directory, 'index.html'), index.replace(/<\/head\s*>/i, closing => `${meta}${closing}`))
  await writeFile(join(directory, 'web-build.json'), `${JSON.stringify({ id })}\n`)
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  await buildWeb(resolve('dist'))
}
