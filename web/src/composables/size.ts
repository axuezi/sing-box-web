import { onBeforeUnmount, onMounted, ref, type Ref } from 'vue'

/** Tracks an element's content width. */
export function useWidth(el: Ref<HTMLElement | undefined>) {
  const width = ref(0)
  let ro: ResizeObserver | null = null
  onMounted(() => {
    ro = new ResizeObserver((entries) => (width.value = Math.floor(entries[0]!.contentRect.width)))
    if (el.value) ro.observe(el.value)
  })
  onBeforeUnmount(() => ro?.disconnect())
  return width
}
