<script setup lang="ts">
import { computed } from 'vue'
import { formatBytes } from '../../composables/format'

const props = defineProps<{ items: { name: string; down: number; up: number }[]; empty: string; labels: { down: string; up: string } }>()
const rows = computed(() =>
  [...props.items].map((i) => ({ ...i, total: i.down + i.up })).sort((a, b) => b.total - a.total),
)
const max = computed(() => Math.max(1, ...rows.value.map((r) => r.total)))
const pct = (v: number) => `${(v / max.value) * 100}%`
</script>

<template>
  <div class="rank">
    <div v-if="rows.length" class="viz-legend legend">
      <span><i class="viz-key" style="background: var(--viz-down)" />{{ labels.down }}</span>
      <span><i class="viz-key" style="background: var(--viz-up)" />{{ labels.up }}</span>
    </div>
    <div v-for="r in rows" :key="r.name" class="row" :title="`${labels.down} ${formatBytes(r.down)} · ${labels.up} ${formatBytes(r.up)}`">
      <span class="name">{{ r.name }}</span>
      <span class="track">
        <span class="bar down" :style="{ width: pct(r.down) }" />
        <span v-if="r.up" class="bar up" :style="{ width: pct(r.up) }" />
      </span>
      <span class="value">{{ formatBytes(r.total) }}</span>
    </div>
    <div v-if="!rows.length" class="empty">{{ empty }}</div>
  </div>
</template>

<style scoped>
.legend {
  margin-bottom: 8px;
}
.row {
  display: grid;
  grid-template-columns: minmax(64px, 140px) 1fr auto;
  align-items: center;
  gap: 12px;
  height: 32px;
  font-size: 13px;
}
.name {
  color: var(--viz-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.track {
  display: flex;
  gap: 2px;
  height: 8px;
}
.bar {
  height: 100%;
  min-width: 0;
}
.bar.down {
  background: var(--viz-down);
  border-radius: 0;
}
.bar.up {
  background: var(--viz-up);
}
.track .bar:first-child {
  border-radius: 4px 0 0 4px;
}
.track .bar:last-child {
  border-radius: 0 4px 4px 0;
}
.track .bar:only-child {
  border-radius: 4px;
}
.value {
  color: var(--viz-text-2);
  font-variant-numeric: tabular-nums;
  min-width: 64px;
  text-align: right;
}
.empty {
  color: var(--viz-muted);
  font-size: 13px;
  padding: 8px 0;
}
</style>
