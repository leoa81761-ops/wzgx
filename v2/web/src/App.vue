<script setup>
import { ref, onMounted } from 'vue'

// User menu has at most 5 top-level items (rebuild_plan §4.8). Admin console is separate.
const userMenu = ['首页', '公众号', '写作计划', '文章', '我的']
const ver = ref(null)
onMounted(async () => {
  try { ver.value = await (await fetch('/api/version')).json() } catch { /* offline */ }
})
</script>

<template>
  <header class="top">
    <strong>公号智写</strong>
    <nav><a v-for="m in userMenu" :key="m" href="#">{{ m }}</a></nav>
  </header>
  <main><p>v2 骨架。</p></main>
  <footer v-if="ver">版本 {{ ver.commit }} · schema {{ ver.schema }}</footer>
</template>

<style>
body { font-family: system-ui, sans-serif; margin: 0; }
.top { display: flex; gap: 24px; align-items: center; padding: 12px 20px; border-bottom: 1px solid #eee; }
.top nav a { margin-right: 16px; color: #333; text-decoration: none; }
main, footer { padding: 20px; }
footer { color: #999; font-size: 12px; }
</style>
