<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NInput, NSelect, NSpace, NSwitch, NTag, NText, NVirtualList } from 'naive-ui'
import type { LogLine } from '../../api'
import { useIsMobile } from '../../composables/viewport'

const MAX_LINES = 5000
const { t } = useI18n()
const isMobile = useIsMobile()

interface Row extends LogLine {
  key: number
}

const level = ref<string>('')
const query = ref('')
const appliedQuery = ref('')
const paused = ref(false)
const follow = ref(true)
const conn = ref<'connecting' | 'connected' | 'disconnected'>('connecting')
const dropped = ref(0)

const rows = ref<Row[]>([])
const buffer: Row[] = []
let seq = 0
let es: EventSource | null = null
const list = ref<InstanceType<typeof NVirtualList>>()

const levelOptions = computed(() => [
  { value: '', label: t('logs.allLevels') },
  { value: 'debug', label: 'DEBUG+' },
  { value: 'info', label: 'INFO+' },
  { value: 'warn', label: 'WARN+' },
  { value: 'error', label: 'ERROR+' },
])

function push(items: Row[]) {
  if (!items.length) return
  const next = rows.value.concat(items)
  rows.value = next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next
  if (follow.value) nextTick(() => list.value?.scrollTo({ position: 'bottom' }))
}

// Batch incoming lines into one render per animation frame.
let pending: Row[] = []
let frame = 0
function enqueue(r: Row) {
  if (paused.value) {
    buffer.push(r)
    if (buffer.length > MAX_LINES) buffer.shift()
    return
  }
  pending.push(r)
  if (!frame) {
    frame = requestAnimationFrame(() => {
      frame = 0
      const batch = pending
      pending = []
      push(batch)
    })
  }
}

function connect() {
  es?.close()
  rows.value = []
  buffer.length = 0
  conn.value = 'connecting'
  const params = new URLSearchParams({ backlog: '500' })
  if (level.value) params.set('level', level.value)
  if (appliedQuery.value) params.set('q', appliedQuery.value)
  es = new EventSource(`/api/logs/stream?${params}`)
  es.addEventListener('line', (ev) => enqueue({ key: seq++, ...(JSON.parse((ev as MessageEvent).data) as LogLine) }))
  es.addEventListener('ready', () => (conn.value = 'connected'))
  es.addEventListener('dropped', (ev) => (dropped.value += JSON.parse((ev as MessageEvent).data).count))
  es.onopen = () => (conn.value = 'connected')
  es.onerror = () => (conn.value = 'disconnected') // EventSource retries on its own
}

let debounce = 0
watch(query, (q) => {
  clearTimeout(debounce)
  debounce = window.setTimeout(() => {
    appliedQuery.value = q.trim()
  }, 400)
})
watch([level, appliedQuery], connect)
watch(paused, (p) => {
  if (!p) push(buffer.splice(0))
})

function clear() {
  rows.value = []
  dropped.value = 0
}

function onScroll(e: Event) {
  const el = e.target as HTMLElement
  follow.value = el.scrollHeight - el.scrollTop - el.clientHeight < 40
}

onMounted(connect)
onBeforeUnmount(() => {
  es?.close()
  cancelAnimationFrame(frame)
})

const levelClass = (l: string) => `lv-${(l || 'raw').toLowerCase()}`
</script>

<template>
  <div class="live">
    <div class="toolbar">
      <NSelect v-model:value="level" :options="levelOptions" size="small" class="level" />
      <NInput v-model:value="query" size="small" clearable :placeholder="t('logs.search')" class="search" />
      <NSpace :size="8" align="center" :wrap="false">
        <NButton size="small" @click="paused = !paused">{{ paused ? t('logs.resume') : t('logs.pause') }}</NButton>
        <NButton size="small" @click="clear">{{ t('logs.clear') }}</NButton>
        <NButton size="small" tag="a" href="/api/logs/download">{{ t('logs.download') }}</NButton>
      </NSpace>
      <div class="state">
        <NSwitch v-model:value="follow" size="small" />
        <NText depth="3">{{ t('logs.follow') }}</NText>
        <NTag size="small" round :bordered="false"
          :type="conn === 'connected' ? 'success' : conn === 'connecting' ? 'default' : 'warning'">
          {{ t(`logs.${conn}`) }}
        </NTag>
      </div>
    </div>
    <NText v-if="paused" depth="3" class="hint">{{ t('logs.paused', { n: buffer.length }) }}</NText>
    <NText v-if="dropped" type="warning" class="hint">{{ t('logs.dropped', { n: dropped }) }}</NText>

    <div class="screen">
      <NVirtualList
        v-if="rows.length"
        ref="list"
        :items="rows"
        :item-size="22"
        :item-resizable="isMobile"
        key-field="key"
        class="vlist"
        @scroll="onScroll"
      >
        <template #default="{ item }">
          <div class="line" :class="[levelClass(item.level), { wrap: isMobile }]">
            <span class="time">{{ item.time?.slice(11) }}</span>
            <span class="lvl">{{ item.level || '' }}</span>
            <span class="text">{{ item.text }}</span>
          </div>
        </template>
      </NVirtualList>
      <div v-else class="empty">
        <NText depth="3">{{ conn === 'connected' ? t('logs.empty') : t('logs.connecting') }}</NText>
      </div>
    </div>
  </div>
</template>

<style scoped>
.live {
  display: flex;
  flex-direction: column;
  gap: 8px;
  height: 100%;
}
.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.level {
  width: 120px;
}
.search {
  width: 220px;
  max-width: 100%;
}
.state {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
  font-size: 12px;
}
.hint {
  font-size: 12px;
}
.screen {
  flex: 1;
  min-height: 300px;
  border-radius: 6px;
  background: #0f1117;
  color: #c9d1d9;
  overflow: hidden;
}
.vlist {
  height: 100%;
}
.line {
  display: flex;
  gap: 10px;
  height: 22px;
  line-height: 22px;
  padding: 0 12px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  white-space: nowrap;
}
.time {
  color: #6e7681;
  flex-shrink: 0;
}
.lvl {
  width: 40px;
  flex-shrink: 0;
  font-weight: 600;
}
.text {
  overflow: hidden;
  text-overflow: ellipsis;
}
.line:hover .text {
  white-space: normal;
  overflow: visible;
}
/* phones: no room to hover, so long lines wrap instead of being cut */
.line.wrap {
  display: block;
  height: auto;
  min-height: 22px;
  line-height: 18px;
  padding: 2px 10px;
  white-space: normal;
  overflow-wrap: anywhere;
}
.line.wrap .time {
  margin-right: 8px;
}
.line.wrap .lvl {
  display: inline-block;
  width: auto;
  margin-right: 8px;
}
.line.wrap .text {
  overflow: visible;
}
.lv-info .lvl { color: #58a6ff; }
.lv-debug .lvl, .lv-trace .lvl { color: #8b949e; }
.lv-warn .lvl { color: #d29922; }
.lv-error .lvl, .lv-fatal .lvl, .lv-panic .lvl { color: #f85149; }
.lv-error .text, .lv-fatal .text { color: #ffa198; }
.empty {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
}
@media (max-width: 767px) {
  .state {
    margin-left: 0;
  }
  .search {
    width: 100%;
  }
}
</style>
