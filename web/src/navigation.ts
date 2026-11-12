export type ServiceRoute = { kind: 'monitor' } | { kind: 'service'; project: string; container?: string } | { kind: 'invalid' }

export function serviceHref(project: string, container?: string): string {
  return `#service/${encodeURIComponent(project)}${container === undefined ? '' : `/${encodeURIComponent(container)}`}`
}

export function serviceRoute(hash: string): ServiceRoute {
  if (!hash || hash === '#monitor' || hash === '#activity') return { kind: 'monitor' }
  const segments = hash.slice(1).split('/')
  if (segments[0] !== 'service' || segments.length < 2 || segments.length > 3) return { kind: 'invalid' }
  try {
    const [, project, container] = segments.map(decodeURIComponent)
    if (!project || (segments.length === 3 && !container)) return { kind: 'invalid' }
    return { kind: 'service', project, ...(container === undefined ? {} : { container }) }
  } catch { return { kind: 'invalid' } }
}
