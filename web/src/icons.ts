import { deleteIcon, getIcons, saveIcon, uploadIcon } from './api.ts'
import type { IconList, IconTarget } from './types.ts'

export function imageUrlError(value: string): string {
  try {
    const url = new URL(value)
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) throw new Error()
    return ''
  } catch { return 'Enter a complete public http:// or https:// image URL without credentials.' }
}

// Convenience checks only; the server validates the actual image bytes and dimensions.
export function imageFileError(file: File | null): string {
  if (!file) return 'Choose a PNG, JPEG or GIF image to upload.'
  if (!['image/png', 'image/jpeg', 'image/gif'].includes(file.type)) return 'Choose a PNG, JPEG or GIF image. SVG is not supported.'
  if (file.size > 2 * 1024 * 1024) return 'The image must be 2 MiB or smaller.'
  return ''
}

// Never publish an optimistic icon: a successful write must be read back first.
export async function changeIcon(target: IconTarget, source: string | File | null, fetcher: typeof fetch = fetch): Promise<IconList> {
  const uploading = source !== null && typeof source !== 'string'
  const saved = source === null ? (await deleteIcon(target, fetcher), null) : uploading ? await uploadIcon(target, source, fetcher) : await saveIcon(target, source, fetcher)
  const next = await getIcons(fetcher)
  const current = next.icons.find(icon => icon.kind === target.kind && icon.key === target.key)
  if (!next.available || (source !== null && !saved) || (saved ? saved.kind !== target.kind || saved.key !== target.key || (uploading && saved.sourceUrl !== '') || !current || current.url !== saved.url || current.sourceUrl !== saved.sourceUrl : current !== undefined)) {
    throw new Error('The icon change could not be verified. Your displayed icon has been kept. Refresh before retrying.')
  }
  return next
}
