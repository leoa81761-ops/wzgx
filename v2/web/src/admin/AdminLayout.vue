<template>
  <div class="shell">
    <nav class="nav">
      <div class="brand">公号智写 · 管理后台</div>
      <router-link to="/admin/users">用户与额度</router-link>
      <router-link to="/admin/accounts">公众号</router-link>
      <router-link to="/admin/types">文章类型</router-link>
      <router-link to="/admin/configs">模型配置</router-link>
      <router-link to="/admin/routes">环节路由</router-link>
      <router-link v-if="isRoot" to="/admin/operators">运营人员</router-link>
      <router-link to="/admin/audit">操作日志</router-link>
      <div class="foot">{{ session.me?.email }}<br />{{ isRoot ? '超级管理员' : '运营' }}<br /><a href="#" @click.prevent="logout">退出</a></div>
    </nav>
    <main class="main"><router-view /></main>
  </div>
</template>
<script setup>
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { post } from '../api.js'
import { session } from '../store.js'
const isRoot = computed(() => session.me?.role === 'super_admin')
const router = useRouter()
async function logout() { await post('/api/auth/logout').catch(() => {}); session.me = null; router.push('/login') }
</script>
