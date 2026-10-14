import { h } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDialog, useMessage } from 'naive-ui'
import { ApiError, type ApplyResult } from '../api'

/**
 * Shows the outcome of anything that changes config.json: a toast on
 * success, and a dialog with sing-box's own output when the change was
 * rejected or rolled back.
 */
export function useApplyFeedback() {
  const { t } = useI18n()
  const message = useMessage()
  const dialog = useDialog()

  const pre = (text: string) =>
    h('pre', { style: 'white-space:pre-wrap;word-break:break-word;font-size:12px;max-height:50vh;overflow:auto;margin:8px 0 0' }, text)

  function success(result?: ApplyResult) {
    if (!result || !result.changed) return message.success(t('apply.saved'))
    if (result.status === 'written') return message.warning(t('apply.written'), { duration: 6000 })
    message.success(t('apply.applied'))
  }

  /**
   * Explains a failed change. A network error usually means the reload of
   * sing-box dropped our own connection (the admin is often browsing through
   * it): the change itself continues on the server, so `recheck` runs once
   * the connection is back to show what really happened.
   */
  function failure(e: unknown, recheck?: () => unknown): boolean {
    if (isNetworkError(e)) {
      message.warning(t('apply.disconnected'), { duration: 8000 })
      if (recheck) {
        setTimeout(recheck, 3000)
        setTimeout(recheck, 8000)
      }
      return true
    }
    if (!(e instanceof ApiError)) {
      message.error(t('common.requestFailed', { msg: e instanceof Error ? e.message : String(e) }))
      return false
    }
    if (e.code === 'check_failed') {
      dialog.error({
        title: t('apply.rejectedTitle'),
        content: () => h('div', [t('apply.rejectedBody'), pre(String(e.body.output ?? ''))]),
        positiveText: t('common.confirm'),
      })
      return true
    }
    if (e.code === 'recovery_failed') {
      const r = e.body.result as ApplyResult | undefined
      dialog.error({
        title: t('apply.recoveryFailedTitle'),
        content: () => h('div', [t('apply.recoveryFailedBody'), pre(r?.log || e.message)]),
        positiveText: t('common.confirm'),
      })
      return true
    }
    if (e.code === 'rolled_back') {
      const r = e.body.result as ApplyResult | undefined
      dialog.warning({
        title: t('apply.rolledBackTitle'),
        content: () => h('div', [t('apply.rolledBackBody'), pre(r?.log ?? '')]),
        positiveText: t('common.confirm'),
      })
      return true
    }
    if (e.code === 'referenced') {
      const refs = (e.body.references as string[] | undefined) ?? []
      message.error(t('apply.referenced', { refs: refs.join(', ') }), { duration: 8000 })
      return true
    }
    if (e.code === 'tag_taken') {
      message.error(t('apply.tagTaken'))
      return true
    }
    if (e.code === 'stale') {
      message.warning(t('apply.stale'), { duration: 6000 })
      return true
    }
    message.error(e.message)
    return false
  }

  return { success, failure }
}

/** fetch() rejects with a TypeError when the connection itself fails. */
export function isNetworkError(e: unknown): boolean {
  return e instanceof TypeError
}

export function copyText(text: string): Promise<void> {
  if (navigator.clipboard && window.isSecureContext) return navigator.clipboard.writeText(text)
  // Fallback for plain-http development
  const ta = document.createElement('textarea')
  ta.value = text
  ta.style.position = 'fixed'
  ta.style.opacity = '0'
  document.body.appendChild(ta)
  ta.select()
  document.execCommand('copy')
  ta.remove()
  return Promise.resolve()
}
