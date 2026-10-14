import { onBeforeUnmount, onMounted, ref } from 'vue'

const MOBILE_QUERY = '(max-width: 767px)'

export function useIsMobile() {
  const mq = window.matchMedia(MOBILE_QUERY)
  const isMobile = ref(mq.matches)
  const update = (e: MediaQueryListEvent) => (isMobile.value = e.matches)
  onMounted(() => mq.addEventListener('change', update))
  onBeforeUnmount(() => mq.removeEventListener('change', update))
  return isMobile
}
