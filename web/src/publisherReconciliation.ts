import { splitImageReference } from './imageReference.js'
import type { Connection } from './publisherApi.ts'
import type { Container } from './types.ts'

export const publisherHref = (id: string) => `#publishers/${encodeURIComponent(id)}`
export function publisherRoute(hash: string) {
  if (!hash.startsWith('#publishers/')) return ''
  // eslint-disable-next-line no-control-regex -- Reject control characters in decoded identifiers.
  try { const id = decodeURIComponent(hash.slice('#publishers/'.length)); return id && !/[\u0000-\u001f\u007f]/.test(id) ? id : '' } catch { return '' }
}
export const publisherNeedsAttention = (connection: Connection) => ['review_required', 'identity_mismatch', 'unavailable', 'awaiting_publication'].includes(connection.reconciliation?.state ?? '') || connection.state === 'error'
export const publishersForContainers = (connections: Connection[], containers: Container[]) => connections.filter(connection => containers.some(container => splitImageReference(container.image).image === connection.imageRepository))
export function publisherSignal(connection: Connection) {
  const state = connection.reconciliation?.state
  return state === 'review_required' ? 'Publisher needs reconciliation' : state === 'awaiting_publication' ? 'Publisher awaiting publication' : publisherNeedsAttention(connection) ? 'Publisher needs attention' : state === 'reconciled' ? (connection.reconciliation?.previousName === connection.reconciliation?.currentName ? 'Publisher identity reconciled' : 'Publisher rename reconciled') : 'View publisher'
}
