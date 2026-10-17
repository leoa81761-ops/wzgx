<template>
  <h1>用户与额度</h1>
  <div class="row" style="margin-bottom:12px"><input v-model.trim="q" placeholder="按邮箱或称呼搜索" style="max-width:280px" @keyup.enter="load" /><button @click="load">搜索</button></div>
  <div v-if="msg" class="msg" :class="{ ok }">{{ msg }}</div>
  <table>
    <tr><th>用户</th><th>套餐</th><th>可绑定公众号</th><th>每日额度</th><th>不限额度</th><th>状态</th><th></th></tr>
    <tr v-for="u in list" :key="u.id">
      <td>{{ u.email }}<div class="muted">{{ u.display_name }} · 已绑 {{ u.accounts }} 个 · {{ fmtTime(u.created_at) }}<template v-if="u.tier_expires_at"> · 套餐 {{ fmtTime(u.tier_expires_at).slice(0, 10) }} 到期</template></div></td>
      <td><select v-model="u.tier"><option v-for="t in tiers" :key="t[0]" :value="t[0]">{{ t[1] }}</option></select></td>
      <td style="width:90px"><input type="number" min="0" v-model.number="u.max_accounts" /></td>
      <td style="width:90px"><input type="number" min="0" v-model.number="u.daily_quota" /></td>
      <td><input type="checkbox" v-model="u.quota_exempt" /></td>
      <td>{{ u.status === 'active' ? '正常' : '停用' }}</td>
      <td class="act"><div class="row"><button @click="save(u)">保存</button><button @click="consents(u)">同意记录</button></div></td>
    </tr>
  </table>
  <div v-if="cons" class="card" style="margin-top:14px">
    <div class="row" style="justify-content:space-between"><h2 style="margin:0">{{ cons.email }} 的同意记录</h2><button @click="cons = null">收起</button></div>
    <table>
      <thead><tr><th>文件</th><th>版本</th><th>同意时间</th><th>IP</th><th>设备</th><th>撤回</th></tr></thead>
      <tbody>
        <tr v-for="(c, i) in cons.rows" :key="i">
          <td>{{ docText[c.doc_type] || c.doc_type }}</td><td>{{ c.version }}</td><td class="nowrap">{{ fmtTime(c.agreed_at) }}</td>
          <td>{{ c.ip || '—' }}</td><td style="max-width:280px">{{ c.device || '—' }}</td><td class="nowrap">{{ c.revoked_at ? fmtTime(c.revoked_at) : '—' }}</td>
        </tr>
        <tr v-if="!cons.rows.length"><td colspan="6" class="muted">没有同意记录（可能是从旧系统迁移过来的用户）</td></tr>
      </tbody>
    </table>
  </div>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import { get, put, fmtTime } from '../api.js'
const tiers = [['free', '免费'], ['standard', '标准'], ['premium', '高级'], ['buyout', '买断'], ['custom', '定制']]
const list = ref([]), q = ref(''), msg = ref(''), ok = ref(false), cons = ref(null)
const docText = { terms: '用户协议', privacy: '隐私政策' }
async function consents(u) { try { cons.value = { email: u.email, rows: await get(`/api/admin/users/${u.id}/consents`) } } catch (e) { say(e.message) } }
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
