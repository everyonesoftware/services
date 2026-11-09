export type OutputRow = { key: string; text: string; id?: string; status?: string; detail?: string; percent?: number }

// Compose's plain output repeats a full line on every refresh. Interpret only
// known progress records; warnings, errors and unrecognized output stay intact.
const layer = /^([a-f\d]{12,64})\s+(Waiting|Pulling fs layer|Downloading|Verifying Checksum|Download complete|Extracting|Pull complete|Already exists)(?:\s+(.*))?$/i
const resource = /^((?:Image|Container|Network|Volume)\s+\S+)\s+(Pulling|Pulled|Creating|Created|Recreate|Recreated|Starting|Started|Stopping|Stopped|Removing|Removed|Waiting|Healthy|Running|Error)(?:\s+(.*))?$/
const size = '(\\d+(?:\\.\\d+)?)\\s*([kMGTPE]?i?B)'
const transfer = new RegExp(`${size}\\s*/\\s*${size}`, 'i')
const units = ['b', 'kb', 'mb', 'gb', 'tb', 'pb', 'eb']
const bytes = (value: string, unit: string) => Number(value) * (unit.toLowerCase().includes('i') ? 1024 : 1000) ** units.indexOf(unit.toLowerCase().replace('i', ''))

export function compactOperationOutput(messages: string[]): OutputRow[] {
  const rows: OutputRow[] = []
  const positions = new Map<string, number>()
  let command = 0
  for (const message of messages) {
    const stream = message.match(/^(stdout|stderr):\s*/)?.[1] || ''
    // ANSI cursor/color controls are intentionally matched, never rendered.
    // eslint-disable-next-line no-control-regex
    const clean = message.replace(/\u001b\[[0-?]*[ -/]*[@-~]/g, '').replace(/^(?:stdout|stderr):\s*/, '')
    for (const text of clean.split(/[\r\n]+/).map(line => line.trim()).filter(Boolean)) {
      if (text.startsWith('Running: ')) command++
      const match = text.match(layer) || text.match(resource)
      if (!match) {
        rows.push({ key: `line-${rows.length}`, text })
        continue
      }
      const [, id, status, remainder = ''] = match
      const detail = remainder.replace(/\[[=>\s]*\]\s*/, '').trim()
      const amounts = /^(Downloading|Extracting)$/i.test(status) ? detail.match(transfer) : null
      const total = amounts ? bytes(amounts[3], amounts[4]) : 0
      const percent = amounts && total > 0 ? Math.min(100, Math.max(0, bytes(amounts[1], amounts[2]) / total * 100)) : undefined
      const key = `${command}:${stream}:${id}`
      const row = { key, text, id, status, detail, percent }
      const position = positions.get(key)
      if (position === undefined) {
        positions.set(key, rows.length)
        rows.push(row)
      } else rows[position] = row
    }
  }
  return rows
}
