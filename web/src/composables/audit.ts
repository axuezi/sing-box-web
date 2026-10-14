import { useI18n } from 'vue-i18n'

/** Human label for an audit action like "login.failure". */
export function useAuditLabel() {
  const { t, te } = useI18n()
  return (action: string) => {
    const key = `audit.${action.replace(/\./g, '_')}`
    return te(key) ? t(key) : action
  }
}

/** Audit details hold a few status words ("quota (applied)"); translate those. */
export function useAuditDetail() {
  const { t, te } = useI18n()
  const word = (w: string) => (te(`auditDetail.${w}`) ? t(`auditDetail.${w}`) : w)
  // Only the short forms "applied" / "quota (applied)"; free text such as a
  // rejected config's output is left alone.
  return (detail: string) =>
    /^[a-z]+( \([a-z]+\))?$/.test(detail) ? detail.replace(/[a-z]+/g, word) : detail
}

export function auditTagType(action: string): 'default' | 'success' | 'warning' | 'error' | 'info' {
  if (action === 'config.recovery_failed') return 'error'
  if (action.endsWith('.failure')) return 'warning'
  if (action.endsWith('.locked')) return 'error'
  if (action.endsWith('.success')) return 'success'
  if (action.startsWith('config.')) return 'info'
  return 'default'
}
