<template>
  <div class="shell">
    <nav class="nav">
      <div class="brand">公号智写</div>
      <router-link to="/" active-class="" exact-active-class="router-link-active">首页</router-link>
      <router-link to="/accounts">公众号</router-link>
      <router-link to="/plans">写作计划</router-link>
      <router-link to="/calendar">内容日历</router-link>
      <router-link to="/articles">文章</router-link>
      <router-link to="/me">我的</router-link>
      <div class="foot">{{ session.me?.display_name || session.me?.email }}</div>
    </nav>
    <main class="main"><div v-if="session.notice" class="msg ok">{{ session.notice }} <a href="#" @click.prevent="dismiss">知道了</a></div><router-view /></main>
  </div>
</template>
<script setup>
import { onMounted } from 'vue'
import { session } from '../store.js'
import { get } from '../api.js'
// last week's article numbers, once a week, in the notification area
let weekKey = ''
onMounted(async () => {
  try {
    const w = await get('/api/me/weekly')
    if (w.text && localStorage.getItem('weekly-seen') !== w.key && !session.notice) { weekKey = w.key; session.notice = w.text }
  } catch (e) { /* no numbers */ }
})
function dismiss() { if (weekKey) { localStorage.setItem('weekly-seen', weekKey); weekKey = '' } session.notice = '' }
</script>
