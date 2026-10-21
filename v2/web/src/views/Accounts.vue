<template>
  <h1>公众号</h1>
  <div v-if="err" class="msg">{{ err }}</div>
  <table v-if="list.length">
    <tr><th>名称</th><th>AppID</th><th>接口检查</th><th class="act"></th></tr>
    <tr v-for="a in list" :key="a.id">
      <td>{{ a.name }}</td><td class="muted">{{ a.app_id }}</td>
      <td><CheckResult :report="a.check_report" :at="a.checked_at" /></td>
      <td class="act"><router-link :to="`/accounts/${a.id}`">设置</router-link></td>
    </tr>
  </table>
  <p v-else-if="loaded" class="muted">还没有绑定公众号。</p>
  <div class="card" style="margin-top:16px">
    <h2 style="margin-top:0">绑定公众号</h2>
    <div v-if="loaded && full" class="hint">你的账号最多可以绑定 {{ maxAccounts }} 个公众号，已经绑满了。要再绑定，请先解绑一个，或联系客服提高数量。</div>
    <template v-else>
      <p class="muted">在公众号后台「设置与开发 → 基本配置」里找到 AppID 和 AppSecret。AppSecret 只保存加密后的内容，之后只显示前后几位。<span v-if="maxAccounts">（可绑定 {{ maxAccounts }} 个，已绑 {{ list.length }} 个）</span></p>
      <p v-if="serverIP" class="hint">还要在同一页的「IP 白名单」里加上本平台的服务器 IP：<b class="mono">{{ serverIP }}</b>，否则微信会拒绝调用。</p>
      <p v-else class="muted">还要在同一页的「IP 白名单」里加上本平台的服务器 IP（请联系客服获取）。</p>
      <form @submit.prevent="bind">
        <div class="two">
          <label class="f"><span>公众号名称</span><input v-model.trim="f.name" required /></label>
          <label class="f"><span>主体类型</span><select v-model="f.subject_type"><option value="personal">个人</option><option value="enterprise">企业/组织</option></select></label>
          <label class="f"><span>AppID（wx 开头，共 18 位）</span><input v-model.trim="f.app_id" placeholder="wx 开头的 18 位" maxlength="18" required /></label>
          <label class="f"><span>AppSecret（32 位字母和数字）<span class="muted" v-if="f.app_secret"> · 已输入 {{ f.app_secret.length }} 位</span></span>
            <div class="pw"><input v-model.trim="f.app_secret" :type="showSecret ? 'text' : 'password'" autocomplete="off" maxlength="64" required />
              <button type="button" class="eye" @click="showSecret = !showSecret">{{ showSecret ? '隐藏' : '显示' }}</button></div></label>
        </div>
        <label class="row" style="margin-bottom:12px"><input type="checkbox" v-model="f.verified" /><span>已认证</span></label>
        <div v-if="bindErr" class="msg">{{ bindErr }}</div>
        <button class="primary" :disabled="busy">{{ busy ? '正在绑定…' : '绑定' }}</button>
      </form>
    </template>
  </div>
</template>
<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { get, post } from '../api.js'
import { session, loadMe } from '../store.js'
import CheckResult from './CheckResult.vue'
const list = ref([]), err = ref(''), bindErr = ref(''), busy = ref(false), serverIP = ref(''), loaded = ref(false), showSecret = ref(false)
const f = reactive({ name: '', app_id: '', app_secret: '', subject_type: 'personal', verified: false })
const maxAccounts = computed(() => session.me?.max_accounts ?? 0)
const full = computed(() => list.value.length >= maxAccounts.value)
const router = useRouter()
async function load() { try { list.value = await get('/api/accounts') } catch (e) { err.value = e.message } finally { loaded.value = true } }
async function bind() {
  bindErr.value = ''; busy.value = true
  try {
    const a = await post('/api/accounts', f)
    await post(`/api/accounts/${a.id}/check`).catch(() => {})
    router.push(`/accounts/${a.id}`)
  } catch (e) { bindErr.value = e.message } finally { busy.value = false }
}
onMounted(() => { loadMe().catch(() => {}); load(); get('/api/site').then((s) => (serverIP.value = s.server_ip || '')).catch(() => {}) })
</script>
