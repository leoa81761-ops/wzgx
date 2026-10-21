<template>
  <p><router-link to="/admin/accounts">← 公众号</router-link></p>
  <div v-if="msg" class="msg" :class="{ ok }">{{ msg }}</div>
  <template v-if="a">
    <h1>{{ a.name }}</h1>
    <div class="card">
      <div class="two">
        <label class="f"><span>名称</span><input v-model.trim="a.name" /></label>
        <label class="f"><span>主体类型</span><select v-model="a.subject_type"><option value="personal">个人</option><option value="enterprise">企业/组织</option></select></label>
      </div>
      <label class="row" style="margin-bottom:12px"><input type="checkbox" v-model="a.verified" /><span>已认证</span></label>
      <div class="row"><button class="primary" @click="saveAcc">保存</button>
        <button @click="check">接口检查</button> <CheckResult :report="a.check_report" :at="a.checked_at" /></div>
    </div>
    <h2>写作设置</h2>
    <div class="card" v-if="p"><ProfileForm :p="p" @save="saveProfile" /></div>
    <h2>栏目</h2>
    <div class="card"><Columns :base="`/api/admin/accounts/${a.id}`" /></div>
  </template>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { get, put, patch, post } from '../api.js'
import CheckResult from '../views/CheckResult.vue'
import ProfileForm from '../views/ProfileForm.vue'
import Columns from '../views/Columns.vue'
const route = useRoute()
const a = ref(null), p = ref(null), msg = ref(''), ok = ref(false)
const base = () => `/api/admin/accounts/${route.params.id}`
const say = (m, good = false) => { msg.value = m; ok.value = good }
async function load() { a.value = await get(base()); p.value = await get(`${base()}/profile`) }
async function saveAcc() { try { await patch(base(), { name: a.value.name, subject_type: a.value.subject_type, verified: a.value.verified }); say('已保存', true) } catch (e) { say(e.message) } }
async function saveProfile() { try { p.value = await put(`${base()}/profile`, p.value); say('设置已保存', true) } catch (e) { say(e.message) } }
async function check() { try { await post(`${base()}/check`); await load() } catch (e) { say(e.message) } }
onMounted(() => load().catch((e) => say(e.message)))
</script>
