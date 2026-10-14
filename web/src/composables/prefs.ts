// Per-browser UI preferences. Storage can be unavailable (private mode,
// blocked site data), so every access is guarded and failures are ignored.
const PREFIX = 'sbw.'

export function readPref(key: string): string | null {
  try {
    return localStorage.getItem(PREFIX + key)
  } catch {
    return null
  }
}

export function writePref(key: string, value: string): void {
  try {
    localStorage.setItem(PREFIX + key, value)
  } catch {
    // ignore
  }
}
