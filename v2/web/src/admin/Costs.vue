<template>
  <h1>模型花费</h1>
  <div class="row" style="gap:8px;margin-bottom:12px">
    <span>最近</span>
    <button v-for="d in [1, 7, 30]" :key="d" :class="{ primary: days === d }" @click="days = d; load()">{{ d === 1 ? '今天' : d + ' 天' }}</button>
  </div>
  <p class="muted">花费按每个模型配置填的单价估算（元 / 百万 tokens），没填单价的配置记为 0。单价在“模型配置”里由超级管理员填写，改了单价只影响之后的调用。</p>
  <div v-if="msg" class="msg">{{ msg }}</div>
  <div v-if="r" class="card">
    <b>合计：</b>{{ total.calls }} 次调用，输入 {{ n(total.tokens_in) }} / 输出 {{ n(total.tokens_out) }} tokens，约 ¥{{ total.cost.toFixed(2) }}
  </div>
  <template v-for="t in tables" :key="t.k">
    <div v-if="r" class="card">
      <h2 style="margin-top:0">{{ t.title }}</h2>
      <table>
        <thead><tr><th>{{ t.col }}</th><th>调用次数</th><th>输入 tokens</th><th>输出 tokens</th><th>预估花费</th></tr></thead>
        <tbody>
          <tr v-for="x in r[t.k]" :key="x.key">
            <td>{{ x.label }}<span v-if="t.k === 'by_config' && !x.priced" class="muted">（未填单价）</span></td>
            <td>{{ x.calls }}</td><td>{{ n(x.tokens_in) }}</td><td>{{ n(x.tokens_out) }}</td><td class="nowrap">¥{{ x.cost.toFixed(4) }}</td>
          </tr>
          <tr v-if="!r[t.k].length"><td colspan="5" class="muted">这段时间没有模型调用</td></tr>
        </tbody>
      </table>
    </div>
  </template>
</template>
<script setup>
import { ref, computed, onMounted } from 'vue'
import { get } from '../api.js'
const days = ref(7), r = ref(null), msg = ref('')
const tables = [{ k: 'by_day', title: '按天', col: '日期' }, { k: 'by_config', title: '按模型配置', col: '配置' }, { k: 'by_user', title: '按用户', col: '用户' }]
const n = (x) => Number(x || 0).toLocaleString('zh-CN')
const total = computed(() => (r.value?.by_day || []).reduce((a, x) => ({ calls: a.calls + x.calls, tokens_in: a.tokens_in + x.tokens_in, tokens_out: a.tokens_out + x.tokens_out, cost: a.cost + x.cost }), { calls: 0, tokens_in: 0, tokens_out: 0, cost: 0 }))
async function load() { try { r.value = await get('/api/admin/costs?days=' + days.value) } catch (e) { msg.value = e.message } }
onMounted(load)
</script>
