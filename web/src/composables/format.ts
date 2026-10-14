export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.max(0, Math.min(Math.floor(Math.log(n) / Math.log(1024)), units.length - 1))
  const v = n / 1024 ** i
  return `${v >= 100 || i === 0 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}`
}

export function formatDateTime(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return '—'
  return d.toLocaleString(undefined, { hour12: false })
}

export function formatDate(iso?: string | number): string {
  if (iso === undefined || iso === '') return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return '—'
  return d.toLocaleDateString(undefined)
}

/** "3 min ago"-style text via vue-i18n's `time.*` messages. */
export function formatAgo(iso: string | undefined, t: (key: string, named: Record<string, unknown>) => string, now = Date.now()): string {
  if (!iso) return ''
  const s = Math.max(0, Math.floor((now - new Date(iso).getTime()) / 1000))
  if (s < 60) return t('time.justNow', {})
  if (s < 3600) return t('time.minutesAgo', { n: Math.floor(s / 60) })
  if (s < 86400) return t('time.hoursAgo', { n: Math.floor(s / 3600) })
  return t('time.daysAgo', { n: Math.floor(s / 86400) })
}

export function formatRate(bytesPerSec: number): string {
  return `${formatBytes(bytesPerSec)}/s`
}

/** Round "nice" upper bound and step count for an axis from 0 to max. */
export function niceScale(max: number, ticks = 4): { top: number; step: number } {
  if (!(max > 0)) return { top: 1, step: 0.25 }
  const raw = max / ticks
  const pow = 10 ** Math.floor(Math.log10(raw))
  const n = raw / pow
  const step = (n <= 1 ? 1 : n <= 2 ? 2 : n <= 2.5 ? 2.5 : n <= 5 ? 5 : 10) * pow
  return { top: step * Math.ceil(max / step), step }
}

/** niceScale for byte quantities: steps are round in the unit the axis
 * labels use (KB, MB, … with 1024-based units), e.g. 5 MB rather than 4.8. */
export function niceBytesScale(max: number, ticks = 4): { top: number; step: number } {
  if (!(max > 0)) return { top: 1024, step: 256 }
  max = Math.max(max, 8) // whole-byte ticks for a near-idle axis, never 0.25 B
  const unit = 1024 ** Math.max(0, Math.min(4, Math.floor(Math.log(max) / Math.log(1024))))
  const s = niceScale(max / unit, ticks)
  return { top: s.top * unit, step: s.step * unit }
}
