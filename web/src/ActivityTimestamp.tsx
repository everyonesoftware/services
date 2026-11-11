import { formatActivityTimestamp } from './activityTime.ts'
import './ActivityTimestamp.css'

export function ActivityTimestamp({ value }: { value?: string }) {
  const parts = formatActivityTimestamp(value)
  if (!parts || !value) return <span className="activity-timestamp">Unknown time</span>
  return <time className="activity-timestamp" dateTime={value} title={new Date(value).toLocaleString()}>
    <span className="activity-clock">{parts.time}</span>
    <span className="activity-time-separator" aria-hidden="true"> · </span>
    <span className="activity-date">{parts.date}</span>
  </time>
}
