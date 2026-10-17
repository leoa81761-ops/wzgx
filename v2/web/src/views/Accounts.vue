<template>
  <h1>公众号</h1>
  <div v-if="err" class="msg">{{ err }}</div>
  <table v-if="list.length">
    <tr><th>名称</th><th>AppID</th><th>接口检查</th><th></th></tr>
    <tr v-for="a in list" :key="a.id">
      <td>{{ a.name }}</td><td class="muted">{{ a.app_id }}</td>
      <td><CheckResult :report="a.check_report" :at="a.checked_at" /></td>
      <td><router-link :to="`/accounts/${a.id}`">设置</router-link></td>
    </tr>
  </table>
  <p v-else class="muted">还没有绑定公众号。</p>
  <div class="card" style="margin-top:16px">
    <h2 style="margin-top:0">绑定公众号</h2>
    <p class="muted">在公众号后台「设置与开发 → 基本配置」里找到 AppID 和 AppSecret。AppSecret 只保存加密后的内容，之后只显示前后几位。</p>
    <form @submit.prevent="bind">
      <div class="two">
        <label class="f"><span>公众号名称</span><input v-model.trim="f.name" required /></label>
        <label class="f"><span>主体类型</span><select v-model="f.subject_type"><option value="personal">个人</option><option value="enterprise">企业/组织</option></select></label>
        <label class="f"><span>AppID</span><input v-model.trim="f.app_id" placeholder="wx 开头的 18 位" required /></label>
        <label class="f"><span>AppSecret</span><input v-model.trim="f.app_secret" type="password" autocomplete="off" required /></label>
      </div>
      <label class="row" style="margin-bottom:12px"><input type="checkbox" v-model="f.verified" /><span>已认证</span></label>
      <button class="primary" :disabled="busy">绑定</button>
    </form>
  </div>
</template>
<script setup>
import { ref, reactive, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { get, post } from '../api.js'
import CheckResult from './CheckResult.vue'
const list = ref([]), err = ref(''), busy = ref(false)
const f = reactive({ name: '', app_id: '', app_secret: '', subject_type: 'personal', verified: false })
const router = useRouter()
async function load() { try { list.value = await get('/api/accounts') } catch (e) { err.value = e.message } }
async function bind() {
  err.value = ''; busy.value = true
  try {
    const a = await post('/api/accounts', f)
    await post(`/api/accounts/${a.id}/check`).catch(() => {})
    router.push(`/accounts/${a.id}`)
  } catch (e) { err.value = e.message } finally { busy.value = false }
}
onMounted(load)
</script>
