import { useState } from 'react'
import type { BrandIcon } from './brandIcons.js'
import type { Icon, IconTarget } from './types.ts'

export type IconEditorTarget = IconTarget & { automatic?: BrandIcon; current?: Icon; trigger: HTMLButtonElement }

export function ServiceIcon({ icon, custom, project = false, label, onEdit }: { icon?: BrandIcon; custom?: Icon; project?: boolean; label?: string; onEdit?: (trigger: HTMLButtonElement) => void }) {
  const [failedSources, setFailedSources] = useState<string[]>([])
  const source = [custom && { src: custom.url, slug: 'custom' }, icon].find(candidate => candidate && !failedSources.includes(candidate.src))
  const artwork = source
    ? <img className="service-icon" data-brand={source.slug} src={source.src} alt="" aria-hidden="true" decoding="async" onError={() => setFailedSources(previous => [...previous, source.src])} />
    : <svg className="service-icon neutral-icon" data-brand={project ? 'stack' : 'container'} aria-hidden="true" focusable="false" viewBox="-4 -4 32 32">
      {project ? <path d="m3 7 9-4 9 4-9 4-9-4Zm0 5 9 4 9-4M3 17l9 4 9-4" /> : <path d="m3 7 9-4 9 4v10l-9 4-9-4V7Zm0 0 9 4 9-4M12 11v10M7.5 5l9 4" />}
    </svg>
  return onEdit ? <button type="button" className="icon-button" aria-label={label} aria-haspopup="dialog" onClick={event => { event.preventDefault(); event.stopPropagation(); onEdit(event.currentTarget) }}>{artwork}</button> : artwork
}
