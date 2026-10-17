<template>
  <div class="auth card">
    <h1>注册</h1>
    <form @submit.prevent="submit">
      <label class="f"><span>邮箱</span><input v-model.trim="f.email" type="email" required /></label>
      <label class="f"><span>称呼（可不填）</span><input v-model.trim="f.name" /></label>
      <label class="f"><span>密码（至少 8 位）</span><input v-model="f.password" type="password" autocomplete="new-password" required /></label>
      <label class="row" style="margin-bottom:12px"><input type="checkbox" v-model="agree" />
        <span>我已阅读并同意《用户协议》和《隐私政策》</span></label>
      <div v-if="err" class="msg">{{ err }}</div>
      <button class="primary" :disabled="busy || !agree">注册</button>
      <span class="muted" style="margin-left:12px">已有账号？<router-link to="/login">登录</router-link></span>
    </form>
  </div>
</template>
<script setup>
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { post } from '../api.js'
import { session } from '../store.js'
const TERMS = '2026-10', PRIVACY = '2026-10'
const f = reactive({ email: '', name: '', password: '' })
const agree = ref(false), err = ref(''), busy = ref(false)
const router = useRouter()
async function submit() {
  err.value = ''; busy.value = true
  try {
    await post('/api/auth/register', { ...f, terms_version: TERMS, privacy_version: PRIVACY })
    session.me = await post('/api/auth/login', { email: f.email, password: f.password })
    router.push('/')
  } catch (e) { err.value = e.message } finally { busy.value = false }
}
</script>
