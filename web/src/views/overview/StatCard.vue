<script setup lang="ts">
import type { Component } from 'vue'

defineProps<{ label: string; value: string; sub?: string; icon: Component; tone?: 'down' | 'up' | 'green' | 'violet' }>()
</script>

<template>
  <div class="stat">
    <div class="icon" :class="tone ?? 'green'"><component :is="icon" :size="20" :stroke-width="1.8" /></div>
    <div class="body">
      <div class="label">{{ label }}</div>
      <div class="value">{{ value }}</div>
      <div v-if="sub" class="sub">{{ sub }}</div>
    </div>
  </div>
</template>

<style scoped>
.stat {
  display: flex;
  gap: 14px;
  align-items: flex-start;
  height: 100%;
  padding: 18px 20px;
  border-radius: 12px;
  background: var(--sbw-card);
  border: 1px solid var(--sbw-border);
  box-sizing: border-box;
}
.icon {
  display: grid;
  place-items: center;
  width: 40px;
  height: 40px;
  border-radius: 10px;
  flex-shrink: 0;
}
.icon.down {
  color: var(--viz-down);
  background: color-mix(in srgb, var(--viz-down) 14%, transparent);
}
.icon.up {
  color: var(--viz-up);
  background: color-mix(in srgb, var(--viz-up) 14%, transparent);
}
.icon.green {
  color: #18a058;
  background: color-mix(in srgb, #18a058 14%, transparent);
}
.icon.violet {
  color: #7c6bd6;
  background: color-mix(in srgb, #7c6bd6 15%, transparent);
}
.body {
  min-width: 0;
}
.label {
  font-size: 13px;
  color: var(--viz-text-2);
}
.value {
  margin-top: 2px;
  font-size: 24px;
  font-weight: 650;
  letter-spacing: -0.02em;
  font-variant-numeric: tabular-nums;
  color: var(--viz-text);
  white-space: nowrap;
}
.sub {
  margin-top: 2px;
  font-size: 12px;
  color: var(--viz-muted);
}
@media (max-width: 767px) {
  .stat {
    flex-direction: column;
    gap: 10px;
    padding: 14px;
  }
  .icon {
    width: 32px;
    height: 32px;
    border-radius: 8px;
  }
  .value {
    font-size: 19px;
  }
  .sub {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
}
</style>
