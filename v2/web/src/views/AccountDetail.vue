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
      <div style="margin-top:12px;border-top:1px solid var(--line);padding-top:10px">
        <label class="row"><input type="checkbox" :checked="a.auto_publish" :disabled="!a.verified && !a.auto_publish" @change="control({ auto_publish: $event.target.checked })" />
          <span>推送到草稿箱后自动发表<span class="muted"> · 默认关闭</span></span></label>
        <p v-if="!a.verified" class="muted" style="margin:6px 0 0">只有已认证的公众号可以开启。认证状态由管理员核实后标记。</p>
        <template v-if="a.auto_publish">
          <p class="hint" style="margin:8px 0">推送到草稿箱后先等一段检查时间，这期间你可以在「文章」页取消；时间到了系统会先确认草稿还在，再通过微信「发表」接口发出去，同一篇只发一次。</p>
          <div class="row" style="gap:8px;flex-wrap:wrap">
            <span>检查时间</span>
            <select :value="a.review_minutes" @change="control({ review_minutes: Number($event.target.value) })" style="width:auto">
              <option v-for="m in reviewOpts" :key="m[0]" :value="m[0]">{{ m[1] }}</option>
              <option v-if="!reviewOpts.some((m) => m[0] === a.review_minutes)" :value="a.review_minutes">{{ a.review_minutes }} 分钟</option>
            </select>
            <span style="margin-left:10px">每天最多自动发表</span>
            <select :value="a.publish_cap" @change="control({ publish_cap: Number($event.target.value) })" style="width:auto">
              <option v-for="n in 5" :key="n" :value="n">{{ n }} 篇</option>
            </select>
          </div>
        </template>
      </div>
      <div v-if="ctl" class="msg" :class="{ ok: ctl.ok }">{{ ctl.text }}</div>
    </div>
    <h2>排版</h2>
    <div class="card" v-if="lay">
      <p class="muted" style="margin-top:0">只改文章的样子（颜色、小标题、间距），不改文字。换了之后，还没推送到草稿箱的文章会马上重新排版，已推送的不动。表格和配图仍由系统决定。</p>
      <div class="layouts">
        <label v-for="l in lay.layouts" :key="l.key" class="layout-opt" :class="{ on: lay.layout === l.key }">
          <input type="radio" name="layout" :value="l.key" :checked="lay.layout === l.key" @change="setLayout(l.key)" />
          <b>{{ l.name }}</b><span class="muted">{{ l.note }}</span>
        </label>
      </div>
      <div v-if="layMsg" class="msg" :class="{ ok: layMsg.ok }">{{ layMsg.text }}</div>
      <p class="muted" style="margin-bottom:0">想先看看效果：在<router-link to="/articles">文章</router-link>页的手机预览上方可以试看各个排版和深色模式。</p>
    </div>
    <h2>写作设置</h2>
    <div class="card" v-if="p"><ProfileForm :p="p" @save="saveProfile" /></div>
    <h2>栏目</h2>
    <div class="card"><Columns :base="`/api/accounts/${a.id}`" /></div>
    <div class="card"><StyleCard :base="`/api/accounts/${a.id}`" /></div>
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
import StyleCard from './StyleCard.vue'
const route = useRoute(), router = useRouter()
const a = ref(null), p = ref(null), err = ref(''), msg = ref(''), ok = ref(false), busy = ref(false), secret = ref(''), ctl = ref(null)
const base = () => `/api/accounts/${route.params.id}`
const reviewOpts = [[30, '30 分钟'], [60, '1 小时'], [120, '2 小时（默认）'], [240, '4 小时'], [720, '12 小时'], [1440, '1 天'], [4320, '3 天']]
const lay = ref(null), layMsg = ref(null)
async function setLayout(k) {
  try { const r = await put(`${base()}/layout`, { layout: k }); lay.value.layout = k; const n = lay.value.layouts.find((l) => l.key === k)?.name; layMsg.value = { ok: true, text: `已换成「${n}」` + (r.rerendered ? `，重新排版了 ${r.rerendered} 篇还没推送的文章` : '') } }
  catch (e) { layMsg.value = { ok: false, text: e.message } }
}
const say = (m, good = false) => { msg.value = m; ok.value = good }
async function load() { a.value = await get(base()); p.value = await get(`${base()}/profile`); lay.value = await get(`${base()}/layout`).catch(() => null) }
async function check() { busy.value = true; try { await post(`${base()}/check`); await load() } catch (e) { say(e.message) } finally { busy.value = false } }
async function saveSecret() { try { await patch(base(), { app_secret: secret.value }); secret.value = ''; await check() } catch (e) { say(e.message) } }
async function saveProfile() { try { p.value = await put(`${base()}/profile`, p.value); say('设置已保存', true) } catch (e) { say(e.message) } }
async function control(c) {
  try { await put(`${base()}/controls`, c); await load(); ctl.value = { ok: true, text: c.paused !== undefined ? (c.paused ? '已暂停' : '已恢复') : c.confirm_topic !== undefined ? (c.confirm_topic ? '已开启选题确认' : '已关闭选题确认') : c.auto_publish !== undefined ? (c.auto_publish ? '已开启自动发表' : '已关闭自动发表，等待中的文章都不会发了') : '已保存' } }
  catch (e) { ctl.value = { ok: false, text: e.message } }
}
async function unbind() { if (!confirm(`确定解绑「${a.value.name}」？`)) return; try { await del(base()); router.push('/accounts') } catch (e) { say(e.message) } }
onMounted(() => load().catch((e) => (err.value = e.message)))
</script>
