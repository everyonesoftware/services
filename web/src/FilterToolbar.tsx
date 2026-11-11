import { useEffect, useId, useRef, useState } from 'react'
import type { KeyboardEvent, ReactNode, RefObject } from 'react'
import './FilterToolbar.css'

export type Choice = { value: string; label: string }
function DropdownIcon({ kind }: { kind: 'application' | 'type' | 'sort' | 'filter' }) {
  return <svg viewBox="0 0 24 24" aria-hidden="true">{kind === 'filter' ? <path d="M3 4h18l-7 8v7l-4 2v-9z" /> : kind === 'application' ? <><rect x="3" y="3" width="7" height="7" rx="1" /><rect x="14" y="3" width="7" height="7" rx="1" /><rect x="3" y="14" width="7" height="7" rx="1" /><rect x="14" y="14" width="7" height="7" rx="1" /></> : kind === 'type' ? <><path d="M9 6h12M9 12h12M9 18h12" /><circle cx="4" cy="6" r="1" /><circle cx="4" cy="12" r="1" /><circle cx="4" cy="18" r="1" /></> : <path d="M5 4v16m-3-3 3 3 3-3M12 5h9M12 10h7M12 15h5M12 20h3" />}</svg>
}

export function IconDropdown({ kind, label, value, choices, active, onChange }: { kind: 'application' | 'type' | 'sort' | 'filter'; label: string; value: string; choices: Choice[]; active: boolean; onChange: (value: string) => void }) {
  const id = useId()
  const button = useRef<HTMLButtonElement>(null)
  const menu = useRef<HTMLDivElement>(null)
  const anchor = useRef<{ top: number; left: number } | null>(null)
  const [expanded, setExpanded] = useState(false)
  const selected = choices.find(choice => choice.value === value)?.label ?? value
  const title = `${label}: ${selected}`
  const close = (restoreFocus = true) => {
    if (menu.current?.matches(':popover-open')) menu.current.hidePopover()
    if (restoreFocus) button.current?.focus({ preventScroll: true })
  }
  const open = (last = false) => {
    if (!menu.current || !button.current) return
    if (menu.current.matches(':popover-open')) { close(); return }
    const rect = button.current.getBoundingClientRect()
    anchor.current = { top: rect.top, left: rect.left }
    const width = Math.min(280, window.innerWidth - 24)
    const below = window.innerHeight - rect.bottom - 19
    const above = rect.top - 19
    const useAbove = below < 200 && above > below
    Object.assign(menu.current.style, {
      width: `${width}px`, left: `${Math.max(12, Math.min(rect.right - width, window.innerWidth - width - 12))}px`,
      top: useAbove ? 'auto' : `${rect.bottom + 7}px`, bottom: useAbove ? `${window.innerHeight - rect.top + 7}px` : 'auto',
      maxHeight: `${Math.max(44, useAbove ? above : below)}px`,
    })
    menu.current.showPopover()
    const options = [...menu.current.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]')]
    ;(last ? options.at(-1) : options.find(option => option.getAttribute('aria-checked') === 'true') ?? options[0])?.focus({ preventScroll: true })
  }
  const keydown = (event: KeyboardEvent<HTMLDivElement>) => {
    const options = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]')]
    const index = options.indexOf(document.activeElement as HTMLButtonElement)
    const target = event.key === 'ArrowDown' ? options[(index + 1) % options.length] : event.key === 'ArrowUp' ? options[(index - 1 + options.length) % options.length] : event.key === 'Home' ? options[0] : event.key === 'End' ? options.at(-1) : undefined
    if (target) { event.preventDefault(); target.focus() }
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close() }
    if (event.key === 'Tab') close(false)
  }
  useEffect(() => {
    const moved = (event: Event) => {
      if (event.target instanceof Node && menu.current?.contains(event.target)) return
      if (!menu.current?.matches(':popover-open')) return
      const rect = button.current?.getBoundingClientRect()
      // A focus-induced scroll event can arrive after opening. Close only when the anchor actually moves.
      if (event.type === 'resize' || !rect || !anchor.current || Math.abs(rect.top - anchor.current.top) > 1 || Math.abs(rect.left - anchor.current.left) > 1) menu.current.hidePopover()
    }
    window.addEventListener('resize', moved)
    document.addEventListener('scroll', moved, true)
    return () => { window.removeEventListener('resize', moved); document.removeEventListener('scroll', moved, true) }
  }, [])
  return <>
    <button ref={button} className="activity-icon-dropdown" type="button" title={title} aria-label={title} aria-haspopup="menu" aria-expanded={expanded} aria-controls={id} data-active={active} onClick={() => open()} onKeyDown={event => { if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); open(event.key === 'ArrowUp') } }}><DropdownIcon kind={kind} /><svg className="activity-chevron" viewBox="0 0 24 24" aria-hidden="true"><path d="m7 10 5 5 5-5" /></svg></button>
    <div ref={menu} id={id} popover="auto" className="activity-dropdown-menu" role="menu" aria-label={`${label} options`} onToggle={event => setExpanded(event.currentTarget.matches(':popover-open'))} onKeyDown={keydown}>
      <p className="activity-menu-title">{label}</p>
      {choices.map(choice => <button key={choice.value} className="activity-menu-option" type="button" role="menuitemradio" tabIndex={-1} aria-checked={choice.value === value} onClick={() => { close(); onChange(choice.value) }}><span>{choice.label}</span><span aria-hidden="true">{choice.value === value ? '✓' : ''}</span></button>)}
    </div>
  </>
}

export function FilterToolbar({ label, children }: { label: string; children: ReactNode }) {
  return <div className="sticky-panel"><section className="activity-filters" aria-label={label}>{children}</section></div>
}

export function FilterSearch({ label, value, onChange, inputRef }: { label: string; value: string; onChange: (value: string) => void; inputRef?: RefObject<HTMLInputElement | null> }) {
  return <div className="activity-search"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="10" cy="10" r="6" /><path d="m15 15 5 5" /></svg><input ref={inputRef} type="search" aria-label={label} placeholder={`${label}…`} value={value} onChange={event => onChange(event.target.value)} />{value && <button type="button" aria-label="Clear search" title="Clear search" onClick={() => { onChange(''); inputRef?.current?.focus({ preventScroll: true }) }}>×</button>}</div>
}

export function ResetView({ onClick }: { onClick: () => void }) {
  return <button className="activity-icon-dropdown" type="button" aria-label="Reset view" title="Reset view" onClick={onClick}><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 10a9 9 0 1 1 2 8M3 4v6h6" /></svg></button>
}
