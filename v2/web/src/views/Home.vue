<template>
  <h1>首页</h1>
  <div v-if="err" class="msg">{{ err }}</div>
  <div class="grid" v-if="t">
    <div class="stat"><span class="muted">今日已写</span><b>{{ t.used_today }}<small v-if="!t.quota_exempt" class="muted"> / {{ t.daily_quota }}</small></b>
      <span class="muted">{{ t.quota_exempt ? '不限额度' : '每日额度' }}</span></div>
    <div class="stat"><span class="muted">正在写</span><b>{{ t.running }}</b><span class="muted">排队 {{ t.queued }}</span></div>
    <div class="stat"><span class="muted">可推送文章</span><b>{{ t.ready_articles }}</b></div>
    <div class="stat"><span class="muted">已绑定公众号</span><b>{{ t.accounts }}</b></div>
  </div>
  <div v-if="t && !t.accounts" class="card" style="margin-top:16px">还没有绑定公众号。<router-link to="/accounts">去绑定</router-link></div>
  <h2>最新文章</h2>
  <table v-if="arts.length">
    <tr><th>标题</th><th>状态</th><th>时间</th></tr>
    <tr v-for="a in arts" :key="a.id">
      <td><router-link :to="{ path: '/articles', query: { id: a.id } }">{{ a.title || a.topic || '（无标题）' }}</router-link></td>
      <td><span class="tag" :class="a.status">{{ statusText[a.status] || a.status }}</span></td>
      <td class="muted">{{ fmtTime(a.created_at) }}</td>
    </tr>
  </table>
  <p v-else class="muted">还没有文章。</p>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import { get, fmtTime, statusText } from '../api.js'
const t = ref(null), arts = ref([]), err = ref('')
onMounted(async () => {
  try { t.value = await get('/api/me/today'); arts.value = (await get('/api/articles')).slice(0, 8) } catch (e) { err.value = e.message }
})
</script>
