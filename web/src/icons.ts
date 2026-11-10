import { deleteIcon, getIcons, saveIcon } from './api.ts'
import type { IconList, IconTarget } from './types.ts'

export function imageUrlError(value: string): string {
  try {
    const url = new URL(value)
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) throw new Error()
    return ''
  } catch { return 'Enter a complete public http:// or https:// image URL without credentials.' }
}

// Never publish an optimistic icon: a successful write must be read back first.
export async function changeIcon(target: IconTarget, sourceUrl: string | null, fetcher: typeof fetch = fetch): Promise<IconList> {
  const saved = sourceUrl === null ? (await deleteIcon(target, fetcher), null) : await saveIcon(target, sourceUrl, fetcher)
  const next = await getIcons(fetcher)
  const current = next.icons.find(icon => icon.kind === target.kind && icon.key === target.key)
  if (!next.available || (saved ? saved.kind !== target.kind || saved.key !== target.key || !current || current.url !== saved.url || current.sourceUrl !== saved.sourceUrl : current !== undefined)) {
    throw new Error('The icon change could not be verified. Your displayed icon has been kept. Refresh before retrying.')
  }
  return next
}
