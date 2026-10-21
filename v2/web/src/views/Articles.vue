<template>
  <h1>文章</h1>
  <div v-if="err" class="msg">{{ err }}</div>
  <div class="split">
    <div class="list">
      <table v-if="list.length">
        <tr><th>标题</th><th>公众号</th><th>状态</th><th>时间</th></tr>
        <tr v-for="a in list" :key="a.id" @click="open(a.id)" style="cursor:pointer" :style="{ background: cur?.id === a.id ? '#f0f7f4' : '' }">
          <td>{{ a.title || a.topic || '（无标题）' }} <span v-if="a.topic_kind === 'practical'" class="tag">实用题</span></td>
          <td class="nowrap">{{ a.account_name || '—' }}</td>
          <td class="nowrap"><span class="tag" :class="a.status">{{ statusText[a.status] || a.status }}</span></td>
          <td class="muted nowrap">{{ fmtTime(a.created_at) }}</td>
        </tr>
      </table>
      <p v-else class="muted">还没有文章。到「写作计划」点「现在写一篇」。</p>
    </div>
    <div v-if="cur">
      <div class="card" style="width:390px">
        <b>{{ cur.title }}</b>
        <div class="muted" v-if="curAccount">{{ curAccount }}</div>
        <p class="muted" v-if="cur.summary">{{ cur.summary }}</p>
        <div v-if="cur.fact_check" class="factcheck">
          <b>事实核对</b>
          <span v-if="cur.fact_check.no_research" class="muted">：这篇没有检索资料，下面这些都没法核对，请自己确认。</span>
          <span v-else-if="!cur.fact_check.unsupported" class="muted">：文中 {{ cur.fact_check.claims.length }} 处数字、标准号、日期都能在资料里找到。</span>
          <span v-else>：{{ cur.fact_check.unsupported }} 处在资料里没找到依据，预览里已标黄，推送前请核对。</span>
          <ul v-if="cur.fact_check.unsupported">
            <li v-for="c in cur.fact_check.claims.filter((x) => !x.supported)" :key="c.text"><b>{{ c.text }}</b> <span class="muted">{{ c.context }}</span></li>
          </ul>
        </div>
        <div class="cover">
          <img v-if="cur.cover_url" :src="cur.cover_url" alt="封面" />
          <div class="muted">{{ cur.cover_url ? (cur.cover_ref ? '封面（你的授权图片）' : '封面（AI 生成）') : '封面：推送时使用默认封面或文中第一张图' }}</div>
        </div>
        <div v-if="cur.status === 'ready' || cur.status === 'blocked'" class="refswap">
          <button v-if="!swap.open" @click="openSwap">换成我的授权图片</button>
          <template v-else>
            <label class="f"><span>换哪张</span><select v-model="swap.slot"><option :value="0">封面</option><option v-for="n in 3" :key="n" :value="n">文中第 {{ n }} 张配图</option></select></label>
            <label class="f"><span>用哪张</span><select v-model="swap.ref"><option :value="null">填一个新的图片网址</option><option v-for="x in swap.lib" :key="x.id" :value="x.id">{{ x.caption || ('图片 #' + x.id) }}</option></select></label>
            <template v-if="swap.ref === null">
              <label class="f"><span>图片网址</span><input v-model="swap.url" placeholder="https://…/photo.jpg" /></label>
              <label class="check"><input type="checkbox" v-model="swap.authorized" /> {{ swap.statement }}</label>
            </template>
            <div class="row"><button class="primary" :disabled="busy || (swap.ref === null && (!swap.url.trim() || !swap.authorized))" @click="doSwap">换图</button><button @click="swap.open = false">取消</button></div>
            <div v-if="swap.msg" class="msg" :class="{ ok: swap.msg.ok }">{{ swap.msg.text }}</div>
          </template>
        </div>
        <div v-if="cur.status === 'blocked'" class="msg">没有通过检查：{{ cur.block_reason }}</div>
        <div v-if="cur.status === 'pushed'" class="msg ok">已在公众号草稿箱，可以去公众号后台检查后发表。</div>
        <div v-if="pushErr" class="msg">{{ pushErr }}</div>
        <button v-if="cur.status === 'ready'" class="primary" @click="confirming = true">推送到草稿箱</button>
        <div v-if="cur.publish_status" class="pubstate" :class="cur.publish_status">
          <template v-if="cur.publish_status === 'scheduled'">
            将在 <b>{{ fmtTime(cur.publish_at) }}</b> 自动发表<span v-if="today?.publish_paused">（你已暂停全部自动发表，恢复后才会发）</span>。
            <button class="danger" :disabled="busy" @click="cancelPublish">取消自动发表</button>
            <div v-if="cur.publish_note" class="muted">{{ cur.publish_note }}</div>
          </template>
          <template v-else-if="cur.publish_status === 'published'">已自动发表（{{ fmtTime(cur.published_at) }}）<a v-if="cur.publish_url" :href="cur.publish_url" target="_blank" rel="noopener">查看文章</a></template>
          <template v-else>{{ { cancelled: '不自动发表', submitting: '正在提交发表', publishing: '已提交发表，等待微信处理', failed: '自动发表没有成功' }[cur.publish_status] }}<span v-if="cur.publish_note">：{{ cur.publish_note }}</span></template>
        </div>
        <div v-if="cur.status === 'pushed'" style="margin-top:8px">
          <span class="muted">草稿有没有改过：</span>{{ readbackText(cur) }}
          <button style="margin-left:6px" :disabled="busy" @click="readback">现在检查</button>
          <div v-if="rbErr" class="msg">{{ rbErr }}</div>
        </div>
        <div v-if="cur.status === 'ready' || cur.status === 'pushed' || cur.status === 'blocked'" style="margin-top:12px;border-top:1px solid var(--line);padding-top:10px">
          <span class="muted">这篇怎么样？</span>
          <div class="row" style="margin-top:6px">
            <button :class="{ primary: fb.rating === 1 }" @click="fb.rating = 1">👍 好</button>
            <button :class="{ primary: fb.rating === -1 }" @click="fb.rating = -1">👎 不好</button>
          </div>
          <textarea v-model="fb.note" rows="2" maxlength="500" placeholder="想说点什么（可不填），比如哪里不好、希望怎么写" style="margin-top:6px;min-height:0"></textarea>
          <button style="margin-top:6px" :disabled="!fb.rating || fbBusy" @click="saveFeedback">{{ cur.rating ? '更新评价' : '提交评价' }}</button>
          <span v-if="fbMsg" :class="fbMsg.ok ? 'ok' : 'bad'" style="margin-left:8px">{{ fbMsg.text }}</span>
        </div>
      </div>
      <div>
        <div class="row" style="gap:6px;margin-bottom:8px;flex-wrap:wrap">
          <button :class="{ primary: theme === 'light' }" @click="theme = 'light'">浅色</button>
          <button :class="{ primary: theme === 'dark' }" @click="theme = 'dark'">深色</button>
          <template v-if="layouts.length && cur.status !== 'pushed'">
            <span class="muted" style="margin-left:6px">排版</span>
            <select v-model="tryLayout" style="width:auto">
              <option v-for="l in layouts" :key="l.key" :value="l.key">{{ l.name }}{{ l.key === cur.layout ? '（当前）' : '' }}</option>
            </select>
            <button v-if="tryLayout !== cur.layout" @click="applyLayout" :disabled="busy">用这个排版</button>
          </template>
        </div>
        <div class="phone" :class="{ dark: theme === 'dark' }"><iframe :src="previewURL" sandbox="" title="手机预览" /></div>
        <p class="muted" style="margin:6px 0 0;max-width:390px">
          <template v-if="theme === 'dark'">深色是模拟微信深色模式的大致效果，实际以手机为准。</template>
          <template v-if="tryLayout !== cur.layout && cur.status !== 'pushed'">正在试看「{{ layoutName(tryLayout) }}」，点「用这个排版」会应用到这个公众号，并重新排版还没推送的文章（文字不变）。</template>
          <template v-if="layoutMsg">{{ layoutMsg }}</template>
        </p>
      </div>
    </div>
  </div>
  <div v-if="confirming" class="modal-bg" @click.self="confirming = false">
    <div class="modal">
      <p>把《{{ cur.title }}》推送到公众号的<b>草稿箱</b>？</p>
      <p class="muted">只是放进草稿箱，不会发表；发表需要你在公众号后台操作。</p>
      <div class="row"><button class="primary" @click="push" :disabled="busy">确认推送</button><button @click="confirming = false">取消</button></div>
    </div>
  </div>
</template>
<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { get, post, put, fmtTime, statusText } from '../api.js'
const list = ref([]), cur = ref(null), err = ref(''), pushErr = ref(''), confirming = ref(false), busy = ref(false)
const fb = reactive({ rating: 0, note: '' }), fbMsg = ref(null), fbBusy = ref(false), rbErr = ref('')
const route = useRoute()
const today = ref(null)
async function cancelPublish() {
  busy.value = true
  try { await post(`/api/articles/${cur.value.id}/publish/cancel`); await open(cur.value.id) } catch (e) { pushErr.value = e.message } finally { busy.value = false }
}
const theme = ref('light'), layouts = ref([]), tryLayout = ref(''), layoutMsg = ref('')
const layoutName = (k) => layouts.value.find((l) => l.key === k)?.name || k
const previewURL = computed(() => {
  const q = new URLSearchParams()
  if (theme.value === 'dark') q.set('theme', 'dark')
  if (tryLayout.value && tryLayout.value !== cur.value.layout) q.set('layout', tryLayout.value)
  const s = q.toString()
  return `/api/articles/${cur.value.id}/preview` + (s ? '?' + s : '') + (cur.value._v ? (s ? '&' : '?') + 'v=' + cur.value._v : '')
})
async function applyLayout() {
  busy.value = true
  try {
    const r = await put(`/api/accounts/${cur.value.account_id}/layout`, { layout: tryLayout.value })
    const id = cur.value.id; await open(id); cur.value._v = Date.now()
    layoutMsg.value = `已换成「${layoutName(r.layout)}」，重新排版了 ${r.rerendered} 篇还没推送的文章。`
  } catch (e) { layoutMsg.value = e.message } finally { busy.value = false }
}
const curAccount = computed(() => list.value.find((a) => a.id === cur.value?.id)?.account_name || '')
async function load() { list.value = await get('/api/articles') }
const swap = reactive({ open: false, slot: 0, ref: null, url: '', authorized: false, lib: [], statement: '', msg: null })
async function openSwap() {
  Object.assign(swap, { open: true, slot: 0, ref: null, url: '', authorized: false, msg: null })
  try { const r = await get(`/api/accounts/${cur.value.account_id}/ref-images`); swap.lib = r.images; swap.statement = r.statement; if (r.images.length) swap.ref = r.images[0].id } catch (e) { swap.msg = { ok: false, text: e.message } }
}
async function doSwap() {
  busy.value = true; swap.msg = null
  const body = swap.ref === null ? { slot: swap.slot, url: swap.url.trim(), authorized: swap.authorized } : { slot: swap.slot, ref_id: swap.ref }
  try { await post(`/api/articles/${cur.value.id}/ref-image`, body); const id = cur.value.id; await open(id); cur.value._v = Date.now(); swap.open = false }
  catch (e) { swap.msg = { ok: false, text: e.message } } finally { busy.value = false }
}
async function open(id) {
  pushErr.value = ''; fbMsg.value = null; rbErr.value = ''
  cur.value = await get(`/api/articles/${id}`)
  tryLayout.value = cur.value.layout; layoutMsg.value = ''
  if (!layouts.value.length) layouts.value = (await get(`/api/accounts/${cur.value.account_id}/layout`).catch(() => ({ layouts: [] }))).layouts
  fb.rating = cur.value.rating || 0; fb.note = cur.value.feedback_note || ''
}
function readbackText(a) {
  const pct = a.change_ratio != null ? `（约改动 ${Math.round(a.change_ratio * 100)}%）` : ''
  switch (a.readback_status) {
    case 'unedited': return '没有改动'
    case 'edited': return '你改过了' + pct + (a.readback_note ? '，' + a.readback_note : '')
    case 'gone': return a.readback_note || '草稿箱里已经没有这篇（可能已发表或删除）'
    case 'error': return a.readback_note || '上次检查失败'
    default: return '还没检查（推送后过一阵会自动检查）'
  }
}
async function readback() {
  busy.value = true; rbErr.value = ''
  try { await post(`/api/articles/${cur.value.id}/readback`); await open(cur.value.id) } catch (e) { rbErr.value = e.message } finally { busy.value = false }
}
async function saveFeedback() {
  fbBusy.value = true
  try { await put(`/api/articles/${cur.value.id}/feedback`, { rating: fb.rating, note: fb.note }); cur.value.rating = fb.rating; fbMsg.value = { ok: true, text: '谢谢，已记下' } }
  catch (e) { fbMsg.value = { ok: false, text: e.message } } finally { fbBusy.value = false }
}
async function push() {
  busy.value = true; pushErr.value = ''
  try { await post(`/api/articles/${cur.value.id}/push`); confirming.value = false; await open(cur.value.id); await load() }
  catch (e) { pushErr.value = e.message; confirming.value = false } finally { busy.value = false }
}
onMounted(async () => {
  get('/api/me/today').then((t) => { today.value = t }).catch(() => {})
  try { await load(); const id = route.query.id || list.value[0]?.id; if (id) await open(id) } catch (e) { err.value = e.message }
})
</script>
