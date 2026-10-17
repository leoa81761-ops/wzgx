<template>
  <span class="timepick">
    <select :value="h" @change="set($event.target.value, m)" aria-label="时"><option v-for="x in 24" :key="x" :value="pad(x - 1)">{{ pad(x - 1) }}</option></select>
    <span>:</span>
    <select :value="m" @change="set(h, $event.target.value)" aria-label="分"><option v-for="x in minutes" :key="x" :value="x">{{ x }}</option></select>
  </span>
</template>
<script setup>
// 24-hour time as "HH:MM" without the browser's locale-dependent AM/PM picker.
import { computed } from 'vue'
const props = defineProps({ modelValue: { type: String, default: '08:00' } })
const emit = defineEmits(['update:modelValue'])
const pad = (n) => String(n).padStart(2, '0')
const h = computed(() => (props.modelValue || '08:00').slice(0, 2))
const m = computed(() => (props.modelValue || '08:00').slice(3, 5))
const minutes = computed(() => {
  const l = Array.from({ length: 12 }, (_, i) => pad(i * 5))
  return l.includes(m.value) ? l : [...l, m.value].sort()
})
function set(hh, mm) { emit('update:modelValue', `${hh}:${mm}`) }
</script>
