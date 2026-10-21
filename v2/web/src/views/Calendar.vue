<template>
  <h1>内容日历</h1>
  <p v-if="!accounts.length" class="muted">先去<router-link to="/accounts">绑定公众号</router-link>。</p>
  <template v-else>
    <div class="row" style="gap:8px;flex-wrap:wrap;margin-bottom:10px">
      <select v-model="accId" style="width:auto" @change="load">
        <option v-for="a in accounts" :key="a.id" :value="a.id">{{ a.name }}</option>
      </select>
      <button @click="shift(-1)">‹ 上个月</button>
      <b style="min-width:90px;text-align:center">{{ monthLabel }}</b>
      <button @click="shift(1)">下个月 ›</button>
      <button @click="month = ''; load()">回到本月</button>
    </div>
    <p class="muted">实色是已经写好的文章（点开可预览），虚线框是写作计划接下来会写的时间。<router-link to="/plans">去改写作计划</router-link></p>
    <div v-if="cal?.paused" class="msg">这个公众号已暂停，计划暂时不会运行，所以日历上没有排上的计划。</div>
    <div v-if="err" class="msg">{{ err }}</div>
    <div v-if="cal" class="cal">
      <div v-for="w in weekNames.slice(1)" :key="w" class="wd">{{ w }}</div>
      <div v-for="n in lead" :key="'b' + n" class="day other"></div>
      <div v-for="d in cal.days" :key="d.date" class="day" :class="{ today: d.date === cal.today, past: d.date < cal.today }">
        <div class="num">{{ Number(d.date.slice(8)) }}</div>
        <router-link v-for="a in d.articles" :key="a.id" class="it art" :class="a.status" :to="`/articles?id=${a.id}`" :title="`${a.time} ${a.title}（${statusText[a.status] || a.status}）`">
          {{ a.time }} {{ a.title || '（无标题）' }}
        </router-link>
        <span v-for="p in d.planned" :key="p.plan_id" class="it plan" :title="`计划 ${p.time} · ${p.column}`">{{ p.time }} 计划 · {{ p.column }}</span>
      </div>
    </div>
    <p v-if="cal" class="muted" style="margin-top:10px">本月 {{ total.articles }} 篇文章，还有 {{ total.planned }} 次计划。</p>
  </template>
</template>
<script setup>
import { ref, computed, onMounted } from 'vue'
import { get, statusText, weekNames } from '../api.js'
const accounts = ref([]), accId = ref(null), month = ref(''), cal = ref(null), err = ref('')
const lead = computed(() => (cal.value?.days.length ? cal.value.days[0].weekday - 1 : 0))
const monthLabel = computed(() => (cal.value ? `${cal.value.month.slice(0, 4)} 年 ${Number(cal.value.month.slice(5))} 月` : ''))
const total = computed(() => (cal.value?.days || []).reduce((t, d) => ({ articles: t.articles + d.articles.length, planned: t.planned + d.planned.length }), { articles: 0, planned: 0 }))
async function load() {
  err.value = ''
  try { cal.value = await get(`/api/accounts/${accId.value}/calendar` + (month.value ? '?month=' + month.value : '')); month.value = cal.value.month }
  catch (e) { err.value = e.message }
}
function shift(n) {
  const [y, m] = (month.value || cal.value.month).split('-').map(Number)
  const d = new Date(y, m - 1 + n, 1)
  month.value = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`
  load()
}
onMounted(async () => {
  try {
    accounts.value = (await get('/api/accounts')).filter((a) => !a.bind_status || a.bind_status === 'bound')
    if (accounts.value.length) { accId.value = accounts.value[0].id; await load() }
  } catch (e) { err.value = e.message }
})
</script>
