import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { Sample } from '../api'

/** One SSE connection shared by every component on the page. */
const samples = ref<Sample[]>([])
const state = ref<'connecting' | 'ok' | 'noclash' | 'error'>('connecting')
const lastError = ref('')
let es: EventSource | null = null
let users = 0

async function open() {
  // EventSource hides HTTP status codes, so probe once to detect "no Clash API".
  const probe = await fetch('/api/realtime', { headers: { Accept: 'text/event-stream' } }).catch(() => null)
  probe?.body?.cancel()
  if (probe?.status === 409) {
    state.value = 'noclash'
    return
  }
  if (users === 0) return
  es = new EventSource('/api/realtime')
  es.addEventListener('history', (ev) => {
    samples.value = JSON.parse((ev as MessageEvent).data) ?? []
    state.value = 'ok'
  })
  es.addEventListener('sample', (ev) => {
    const { sample, error } = JSON.parse((ev as MessageEvent).data)
    lastError.value = error ?? ''
    const next = samples.value.concat(sample)
    samples.value = next.length > 300 ? next.slice(next.length - 300) : next
  })
  es.onerror = () => {
    if (state.value !== 'ok') state.value = 'error'
  }
}

export function useRealtime() {
  onMounted(() => {
    if (users++ === 0) open()
  })
  onBeforeUnmount(() => {
    if (--users === 0) {
      es?.close()
      es = null
    }
  })
  const latest = computed(() => samples.value.at(-1))
  return { samples, state, lastError, latest }
}
