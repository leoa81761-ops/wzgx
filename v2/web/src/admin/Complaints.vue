<template>
  <h1>投诉处理</h1>
  <div class="row" style="gap:8px;margin-bottom:12px">
    <button v-for="t in [['new', '待处理'], ['handled', '已处理'], ['', '全部']]" :key="t[0]" :class="{ primary: st === t[0] }" @click="st = t[0]; load()">{{ t[1] }}</button>
  </div>
  <div v-if="msg" class="msg" :class="{ ok: msg.ok }">{{ msg.text }}</div>
  <div v-for="c in list" :key="c.id" class="card">
    <div class="row" style="justify-content:space-between;flex-wrap:wrap">
      <b>#{{ c.id }} {{ kinds[c.kind] || c.kind }}</b>
      <span class="muted">{{ fmtTime(c.created_at) }} · 来自 {{ c.ip || '未知' }}</span>
    </div>
    <p style="white-space:pre-wrap;margin:8px 0">{{ c.content }}</p>
    <p v-if="c.link" class="muted" style="margin:0;word-break:break-all">链接：{{ c.link }}</p>
    <p v-if="c.contact" class="muted" style="margin:0">联系方式：{{ c.contact }}</p>
    <div class="row" style="margin-top:8px;gap:8px;flex-wrap:wrap">
      <input v-model="c.note" placeholder="处理备注，比如：已核实并删除文章" maxlength="500" style="flex:1;min-width:220px" />
      <button v-if="c.status === 'new'" class="primary" @click="handle(c, 'handled')">标记已处理</button>
      <template v-else><span class="muted nowrap">{{ c.handled_by }} {{ fmtTime(c.handled_at) }} 处理</span><button @click="handle(c, 'handled')">保存备注</button><button @click="handle(c, 'new')">改回待处理</button></template>
    </div>
  </div>
  <p v-if="!list.length" class="muted">没有投诉。</p>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import { get, put, fmtTime } from '../api.js'
const kinds = { infringe: '侵权', false: '内容不实', illegal: '违法违规', privacy: '泄露个人信息', other: '其他' }
const st = ref('new'), list = ref([]), msg = ref(null)
async function load() { try { list.value = await get('/api/admin/complaints?status=' + st.value) } catch (e) { msg.value = { ok: false, text: e.message } } }
async function handle(c, s) { try { await put(`/api/admin/complaints/${c.id}`, { status: s, note: c.note }); msg.value = { ok: true, text: `#${c.id} 已更新` }; load() } catch (e) { msg.value = { ok: false, text: e.message } } }
onMounted(load)
</script>
