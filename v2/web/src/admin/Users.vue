<template>
  <h1>用户与额度</h1>
  <div class="row" style="margin-bottom:12px"><input v-model.trim="q" placeholder="按邮箱或称呼搜索" style="max-width:280px" @keyup.enter="load" /><button @click="load">搜索</button></div>
  <div v-if="msg" class="msg" :class="{ ok }">{{ msg }}</div>
  <table>
    <tr><th>用户</th><th>套餐</th><th>可绑定公众号</th><th>每日额度</th><th>不限额度</th><th>状态</th><th></th></tr>
    <tr v-for="u in list" :key="u.id">
      <td>{{ u.email }}<div class="muted">{{ u.display_name }} · 已绑 {{ u.accounts }} 个 · {{ fmtTime(u.created_at) }}</div></td>
      <td><select v-model="u.tier"><option v-for="t in tiers" :key="t[0]" :value="t[0]">{{ t[1] }}</option></select></td>
      <td style="width:90px"><input type="number" min="0" v-model.number="u.max_accounts" /></td>
      <td style="width:90px"><input type="number" min="0" v-model.number="u.daily_quota" /></td>
      <td><input type="checkbox" v-model="u.quota_exempt" /></td>
      <td>{{ u.status === 'active' ? '正常' : '停用' }}</td>
      <td><button @click="save(u)">保存</button></td>
    </tr>
  </table>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import { get, put, fmtTime } from '../api.js'
const tiers = [['free', '免费'], ['standard', '标准'], ['premium', '高级'], ['buyout', '买断'], ['custom', '定制']]
const list = ref([]), q = ref(''), msg = ref(''), ok = ref(false)
const say = (m, good = false) => { msg.value = m; ok.value = good }
async function load() { try { list.value = await get('/api/admin/users?q=' + encodeURIComponent(q.value)) } catch (e) { say(e.message) } }
async function save(u) {
  try {
    await put(`/api/admin/users/${u.id}/allowance`, { max_accounts: u.max_accounts, tier: u.tier, quota_exempt: u.quota_exempt })
    await put(`/api/admin/users/${u.id}/daily-quota`, { daily_quota: u.daily_quota })
    say(`已保存 ${u.email}`, true)
  } catch (e) { say(e.message) }
}
onMounted(load)
</script>
