<template>
  <h1>公众号</h1>
  <p class="muted">管理员不绑定公众号；这里查看用户绑定的公众号并调整参数。</p>
  <div v-if="err" class="msg">{{ err }}</div>
  <table>
    <tr><th class="nowrap">名称</th><th class="nowrap">AppID</th><th class="nowrap">所属用户</th><th class="nowrap">状态</th><th class="nowrap">接口检查</th><th class="act"></th></tr>
    <tr v-for="a in list" :key="a.id">
      <td>{{ a.name }}</td><td class="muted">{{ a.app_id }}</td><td class="muted">{{ a.owner_email || `用户 ${a.user_id}` }}</td>
      <td class="nowrap">{{ a.bind_status === 'bound' ? (a.paused ? (a.paused_reason === 'auto' ? '自动暂停' : '已暂停') : '已绑定') : '已解绑' }}<span v-if="a.confirm_topic" class="muted"> · 选题确认</span></td>
      <td><CheckResult :report="a.check_report" :at="a.checked_at" /></td>
      <td class="act"><router-link :to="`/admin/accounts/${a.id}`">编辑参数</router-link></td>
    </tr>
  </table>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import { get } from '../api.js'
import CheckResult from '../views/CheckResult.vue'
const list = ref([]), err = ref('')
onMounted(async () => { try { list.value = await get('/api/admin/accounts') } catch (e) { err.value = e.message } })
</script>
