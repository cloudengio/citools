// Small formatting helpers shared across components.

export function fmtTime(t?: string): string {
  if (!t) return '—'
  const d = new Date(t)
  if (isNaN(d.getTime())) return '—'
  return d.toLocaleString()
}

export function fmtBytes(n?: number): string {
  if (n === undefined || n === null) return '—'
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}

export function fmtDuration(from?: string, to?: string): string {
  if (!from) return '—'
  const start = new Date(from).getTime()
  const end = to ? new Date(to).getTime() : Date.now()
  if (isNaN(start) || isNaN(end)) return '—'
  let secs = Math.max(0, Math.round((end - start) / 1000))
  const h = Math.floor(secs / 3600)
  secs -= h * 3600
  const m = Math.floor(secs / 60)
  secs -= m * 60
  if (h > 0) return `${h}h${m}m`
  if (m > 0) return `${m}m${secs}s`
  return `${secs}s`
}
