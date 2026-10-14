<script setup lang="ts">
import { NTag } from 'naive-ui'
import type { AuditEntry } from '../api'
import { formatDateTime } from '../composables/format'
import { auditTagType, useAuditDetail, useAuditLabel } from '../composables/audit'

// One audit entry on two lines, for narrow screens: what happened and to
// what, then who, from where and when.
defineProps<{ entry: AuditEntry }>()
const label = useAuditLabel()
const detail = useAuditDetail()
</script>

<template>
  <div class="arow">
    <div class="line">
      <NTag size="small" :type="auditTagType(entry.action)" :bordered="false" class="tag">{{ label(entry.action) }}</NTag>
      <span v-if="entry.target" class="target">{{ entry.target }}</span>
      <span v-if="entry.detail" class="detail" :title="entry.detail">{{ detail(entry.detail) }}</span>
    </div>
    <div class="line sub">
      <span class="who">{{ entry.actor }}</span>
      <span v-if="entry.ip" class="mono">{{ entry.ip }}</span>
      <span class="when">{{ formatDateTime(entry.at) }}</span>
    </div>
  </div>
</template>

<style scoped>
.arow {
  padding: 9px 0;
  border-bottom: 1px solid var(--sbw-border);
  font-size: 13px;
  min-width: 0;
}
.line {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.sub {
  margin-top: 4px;
  font-size: 12px;
  color: var(--viz-muted);
}
.tag {
  flex: none;
}
.target {
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
}
.detail {
  color: var(--viz-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
}
.who {
  white-space: nowrap;
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
}
.when {
  margin-left: auto;
  flex: none;
  font-variant-numeric: tabular-nums;
}
</style>
