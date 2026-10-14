<script setup lang="ts">
import { NText } from 'naive-ui'

defineProps<{ label: string; mono?: boolean }>()
</script>

<template>
  <div class="row">
    <NText depth="3" class="label">{{ label }}</NText>
    <span class="value" :class="{ mono }"><slot /></span>
  </div>
</template>

<style scoped>
.row {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 3px 0;
  font-size: 13px;
}
.label {
  flex-shrink: 0;
}
.value {
  text-align: right;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
</style>
