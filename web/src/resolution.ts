import type { Container, Incident, MonitorEvent, Project, ResolutionAction, ResolutionDecision } from './types.ts'

export type ResolutionEntry = { incident: Incident; previous?: Container; candidates: Container[]; suggestion?: Container; events: MonitorEvent[] }
export type ResolutionDraft = { included: boolean; action: ResolutionAction | ''; replacementId: string; snoozeSeconds: 900 | 3600 | 86400 }
export type ResolutionDrafts = Record<string, ResolutionDraft>

export function resolutionEntries(incidents: Incident[], projects: Project[], events: MonitorEvent[]): ResolutionEntry[] {
  const all = projects.flatMap(project => project.containers.map(container => ({ ...container, project: container.project || project.name })))
  return incidents.filter(incident => incident.status === 'missing').map(incident => {
    const previous = all.find(c => (c.monitorStatus === 'missing' || c.state === 'missing') && c.project === incident.project && c.name === incident.name && (c.service || '') === incident.service)
    const candidates = all.filter(c => c.id && c.project === incident.project && c.state !== 'missing' && c.monitorStatus !== 'missing' && c.id !== previous?.id)
    const matches = previous?.image ? candidates.filter(c => c.project === incident.project && c.image === previous.image) : []
    return { incident, previous, candidates, suggestion: matches.length === 1 ? matches[0] : undefined, events: events.filter(event => event.project === incident.project && event.name === incident.name).sort((a, b) => Date.parse(b.time) - Date.parse(a.time)) }
  })
}

export function initialResolutionDrafts(entries: ResolutionEntry[]): ResolutionDrafts {
  return Object.fromEntries(entries.map(entry => [entry.incident.id, { included: true, action: '', replacementId: entry.suggestion?.id || '', snoozeSeconds: 3600 }]))
}

export function applyResolutionAction(drafts: ResolutionDrafts, action: ResolutionDraft['action']): ResolutionDrafts {
  return Object.fromEntries(Object.entries(drafts).map(([id, draft]) => [id, draft.included ? { ...draft, action } : draft]))
}

export function validateResolution(entries: ResolutionEntry[], drafts: ResolutionDrafts): { errors: Record<string, string>; decisions: ResolutionDecision[]; valid: boolean } {
  const errors: Record<string, string> = {}
  const selected = entries.filter(entry => drafts[entry.incident.id]?.included)
  if (selected.length > 100) errors.selection = 'Select at most 100 entries per batch. Exclude some entries, then resolve the rest separately.'
  const decisions: ResolutionDecision[] = []
  for (const entry of selected) {
    const { id, since } = entry.incident
    const draft = drafts[id]
    if (!draft.action) { errors[id] = 'Choose a resolution.'; continue }
    if (draft.action === 'replace') {
      if (!entry.candidates.some(c => c.id === draft.replacementId)) errors[id] = 'Choose a current container.'
      else if (selected.some(other => other.incident.id !== id && drafts[other.incident.id].action === 'replace' && drafts[other.incident.id].replacementId === draft.replacementId)) errors[id] = 'Choose a different replacement for each old service.'
      decisions.push({ incidentId: id, since, action: 'replace', replacementId: draft.replacementId })
    } else if (draft.action === 'snooze') {
      if (![900, 3600, 86400].includes(draft.snoozeSeconds)) errors[id] = 'Choose a snooze duration.'
      decisions.push({ incidentId: id, since, action: 'snooze', snoozeSeconds: draft.snoozeSeconds })
    } else decisions.push({ incidentId: id, since, action: 'forget' })
  }
  for (const decision of decisions) {
    const incident = entries.find(entry => entry.incident.id === decision.incidentId)!.incident
    if (incident.snoozedUntil) decision.snoozedUntil = incident.snoozedUntil
  }
  return { errors, decisions, valid: selected.length > 0 && Object.keys(errors).length === 0 }
}
