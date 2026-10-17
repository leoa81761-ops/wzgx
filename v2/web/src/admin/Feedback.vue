<template>
  <h1>文章反馈</h1>
  <p class="muted">用户对文章的评价和备注，以及推送后草稿有没有被改过。</p>
  <div class="row" style="margin-bottom:12px">
    <select v-model="rating" style="max-width:160px" @change="load"><option :value="0">全部</option><option :value="1">👍 好</option><option :value="-1">👎 不好</option></select>
  </div>
  <div v-if="err" class="msg">{{ err }}</div>
  <table v-if="list.length">
    <tr><th class="nowrap">时间</th><th>文章</th><th>公众号 / 用户</th><th class="nowrap">评价</th><th>备注</th><th class="nowrap">草稿改动</th></tr>
    <tr v-for="f in list" :key="f.article_id">
      <td class="muted nowrap">{{ fmtTime(f.at) }}</td>
      <td>{{ f.title || '（无标题）' }}</td>
      <td>{{ f.account_name }}<div class="muted">{{ f.owner_email }}</div></td>
      <td class="nowrap">{{ f.rating === 1 ? '👍 好' : '👎 不好' }}</td>
      <td>{{ f.note || '—' }}</td>
      <td class="muted nowrap">{{ rb(f) }}</td>
    </tr>
  </table>
  <p v-else-if="!err" class="muted">还没有反馈。</p>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import { get, fmtTime } from '../api.js'
const list = ref([]), rating = ref(0), err = ref('')
const rb = (f) => ({ unedited: '没改', edited: `改过（约 ${Math.round((f.change_ratio || 0) * 100)}%）`, gone: '已不在草稿箱', error: '检查失败' })[f.readback_status] || '—'
async function load() { try { list.value = await get(`/api/admin/feedback?rating=${rating.value}`); err.value = '' } catch (e) { err.value = e.message } }
onMounted(load)
</script>
