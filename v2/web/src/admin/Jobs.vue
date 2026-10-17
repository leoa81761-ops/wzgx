<template>
  <h1>任务监控</h1>
  <div class="row" style="gap:8px;flex-wrap:wrap;margin-bottom:12px">
    <button v-for="t in tabs" :key="t.k" :class="{ primary: tab === t.k }" @click="tab = t.k; load()">
      {{ t.label }}<span v-if="counts[t.k] != null"> {{ counts[t.k] }}</span>
    </button>
    <button @click="load()">刷新</button>
  </div>
  <div v-if="msg" class="msg" :class="{ ok: msg.ok }">{{ msg.text }}</div>
  <div class="card">
    <table>
      <thead><tr><th>编号</th><th>状态</th><th>用户 / 公众号</th><th>题目</th><th>来源</th><th>模型调用</th><th>预估花费</th><th>时间</th><th>原因</th><th></th></tr></thead>
      <tbody>
        <tr v-for="j in jobs" :key="j.id">
          <td>{{ j.id }}</td>
          <td class="nowrap">{{ jobText[j.status] || j.status }}</td>
          <td>{{ j.user_email }}<br /><span class="muted">{{ j.account_name }}<template v-if="j.column_name"> · {{ j.column_name }}</template></span></td>
          <td>{{ j.topic || '—' }}</td>
          <td class="nowrap">{{ j.source === 'plan' ? '计划' : '手动' }}</td>
          <td>{{ j.calls }}</td>
          <td class="nowrap">¥{{ j.cost.toFixed(4) }}</td>
          <td class="nowrap">{{ fmtTime(j.finished_at || j.run_at) }}</td>
          <td style="max-width:240px">{{ j.failure }}</td>
          <td class="act"><div class="row">
            <button @click="open(j.id)">详情</button>
            <button v-if="j.status === 'failed' || j.status === 'cancelled'" @click="act(j.id, 'retry')">重试</button>
            <button v-if="['queued', 'awaiting_topic', 'running'].includes(j.status)" class="danger" @click="act(j.id, 'cancel')">取消</button>
          </div></td>
        </tr>
        <tr v-if="!jobs.length"><td colspan="10" class="muted">没有任务</td></tr>
      </tbody>
    </table>
  </div>
  <div v-if="detail" class="card">
    <div class="row" style="justify-content:space-between"><h2 style="margin:0">任务 {{ detail.job.id }} · {{ jobText[detail.job.status] }}</h2><button @click="detail = null">收起</button></div>
    <p v-if="detail.job.article_id" class="muted">生成的文章编号：{{ detail.job.article_id }}</p>
    <h3>环节记录</h3>
    <ol class="trace">
      <li v-for="(s, i) in detail.job.trace" :key="i">
        <b>{{ stageText[s.stage] || s.stage }}</b>
        <span v-if="s.note"> · {{ s.note }}</span>
        <span v-for="(a, k) in s.attempts || []" :key="k" class="muted"> · 第{{ a.n }}次 {{ a.model }} {{ outcomeText(a.outcome) }} {{ a.total_ms }}ms</span>
      </li>
      <li v-if="!(detail.job.trace || []).length" class="muted">还没有记录</li>
    </ol>
    <h3>模型调用</h3>
    <table>
      <thead><tr><th>环节</th><th>配置</th><th>模型</th><th>第几次</th><th>结果</th><th>首字 / 总耗时</th><th>输入 / 输出 tokens</th><th>花费</th><th>错误</th></tr></thead>
      <tbody>
        <tr v-for="(c, i) in detail.calls" :key="i">
          <td class="nowrap">{{ stageText[c.stage] || c.stage }}</td><td>{{ c.config_name }}</td><td>{{ c.model }}</td><td>{{ c.attempt }}</td>
          <td class="nowrap">{{ outcomeText(c.outcome) }}</td>
          <td class="nowrap">{{ c.first_byte_ms ?? '—' }} / {{ c.total_ms }} ms</td>
          <td class="nowrap">{{ c.tokens_in }} / {{ c.tokens_out }}</td>
          <td class="nowrap">¥{{ c.cost.toFixed(4) }}</td>
          <td style="max-width:260px">{{ c.error }}</td>
        </tr>
        <tr v-if="!detail.calls.length"><td colspan="9" class="muted">没有模型调用</td></tr>
      </tbody>
    </table>
  </div>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import { get, post, fmtTime, jobText } from '../api.js'
const tabs = [
  { k: '', label: '进行中与失败' }, { k: 'running', label: '写作中' }, { k: 'queued', label: '排队中' },
  { k: 'awaiting_topic', label: '等确认选题' }, { k: 'failed', label: '失败' }, { k: 'cancelled', label: '已取消' }, { k: 'done', label: '完成' },
]
const stageText = { topic: '选题', research: '查资料', scan: '找新闻', write: '写作', review: '自查', image: '配图', publish: '推送' }
const outcomes = { ok: '成功', empty: '空回复', timeout_first_byte: '首字超时', timeout_idle: '中途卡住', timeout_total: '总时长超时', error: '出错' }
const outcomeText = (o) => outcomes[o] || (o?.startsWith('http_') ? '接口返回 ' + o.slice(5) : o)
const tab = ref(''), jobs = ref([]), counts = ref({}), detail = ref(null), msg = ref(null)
async function load() {
  try { const r = await get('/api/admin/jobs' + (tab.value ? '?status=' + tab.value : '')); jobs.value = r.jobs; counts.value = r.counts }
  catch (e) { msg.value = { ok: false, text: e.message } }
}
async function open(id) { try { detail.value = await get('/api/admin/jobs/' + id) } catch (e) { msg.value = { ok: false, text: e.message } } }
async function act(id, a) {
  if (a === 'cancel' && !confirm('确定取消这个任务？')) return
  try { await post(`/api/admin/jobs/${id}/${a}`); msg.value = { ok: true, text: a === 'retry' ? `任务 ${id} 已重新排队` : `任务 ${id} 已取消` }; load(); if (detail.value?.job.id === id) open(id) }
  catch (e) { msg.value = { ok: false, text: e.message } }
}
onMounted(load)
</script>
