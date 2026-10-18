<template>
  <div class="shell">
    <nav class="nav">
      <div class="brand">公号智写 · 管理后台</div>
      <router-link to="/admin/users">用户与额度</router-link>
      <router-link to="/admin/accounts">公众号</router-link>
      <router-link to="/admin/orders">订单与开通</router-link>
      <router-link to="/admin/tiers">套餐设置</router-link>
      <router-link to="/admin/complaints">投诉处理</router-link>
      <router-link to="/admin/jobs">任务监控</router-link>
      <router-link to="/admin/costs">模型花费</router-link>
      <router-link to="/admin/types">文章类型</router-link>
      <router-link to="/admin/configs">模型配置</router-link>
      <router-link to="/admin/routes">环节路由</router-link>
      <router-link v-if="isRoot" to="/admin/operators">运营人员</router-link>
      <router-link to="/admin/feedback">文章反馈</router-link>
      <router-link to="/admin/sensitive">敏感词</router-link>
      <router-link to="/admin/settings">平台设置</router-link>
      <router-link to="/admin/audit">操作日志</router-link>
      <div class="foot">{{ session.me?.email }}<br />{{ isRoot ? '超级管理员' : '运营' }}<br /><a href="#" @click.prevent="logout">退出</a></div>
    </nav>
    <main class="main"><div v-if="route.query.denied" class="msg">只有超级管理员可以打开那个页面。</div>
      <div v-if="budget?.any_hit" class="msg">
        <template v-if="budget.platform_hit">今天全平台模型花费 ¥{{ budget.platform_spent.toFixed(2) }}，已到上限 ¥{{ budget.platform_cap }}，新任务暂停，排队等待。</template>
        <template v-else>以下模型配置今天已到花费上限，正在改用其他配置：{{ budget.configs.filter((c) => c.hit).map((c) => c.name).join('、') }}。</template>
        <template v-if="budget.waiting_jobs"> 有 {{ budget.waiting_jobs }} 个任务因为没有可用的模型在排队。</template>
        可在“平台设置”或“模型配置”里调整上限。
      </div><router-view /></main>
  </div>
</template>
<script setup>
import { computed, ref, watch, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { get, post } from '../api.js'
import { session } from '../store.js'
const isRoot = computed(() => session.me?.role === 'super_admin')
const router = useRouter(), route = useRoute()
const budget = ref(null)
const loadBudget = () => get('/api/admin/budget').then((b) => { budget.value = b }).catch(() => {})
loadBudget()
watch(() => route.path, loadBudget)
const timer = setInterval(loadBudget, 60000)
onUnmounted(() => clearInterval(timer))
async function logout() { await post('/api/auth/logout').catch(() => {}); session.me = null; router.push('/login') }
</script>
