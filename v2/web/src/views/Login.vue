<template>
  <div class="auth card">
    <h1>登录 公号智写</h1>
    <form @submit.prevent="submit">
      <label class="f"><span>邮箱</span><input v-model.trim="email" type="email" autocomplete="username" required /></label>
      <label class="f"><span>密码</span><input v-model="password" type="password" autocomplete="current-password" required /></label>
      <div v-if="err" class="msg">{{ err }}</div>
      <button class="primary" :disabled="busy">登录</button>
      <span class="muted" style="margin-left:12px">还没有账号？<router-link to="/register">注册</router-link></span>
    </form>
  </div>
</template>
<script setup>
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { post } from '../api.js'
import { session, isStaff } from '../store.js'
const email = ref(''), password = ref(''), err = ref(''), busy = ref(false)
const route = useRoute(), router = useRouter()
async function submit() {
  err.value = ''; busy.value = true
  try {
    session.me = await post('/api/auth/login', { email: email.value, password: password.value })
    const next = route.query.next
    router.push(typeof next === 'string' && next.startsWith('/') ? next : isStaff(session.me) ? '/admin' : '/')
  } catch (e) { err.value = e.message } finally { busy.value = false }
}
</script>
