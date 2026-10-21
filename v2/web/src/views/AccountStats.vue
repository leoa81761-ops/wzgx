<template>
  <p><router-link :to="`/accounts/${id}`">← 公众号</router-link></p>
  <h1>文章数据</h1>
  <div v-if="err" class="msg">{{ err }}</div>
  <template v-if="s">
    <div v-if="!s.verified" class="card"><p class="muted" style="margin:0">只有认证的公众号能通过微信接口读取文章数据。这个公众号还没有标为已认证。</p></div>
    <template v-else>
      <div class="card">
        <div class="row"><span class="muted">每天上午 9 点后自动从微信读取已发表文章的数据（每篇最多统计发表后 30 天）。</span></div>
        <div class="row" style="margin-top:6px"><span class="muted">上次读取：</span>{{ s.stats_at ? fmtTime(s.stats_at) : '还没有' }}<span v-if="s.note">（{{ s.note }}）</span>
          <button :disabled="busy" @click="refresh">现在更新</button></div>
        <div v-if="msg" class="msg" :class="{ ok: msg.ok }">{{ msg.text }}</div>
      </div>
      <h2>最近 30 天每天新增</h2>
      <div class="card">
        <div class="row legend"><span><i style="background:#3a8f6b"></i>阅读人数</span><span><i style="background:#e0a030"></i>分享人数</span><span><i style="background:#6a7fd0"></i>收藏人数</span></div>
        <svg :viewBox="`0 0 ${W} ${H + 20}`" class="chart">
          <line :x1="0" :y1="H" :x2="W" :y2="H" stroke="#ccc" />
          <g v-for="(d, i) in s.days" :key="d.date">
            <rect :x="i * bw + 1" :y="H - h(d.reads)" :width="bw * 0.5" :height="h(d.reads)" fill="#3a8f6b"><title>{{ d.date }} 阅读 {{ d.reads }}</title></rect>
            <rect :x="i * bw + 1 + bw * 0.5" :y="H - h(d.shares)" :width="bw * 0.22" :height="h(d.shares)" fill="#e0a030"><title>{{ d.date }} 分享 {{ d.shares }}</title></rect>
            <rect :x="i * bw + 1 + bw * 0.72" :y="H - h(d.favs)" :width="bw * 0.22" :height="h(d.favs)" fill="#6a7fd0"><title>{{ d.date }} 收藏 {{ d.favs }}</title></rect>
            <text v-if="i % 5 === 0" :x="i * bw" :y="H + 14" font-size="10" fill="#888">{{ d.date.slice(5) }}</text>
          </g>
          <text x="2" y="10" font-size="10" fill="#888">{{ max }}</text>
        </svg>
        <p class="muted" v-if="!max">还没有数据。</p>
      </div>
      <h2>各篇文章</h2>
      <div class="card">
        <table v-if="s.articles.length">
          <tr><th>标题</th><th>栏目</th><th>发表</th><th>阅读</th><th>分享</th><th>收藏</th><th>点赞</th><th>完读率</th></tr>
          <tr v-for="x in s.articles" :key="x.id">
            <td>{{ x.title }}</td><td class="nowrap">{{ x.column || '—' }}</td><td class="nowrap muted">{{ x.published }}</td>
            <td>{{ x.reads }}</td><td>{{ x.shares }}</td><td>{{ x.favs }}</td><td>{{ x.likes }}</td><td>{{ Math.round(x.finish_rate * 100) }}%</td>
          </tr>
        </table>
        <p v-else class="muted">还没有匹配到已发表的文章。推送到草稿箱、在公众号发表后，第二天就能看到。</p>
      </div>
      <h2>按数据调整栏目轮换</h2>
      <div class="card">
        <label class="row"><input type="checkbox" :checked="s.weighting" @change="weighting($event.target.checked)" />
          <span>让读的人多的栏目稍微多写一些（默认关闭；只在没指定栏目时生效）</span></label>
        <p class="muted">规则：{{ s.rule }}</p>
        <table>
          <tr><th>栏目</th><th>原权重</th><th>有数据的篇数</th><th>平均阅读</th><th>系数</th></tr>
          <tr v-for="c in s.columns" :key="c.column_id"><td>{{ c.name }}</td><td>{{ c.weight }}</td><td>{{ c.articles }}</td><td>{{ c.avg_reads }}</td>
            <td :style="{ color: c.factor > 1 ? '#3a8f6b' : c.factor < 1 ? '#c0603a' : '' }">×{{ c.factor }}</td></tr>
        </table>
        <p class="muted">全号平均每篇阅读 {{ s.avg_reads }}。</p>
        <p v-if="s.rotation_note"><span class="muted">上次选栏目（{{ fmtTime(s.rotation_at) }}）：</span>{{ s.rotation_note }}</p>
      </div>
    </template>
  </template>
</template>
<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { get, post, put, fmtTime } from '../api.js'
const id = useRoute().params.id
const s = ref(null), err = ref(''), busy = ref(false), msg = ref(null)
const W = 600, H = 160
const bw = computed(() => W / Math.max(1, s.value?.days.length || 1))
const max = computed(() => Math.max(0, ...(s.value?.days || []).map((d) => d.reads)))
const h = (v) => (max.value ? Math.round((v / max.value) * (H - 14)) : 0)
async function load() { s.value = await get(`/api/accounts/${id}/stats`) }
async function refresh() {
  busy.value = true; msg.value = null
  try { const r = await post(`/api/accounts/${id}/stats/refresh`); msg.value = { ok: true, text: r.note }; await load() }
  catch (e) { msg.value = { ok: false, text: e.message } } finally { busy.value = false }
}
async function weighting(on) { try { await put(`/api/accounts/${id}/stats/weighting`, { on }); await load() } catch (e) { err.value = e.message } }
onMounted(() => load().catch((e) => { err.value = e.message }))
</script>
<style scoped>
.chart { width: 100%; max-width: 640px; height: auto; }
.legend span { margin-right: 12px; font-size: 13px; } .legend i { display: inline-block; width: 10px; height: 10px; margin-right: 4px; }
</style>
