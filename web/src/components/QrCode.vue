<script setup lang="ts">
import { ref, watchEffect } from 'vue'
import QRCode from 'qrcode'

const props = defineProps<{ text: string; size?: number }>()
const src = ref('')
watchEffect(async () => {
  src.value = props.text
    ? await QRCode.toDataURL(props.text, { margin: 1, width: props.size ?? 220, errorCorrectionLevel: 'M' })
    : ''
})
</script>

<template>
  <img v-if="src" :src="src" :width="size ?? 220" :height="size ?? 220" alt="QR code" class="qr" />
</template>

<style scoped>
.qr {
  display: block;
  border-radius: 6px;
  background: #fff;
}
</style>
