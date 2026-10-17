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
    <h2>写作控制</h2>
    <div class="card">
      <label class="row"><input type="checkbox" :checked="a.paused" @change="control({ paused: $event.target.checked })" />
        <span>暂停这个公众号<span class="muted"> · 暂停后写作计划不运行，「现在写一篇」仍可用</span></span></label>
      <p v-if="a.paused && a.paused_reason === 'auto'" class="hint" style="margin:8px 0 0">系统自动暂停的：最近推送到草稿箱的几篇都没有改动、也没有评价。取消勾选即可恢复。</p>
      <label class="row" style="margin-top:10px"><input type="checkbox" :checked="a.confirm_topic" @change="control({ confirm_topic: $event.target.checked })" />
        <span>选题先给我确认<span class="muted"> · 每篇选好题目后先停下，等你点「就写这个」「换一个」或自己改题目（在首页和写作计划页）</span></span></label>
      <div v-if="ctl" class="msg" :class="{ ok: ctl.ok }">{{ ctl.text }}</div>
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
const a = ref(null), p = ref(null), err = ref(''), msg = ref(''), ok = ref(false), busy = ref(false), secret = ref(''), ctl = ref(null)
const base = () => `/api/accounts/${route.params.id}`
const say = (m, good = false) => { msg.value = m; ok.value = good }
async function load() { a.value = await get(base()); p.value = await get(`${base()}/profile`) }
async function check() { busy.value = true; try { await post(`${base()}/check`); await load() } catch (e) { say(e.message) } finally { busy.value = false } }
async function saveSecret() { try { await patch(base(), { app_secret: secret.value }); secret.value = ''; await check() } catch (e) { say(e.message) } }
async function saveProfile() { try { p.value = await put(`${base()}/profile`, p.value); say('设置已保存', true) } catch (e) { say(e.message) } }
async function control(c) {
  try { await put(`${base()}/controls`, c); await load(); ctl.value = { ok: true, text: c.paused !== undefined ? (c.paused ? '已暂停' : '已恢复') : (c.confirm_topic ? '已开启选题确认' : '已关闭选题确认') } }
  catch (e) { ctl.value = { ok: false, text: e.message } }
}
async function unbind() { if (!confirm(`确定解绑「${a.value.name}」？`)) return; try { await del(base()); router.push('/accounts') } catch (e) { say(e.message) } }
onMounted(() => load().catch((e) => (err.value = e.message)))
</script>
