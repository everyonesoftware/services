export type Container = {
  id: string
  name: string
  project?: string
  service?: string
  image?: string
  state: string
  health?: string
  monitorStatus?: string
  monitorReason?: string
  restartCount?: number
  startedAt?: string
  exitCode?: number
  oomKilled?: boolean
  healthOutput?: string
  currentDigest?: string
  proposedDigest?: string
  updateAvailable?: boolean
  updateStatus?: string
  updateError?: string
  updateCheckedAt?: string
}
export type Project = { name: string; status: string; updateAvailable?: boolean; containers: Container[] }
export type Incident = { id: string; project: string; service: string; name: string; status: string; reason: string; since: string }
export type MonitorEvent = { id: string; project: string; name: string; kind: string; message: string; time: string }
export type Monitor = { connected: boolean; lastChecked: string | null; error: string | null; incidents: Incident[]; events: MonitorEvent[]; notificationConfigured: boolean }
export type UpdateEvent = { type: 'progress' | 'complete' | 'error'; message: string }
export type Filter = 'all' | 'attention' | 'updates'
