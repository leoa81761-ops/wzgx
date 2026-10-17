<template>
  <span v-if="!r || r.checked_at === undefined && r.ok === undefined" class="muted">未检查</span>
  <span v-else-if="r.ok" class="ok">正常 <span class="muted">{{ fmtTime(at || r.checked_at) }}</span></span>
  <span v-else class="bad">{{ r.hint || r.message }} <span class="muted">{{ fmtTime(at || r.checked_at) }}</span></span>
</template>
<script setup>
import { computed } from 'vue'
import { fmtTime } from '../api.js'
const p = defineProps({ report: [Object, null], at: [String, null] })
const r = computed(() => (p.report && Object.keys(p.report).length ? p.report : null))
</script>
