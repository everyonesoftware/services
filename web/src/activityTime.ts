export function formatActivityTimestamp(value?: string) {
  if (!value || !Number.isFinite(Date.parse(value))) return null
  const date = new Date(value)
  return {
    date: `${String(date.getFullYear()).padStart(4, '0')}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`,
    time: date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }),
  }
}
