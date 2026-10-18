<template>
  <div class="auth card">
    <h1>注册</h1>
    <form @submit.prevent="submit">
      <label class="f"><span>邮箱</span><input v-model.trim="f.email" type="email" required /></label>
      <label class="f"><span>称呼（可不填）</span><input v-model.trim="f.name" /></label>
      <label class="f"><span>密码（至少 8 位）</span><PwInput v-model="f.password" autocomplete="new-password" required /></label>
      <label class="row" style="margin-bottom:12px"><input type="checkbox" v-model="agree" />
        <span>我已阅读并同意<router-link to="/terms" target="_blank">《用户协议》</router-link>和<router-link to="/privacy" target="_blank">《隐私政策》</router-link></span></label>
      <div v-if="err" class="msg">{{ err }}</div>
      <p v-if="invite" class="muted" style="margin:0 0 8px">邀请码：{{ invite }}</p>
      <p v-if="!agree" class="muted" style="margin:0 0 8px">请先勾选上面的同意框，才能注册。</p>
      <button class="primary" :disabled="busy || !agree">注册</button>
      <span class="muted" style="margin-left:12px">已有账号？<router-link to="/login">登录</router-link></span>
    </form>
  </div>
</template>
<script setup>
import { reactive, ref } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { post } from '../api.js'
import { session, deviceId } from '../store.js'
import PwInput from './PwInput.vue'
const TERMS = '2026-10', PRIVACY = '2026-10'
const f = reactive({ email: '', name: '', password: '' })
const agree = ref(false), err = ref(''), busy = ref(false)
const router = useRouter(), route = useRoute()
const invite = ref(typeof route.query.invite === 'string' ? route.query.invite : '')
if (invite.value) localStorage.setItem('gh_invite', invite.value); else invite.value = localStorage.getItem('gh_invite') || ''
async function submit() {
  err.value = ''; busy.value = true
  try {
    await post('/api/auth/register', { ...f, terms_version: TERMS, privacy_version: PRIVACY, invite: invite.value, device_id: deviceId() })
    session.me = await post('/api/auth/login', { email: f.email, password: f.password })
    localStorage.removeItem('gh_invite')
    router.push('/')
  } catch (e) { err.value = e.message } finally { busy.value = false }
}
</script>
