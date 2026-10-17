<template>
  <h1>写作计划</h1>
  <div v-if="err" class="msg">{{ err }}</div>
  <p v-if="!accounts.length" class="muted">先去<router-link to="/accounts">绑定公众号</router-link>。</p>
  <div class="card" v-for="a in accounts" :key="a.id">
    <div class="row" style="justify-content:space-between">
      <h2 style="margin:0">{{ a.name }}</h2>
      <div class="row">
        <select v-model="now[a.id]" style="width:auto"><option value="">按栏目占比自动选</option>
          <option v-for="c in cols[a.id]" :key="c.id" :value="c.id">{{ c.name }}</option></select>
        <button class="primary" @click="writeNow(a)" :disabled="busy[a.id]">{{ busy[a.id] ? '正在提交…' : '现在写一篇' }}</button>
      </div>
    </div>
    <div v-if="fb[a.id]" class="msg" :class="{ ok: fb[a.id].ok }" style="margin-top:10px">{{ fb[a.id].text }}
      <router-link v-if="fb[a.id].ok" to="/articles">去文章页查看</router-link></div>
    <p class="muted">计划按北京时间运行：到点自动写一篇放进「文章」，不会自动推送到公众号。</p>
    <table v-if="plans[a.id]?.length">
      <tr><th>星期</th><th>时间（北京时间）</th><th>栏目</th><th>启用</th><th>上次运行</th><th class="act"></th></tr>
      <tr v-for="p in plans[a.id]" :key="p.id">
        <td><label v-for="d in 7" :key="d" style="margin-right:6px;white-space:nowrap"><input type="checkbox" :value="d" v-model="p.weekdays" />{{ weekNames[d] }}</label></td>
        <td class="nowrap"><TimePick v-model="p.time" /></td>
        <td><select v-model="p.column_id"><option :value="null">按占比自动选</option>
          <option v-for="c in cols[a.id]" :key="c.id" :value="c.id">{{ c.name }}</option></select></td>
        <td><input type="checkbox" v-model="p.enabled" /></td>
        <td class="muted">{{ p.last_result || '还没运行过' }}</td>
        <td class="act"><div class="row"><button @click="save(a, p)">保存</button><button class="danger" @click="remove(a, p)">删除</button></div></td>
      </tr>
    </table>
    <p v-else class="muted">还没有计划。</p>
    <div v-if="pm[a.id]" class="msg" :class="{ ok: pm[a.id].ok }">{{ pm[a.id].text }}</div>
    <button class="near" @click="add(a)">添加计划</button>
  </div>
</template>
<script setup>
import { ref, reactive, onMounted } from 'vue'
import { get, post, put, del, weekNames } from '../api.js'
import TimePick from './TimePick.vue'
const accounts = ref([]), plans = reactive({}), cols = reactive({}), now = reactive({})
const err = ref(''), busy = reactive({}), fb = reactive({}), pm = reactive({})
const say = (a, text, ok = false) => { pm[a.id] = { text, ok } }
async function loadAcc(a) { plans[a.id] = await get(`/api/accounts/${a.id}/plans`); cols[a.id] = (await get(`/api/accounts/${a.id}/columns`)).filter((c) => c.enabled) }
async function save(a, p) {
  try { await (p.id ? put(`/api/accounts/${a.id}/plans/${p.id}`, p) : post(`/api/accounts/${a.id}/plans`, p)); await loadAcc(a); say(a, '计划已保存', true) } catch (e) { say(a, e.message) }
}
function add(a) { plans[a.id] = [...(plans[a.id] || []), { weekdays: [1, 3, 5], time: '08:00', column_id: null, enabled: true }] }
async function remove(a, p) {
  if (p.id && !confirm('删除这个计划？')) return
  try { if (p.id) await del(`/api/accounts/${a.id}/plans/${p.id}`); await loadAcc(a) } catch (e) { say(a, e.message) }
}
async function writeNow(a) {
  busy[a.id] = true; fb[a.id] = null
  try {
    const body = now[a.id] ? { column_id: now[a.id] } : {}
    await post(`/api/accounts/${a.id}/generate`, body)
    fb[a.id] = { ok: true, text: '已开始写作，几分钟后在「文章」里能看到。' }
  } catch (e) { fb[a.id] = { ok: false, text: e.message } } finally { busy[a.id] = false }
}
onMounted(async () => {
  try { accounts.value = await get('/api/accounts'); for (const a of accounts.value) { now[a.id] = ''; await loadAcc(a) } } catch (e) { err.value = e.message }
})
</script>
