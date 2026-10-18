<template>
  <div class="auth card">
    <h1>登录 公号智写</h1>
    <form @submit.prevent="submit">
      <label class="f"><span>邮箱</span><input v-model.trim="email" type="email" autocomplete="username" required /></label>
      <label class="f"><span>密码</span><input v-model="password" type="password" autocomplete="current-password" required /></label>
      <div v-if="err" class="msg">{{ err }}</div>
      <button class="primary" :disabled="busy">登录</button>
      <span class="muted" style="margin-left:12px">还没有账号？<router-link to="/register">注册</router-link></span><span class="muted" style="margin-left:12px"><router-link to="/welcome">产品介绍</router-link> · <router-link to="/help">帮助</router-link></span>
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
    if (session.me.deletion_cancelled) session.notice = '你之前申请了注销账号，这次登录已经取消注销，账号和数据都还在。'
    const next = route.query.next
    router.push(typeof next === 'string' && next.startsWith('/') ? next : isStaff(session.me) ? '/admin' : '/')
  } catch (e) { err.value = e.message } finally { busy.value = false }
}
</script>
