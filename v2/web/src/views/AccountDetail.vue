<template>
  <p><router-link to="/accounts">← 公众号</router-link></p>
  <div v-if="err" class="msg">{{ err }}</div>
  <template v-if="a">
    <h1>{{ a.name }}</h1>
    <div class="card">
      <div class="row"><span class="muted">AppID</span> {{ a.app_id }} <span class="muted">AppSecret</span> {{ a.secret_mask }}</div>
      <div class="row" style="margin-top:8px"><span class="muted">接口检查</span> <CheckResult :report="a.check_report" :at="a.checked_at" />
        <button @click="check" :disabled="busy">重新检查</button></div>
      <p class="muted" v-if="a.check_report?.ok === false">检查用保存的 AppSecret 向微信取一次接口凭证；按上面的提示处理后再检查。</p>
      <details style="margin-top:8px"><summary class="muted">更换 AppSecret</summary>
        <div class="row" style="margin-top:8px"><input v-model.trim="secret" type="password" autocomplete="off" placeholder="新的 AppSecret" style="max-width:320px" />
          <button @click="saveSecret" :disabled="!secret">保存并检查</button></div></details>
    </div>
    <h2>写作设置</h2>
    <div class="card" v-if="p"><ProfileForm :p="p" @save="saveProfile" /></div>
    <h2>栏目</h2>
    <div class="card"><Columns :base="`/api/accounts/${a.id}`" /></div>
    <div v-if="msg" class="msg" :class="{ ok }">{{ msg }}</div>
    <p style="margin-top:24px"><button class="danger" @click="unbind">解绑这个公众号</button>
      <span class="muted"> 解绑后 30 天内不能再绑定同一个 AppID。</span></p>
  </template>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { get, post, put, patch, del } from '../api.js'
import CheckResult from './CheckResult.vue'
import ProfileForm from './ProfileForm.vue'
import Columns from './Columns.vue'
const route = useRoute(), router = useRouter()
const a = ref(null), p = ref(null), err = ref(''), msg = ref(''), ok = ref(false), busy = ref(false), secret = ref('')
const base = () => `/api/accounts/${route.params.id}`
const say = (m, good = false) => { msg.value = m; ok.value = good }
async function load() { a.value = await get(base()); p.value = await get(`${base()}/profile`) }
async function check() { busy.value = true; try { await post(`${base()}/check`); await load() } catch (e) { say(e.message) } finally { busy.value = false } }
async function saveSecret() { try { await patch(base(), { app_secret: secret.value }); secret.value = ''; await check() } catch (e) { say(e.message) } }
async function saveProfile() { try { p.value = await put(`${base()}/profile`, p.value); say('设置已保存', true) } catch (e) { say(e.message) } }
async function unbind() { if (!confirm(`确定解绑「${a.value.name}」？`)) return; try { await del(base()); router.push('/accounts') } catch (e) { say(e.message) } }
onMounted(() => load().catch((e) => (err.value = e.message)))
</script>
