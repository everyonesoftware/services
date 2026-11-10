import { useEffect, useRef, useState } from 'react'
import { changeIcon, imageUrlError } from './icons.ts'
import { ServiceIcon } from './ServiceIcon.tsx'
import type { IconEditorTarget } from './ServiceIcon.tsx'
import type { IconList } from './types.ts'

export function IconDialog({ target, available, onClose, onSaved }: { target: IconEditorTarget; available: boolean; onClose: () => void; onSaved: (icons: IconList, reset: boolean) => void }) {
  const dialog = useRef<HTMLDialogElement>(null)
  const input = useRef<HTMLInputElement>(null)
  const lock = useRef(false)
  const [url, setUrl] = useState(target.current?.sourceUrl ?? '')
  const [busy, setBusy] = useState<'save' | 'reset' | null>(null)
  const [failure, setFailure] = useState('')
  const project = target.kind === 'project'
  useEffect(() => {
    dialog.current?.showModal()
    const trigger = target.trigger
    return () => { trigger?.focus() }
  }, [target.trigger])
  const dismiss = () => { if (!lock.current) { dialog.current?.close(); onClose() } }
  const run = async (reset: boolean) => {
    if (lock.current || !available) return
    const sourceUrl = url.trim()
    const error = reset ? '' : imageUrlError(sourceUrl)
    if (error) { setFailure(error); input.current?.focus(); return }
    lock.current = true
    setBusy(reset ? 'reset' : 'save')
    setFailure('')
    try {
      const next = await changeIcon(target, reset ? null : sourceUrl)
      onSaved(next, reset)
      lock.current = false
      dismiss()
    } catch (cause) {
      setFailure(cause instanceof Error ? cause.message : 'The icon could not be saved. Your displayed icon has been kept.')
    } finally { lock.current = false; setBusy(null) }
  }
  return <dialog ref={dialog} className="update-dialog icon-dialog" aria-labelledby="icon-title" aria-describedby="icon-scope" aria-busy={!!busy} onKeyDown={event => {
    if (event.key !== 'Tab') return
    const controls = [...event.currentTarget.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled)')]
    const first = controls[0]
    const last = controls.at(-1)
    if (!first) { event.preventDefault(); return }
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
  }} onCancel={event => { event.preventDefault(); dismiss() }} onClick={event => { if (event.target === event.currentTarget) { const bounds = event.currentTarget.getBoundingClientRect(); if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) dismiss() } }}>
    <form noValidate onSubmit={event => { event.preventDefault(); void run(false) }}>
      <div className="dialog-header"><span className="eyebrow">{project ? 'COMPOSE PROJECT' : 'CONTAINER'}</span><h2 id="icon-title">Edit {target.key} icon</h2><p id="icon-scope">{project ? "Only changes this project's icon; container icons stay unchanged." : `Only changes ${target.key}. The project icon stays unchanged.`}</p></div>
      <div className="dialog-scroll icon-editor-body">
        <div className="icon-preview"><ServiceIcon icon={target.automatic} custom={target.current} project={project} /><div><strong>Current icon</strong><p>{target.current ? 'Custom · locally stored' : `Automatic${target.automatic ? ` · ${target.automatic.title}` : ' · default icon'}`}</p></div></div>
        <label className="icon-field-label" htmlFor="icon-url">Image URL</label><input ref={input} id="icon-url" className="icon-url" type="url" value={url} onChange={event => setUrl(event.target.value)} placeholder="https://example.com/icon.png" autoComplete="off" autoFocus disabled={!!busy || !available} aria-describedby="icon-help icon-error" aria-invalid={!!failure} />
        <p className="icon-help" id="icon-help">Public HTTP(S) PNG, JPEG or GIF. Maximum 2 MiB, 4 million pixels and 4096 pixels per side. SVG is not supported.</p>
        <p className="icon-error" id="icon-error" role="alert">{failure || (!available ? 'Icon storage is unavailable. Try again after it reconnects.' : '')}</p>
        <p className="icon-storage"><strong>Saved on your server</strong><br />Services downloads and keeps a local copy in its data folder, not loaded from the URL on every visit.</p>
      </div>
      <div className="dialog-actions"><button type="button" className="button icon-reset" onClick={() => void run(true)} disabled={!!busy || !available || !target.current}>{busy === 'reset' ? 'Resetting…' : 'Use default icon'}</button><button type="button" className="button secondary" onClick={dismiss} disabled={!!busy}>Cancel</button><button type="submit" className="button primary" disabled={!!busy || !available}>{busy === 'save' ? 'Downloading…' : 'Download & save'}</button></div>
    </form>
  </dialog>
}
