<template>
  <div class="pw"><input :value="modelValue" @input="$emit('update:modelValue', trim ? $event.target.value.trim() : $event.target.value)"
    :type="show ? 'text' : 'password'" :autocomplete="autocomplete" :placeholder="placeholder" :required="required" />
    <button type="button" class="eye" @click="show = !show">{{ show ? '隐藏' : '显示' }}</button></div>
</template>
<script setup>
// password-style input with a show/hide toggle
import { ref } from 'vue'
defineProps({ modelValue: { type: String, default: '' }, autocomplete: { type: String, default: 'off' }, placeholder: String, required: Boolean, trim: Boolean })
defineEmits(['update:modelValue'])
const show = ref(false)
</script>
