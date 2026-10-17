<template>
  <h1>操作日志</h1>
  <div class="row" style="margin-bottom:12px"><input v-model.trim="action" placeholder="按操作筛选，例如 account. 或 article.push" style="max-width:300px" @keyup.enter="load(0)" /><button @click="load(0)">筛选</button></div>
  <div v-if="err" class="msg">{{ err }}</div>
  <table>
    <tr><th>时间</th><th>操作人</th><th>操作</th><th>对象</th><th>详情</th></tr>
    <tr v-for="l in list" :key="l.id">
      <td class="muted" style="white-space:nowrap">{{ fmtTime(l.at) }}</td><td>{{ l.actor || '系统' }}</td><td>{{ l.action }}</td>
      <td class="muted">{{ l.target }}</td><td class="muted" style="font-size:12px">{{ JSON.stringify(l.detail) }}</td>
    </tr>
  </table>
  <div class="row" style="margin-top:10px"><button :disabled="offset === 0" @click="load(offset - 100)">上一页</button><button :disabled="list.length < 100" @click="load(offset + 100)">下一页</button></div>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import { get, fmtTime } from '../api.js'
const list = ref([]), action = ref(''), offset = ref(0), err = ref('')
async function load(off) {
  try { list.value = await get(`/api/admin/audit?offset=${off}&action=${encodeURIComponent(action.value)}`); offset.value = off } catch (e) { err.value = e.message }
}
onMounted(() => load(0))
</script>
