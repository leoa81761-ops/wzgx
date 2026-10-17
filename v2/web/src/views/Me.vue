<template>
  <h1>我的</h1>
  <div class="card">
    <h2 style="margin-top:0">Tavily 检索密钥（可选）</h2>
    <p class="muted">填了以后写作时会多查一路资料。密钥只保存加密后的内容，这里只显示前后几位。</p>
    <p v-if="tv.has_key">当前：{{ tv.mask }}</p>
    <div class="row"><input v-model.trim="key" type="password" autocomplete="off" placeholder="tvly-..." style="max-width:320px" />
      <button @click="saveKey" :disabled="!key">保存</button></div>
  </div>
  <div class="card">
    <h2 style="margin-top:0">修改密码</h2>
    <form @submit.prevent="changePw" style="max-width:320px">
      <label class="f"><span>当前密码</span><input v-model="pw.old" type="password" autocomplete="current-password" required /></label>
      <label class="f"><span>新密码（至少 8 位）</span><input v-model="pw.new" type="password" autocomplete="new-password" required /></label>
      <button>修改密码</button> <span class="muted">改完需要重新登录</span>
    </form>
  </div>
  <div v-if="msg" class="msg" :class="{ ok }">{{ msg }}</div>
  <button class="danger" @click="logout">退出登录</button>
</template>
<script setup>
import { ref, reactive, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { get, put, post } from '../api.js'
import { session } from '../store.js'
const tv = ref({}), key = ref(''), pw = reactive({ old: '', new: '' }), msg = ref(''), ok = ref(false)
const router = useRouter()
const say = (m, good = false) => { msg.value = m; ok.value = good }
async function saveKey() { try { await put('/api/me/tavily', { key: key.value }); key.value = ''; tv.value = await get('/api/me/tavily'); say('已保存', true) } catch (e) { say(e.message) } }
async function changePw() {
  try { await post('/api/auth/password', { old_password: pw.old, new_password: pw.new }); session.me = null; router.push('/login') } catch (e) { say(e.message) }
}
async function logout() { await post('/api/auth/logout').catch(() => {}); session.me = null; router.push('/login') }
onMounted(async () => { try { tv.value = await get('/api/me/tavily') } catch (e) { say(e.message) } })
</script>
