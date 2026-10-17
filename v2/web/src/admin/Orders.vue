<template>
  <h1>订单与开通</h1>
  <p class="muted">还没有在线支付。用户线下付款后，在这里手动给他开通套餐；每次开通都会记一笔订单并写入操作日志。到期后不会自动降级，请到期前处理。</p>
  <div class="card" style="max-width:640px">
    <h2 style="margin-top:0">手动开通</h2>
    <div class="form">
      <label>用户邮箱<input v-model.trim="f.email" placeholder="user@example.com" @change="findUser" /></label>
      <div v-if="user" class="muted">当前：{{ tierText[user.tier] || user.tier }}，可绑 {{ user.max_accounts }} 个公众号，每天 {{ user.daily_quota }} 篇{{ user.tier_expires_at ? '，' + fmtTime(user.tier_expires_at).slice(0, 10) + ' 到期' : '' }}</div>
      <label>套餐
        <select v-model="f.tier"><option v-for="(v, k) in tierText" :key="k" :value="k">{{ v }}</option></select>
      </label>
      <label>可绑公众号数量<input type="number" min="0" max="100" v-model.number="f.max_accounts" /></label>
      <label>每天可写篇数<input type="number" min="0" max="1000" v-model.number="f.daily_quota" /></label>
      <label>时长（月，留空表示长期）<input type="number" min="1" max="120" v-model.number="f.months" /></label>
      <label>金额（元，仅记录）<input type="number" min="0" step="0.01" v-model.number="f.amount" /></label>
      <label>备注<input v-model="f.note" maxlength="200" placeholder="比如：微信转账 10 月 17 日" /></label>
    </div>
    <div v-if="msg" class="msg" :class="{ ok: msg.ok }">{{ msg.text }}</div>
    <button class="primary" style="margin-top:10px" :disabled="busy || !user" @click="activate">开通</button>
  </div>
  <div class="card">
    <h2 style="margin-top:0">订单记录</h2>
    <table>
      <thead><tr><th>编号</th><th>用户</th><th>套餐</th><th>公众号 / 每天篇数</th><th>时长</th><th>到期</th><th>金额</th><th>备注</th><th>操作人</th><th>时间</th></tr></thead>
      <tbody>
        <tr v-for="o in orders" :key="o.id">
          <td>{{ o.id }}</td><td>{{ o.user_email }}</td><td class="nowrap">{{ tierText[o.tier] || o.tier }}</td>
          <td>{{ o.max_accounts }} / {{ o.daily_quota }}</td><td class="nowrap">{{ o.months ? o.months + ' 个月' : '长期' }}</td>
          <td class="nowrap">{{ o.expires_at ? fmtTime(o.expires_at).slice(0, 10) : '—' }}</td>
          <td class="nowrap">{{ o.amount != null ? '¥' + o.amount.toFixed(2) : '—' }}</td><td>{{ o.note }}</td><td>{{ o.created_by }}</td><td class="nowrap">{{ fmtTime(o.created_at) }}</td>
        </tr>
        <tr v-if="!orders.length"><td colspan="10" class="muted">还没有订单</td></tr>
      </tbody>
    </table>
  </div>
</template>
<script setup>
import { reactive, ref, onMounted } from 'vue'
import { get, post, fmtTime } from '../api.js'
const tierText = { free: '免费', standard: '标准版', premium: '高级版', buyout: '买断', custom: '定制' }
const f = reactive({ email: '', tier: 'standard', max_accounts: 1, daily_quota: 1, months: 1, amount: null, note: '' })
const user = ref(null), orders = ref([]), msg = ref(null), busy = ref(false)
let users = []
async function findUser() {
  if (!f.email) { user.value = null; msg.value = null; return }
  users = await get('/api/admin/users?q=' + encodeURIComponent(f.email)).catch(() => [])
  user.value = users.find((u) => u.email.toLowerCase() === f.email.toLowerCase() ) || null
  msg.value = f.email && !user.value ? { ok: false, text: '没有找到这个用户（只能给普通用户开通）' } : null
  if (user.value) Object.assign(f, { max_accounts: user.value.max_accounts, daily_quota: user.value.daily_quota })
}
async function activate() {
  busy.value = true
  try {
    await post('/api/admin/orders', { user_id: user.value.id, tier: f.tier, max_accounts: f.max_accounts || 0, daily_quota: f.daily_quota || 0,
      months: f.months || null, amount: f.amount === '' || f.amount == null ? null : f.amount, note: f.note })
    msg.value = { ok: true, text: `已给 ${user.value.email} 开通${tierText[f.tier]}` }
    const done = msg.value; await findUser(); msg.value = done
    orders.value = await get('/api/admin/orders')
  } catch (e) { msg.value = { ok: false, text: e.message } } finally { busy.value = false }
}
onMounted(async () => { orders.value = await get('/api/admin/orders').catch(() => []) })
</script>
