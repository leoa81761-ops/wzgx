<template>
  <h1>运营人员</h1>
  <div v-if="msg" class="msg" :class="{ ok }">{{ msg }}</div>
  <table>
    <tr><th>邮箱</th><th>称呼</th><th>角色</th><th>状态</th><th></th></tr>
    <tr v-for="u in list" :key="u.id">
      <td>{{ u.email }}</td><td>{{ u.display_name }}</td><td>{{ u.role === 'super_admin' ? '超级管理员' : '运营' }}</td>
      <td>{{ u.status === 'active' ? '正常' : '停用' }}</td>
      <td><button v-if="u.role === 'operator'" @click="toggle(u)">{{ u.status === 'active' ? '停用' : '启用' }}</button></td>
    </tr>
  </table>
  <div class="card" style="margin-top:16px;max-width:420px">
    <h2 style="margin-top:0">添加运营</h2>
    <label class="f"><span>邮箱</span><input v-model.trim="f.email" type="email" /></label>
    <label class="f"><span>称呼</span><input v-model.trim="f.name" /></label>
    <label class="f"><span>初始密码（至少 8 位）</span><input v-model="f.password" type="password" autocomplete="new-password" /></label>
    <button class="primary" @click="add">添加</button>
  </div>
</template>
<script setup>
import { ref, reactive, onMounted } from 'vue'
import { get, post } from '../api.js'
const list = ref([]), f = reactive({ email: '', name: '', password: '' }), msg = ref(''), ok = ref(false)
const say = (m, good = false) => { msg.value = m; ok.value = good }
async function load() { list.value = await get('/api/admin/operators') }
async function add() { try { await post('/api/admin/operators', f); f.email = f.name = f.password = ''; await load(); say('已添加', true) } catch (e) { say(e.message) } }
async function toggle(u) { try { await post(`/api/admin/operators/${u.id}/status`, { active: u.status !== 'active' }); await load() } catch (e) { say(e.message) } }
onMounted(() => load().catch((e) => say(e.message)))
</script>
