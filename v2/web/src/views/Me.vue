<template>
  <h1>我的</h1>
  <div class="card">
    <h2 style="margin-top:0">Tavily 检索密钥（可选，不填也能正常写作）</h2>
    <p class="muted">Tavily 是一个联网搜索服务。填上你自己的 Tavily 密钥后，写作前除了平台自带的检索，还会再用它多搜一路资料，事实和出处会更充实；搜索费用记在你的 Tavily 账户上。
      没有的话可以到 tavily.com 免费注册获取，以 tvly- 开头。密钥只保存加密后的内容，这里只显示前后几位。</p>
    <p v-if="tv.has_key">当前：<span class="mono">{{ tv.mask }}</span></p>
    <p v-else class="muted">当前：未填写</p>
    <div class="row">
      <div style="flex:1;max-width:360px"><PwInput v-model="tavilyKey" trim placeholder="tvly-..." /></div>
      <button @click="saveKey" :disabled="!tavilyKey">{{ tv.has_key ? '替换' : '保存' }}</button>
    </div>
    <div v-if="keyMsg" class="msg" :class="{ ok: keyMsg.ok }">{{ keyMsg.text }}</div>
  </div>
  <div class="card">
    <h2 style="margin-top:0">修改密码</h2>
    <form @submit.prevent="changePw" style="max-width:360px">
      <label class="f"><span>当前密码</span><PwInput v-model="pw.old" autocomplete="current-password" required /></label>
      <label class="f"><span>新密码（至少 8 位）</span><PwInput v-model="pw.new" autocomplete="new-password" required /></label>
      <div v-if="pwMsg" class="msg">{{ pwMsg }}</div>
      <button :disabled="!pw.old || pw.new.length < 8">修改密码</button> <span class="muted">改完需要重新登录</span>
    </form>
  </div>
  <button class="danger" @click="logout">退出登录</button>
</template>
<script setup>
import { ref, reactive, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { get, put, post } from '../api.js'
import { session } from '../store.js'
import PwInput from './PwInput.vue'
const tv = ref({}), tavilyKey = ref(''), pw = reactive({ old: '', new: '' }), keyMsg = ref(null), pwMsg = ref('')
const router = useRouter()
async function saveKey() {
  try { await put('/api/me/tavily', { key: tavilyKey.value }); tavilyKey.value = ''; tv.value = await get('/api/me/tavily'); keyMsg.value = { ok: true, text: '已保存' } }
  catch (e) { keyMsg.value = { ok: false, text: e.message } }
}
async function changePw() {
  pwMsg.value = ''
  try { await post('/api/auth/password', { old_password: pw.old, new_password: pw.new }); session.me = null; router.push('/login') } catch (e) { pwMsg.value = e.message }
}
async function logout() { await post('/api/auth/logout').catch(() => {}); session.me = null; router.push('/login') }
onMounted(async () => { try { tv.value = await get('/api/me/tavily') } catch (e) { keyMsg.value = { ok: false, text: e.message } } })
</script>
