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
        <div v-if="cur.status === 'blocked'" class="msg">没有通过检查：{{ cur.block_reason }}</div>
        <div v-if="cur.status === 'pushed'" class="msg ok">已在公众号草稿箱，可以去公众号后台检查后发表。</div>
        <div v-if="pushErr" class="msg">{{ pushErr }}</div>
        <button v-if="cur.status === 'ready'" class="primary" @click="confirming = true">推送到草稿箱</button>
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
      <div class="phone"><iframe :src="`/api/articles/${cur.id}/preview`" sandbox="" title="手机预览" /></div>
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
const curAccount = computed(() => list.value.find((a) => a.id === cur.value?.id)?.account_name || '')
async function load() { list.value = await get('/api/articles') }
async function open(id) {
  pushErr.value = ''; fbMsg.value = null; rbErr.value = ''
  cur.value = await get(`/api/articles/${id}`)
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
  try { await load(); const id = route.query.id || list.value[0]?.id; if (id) await open(id) } catch (e) { err.value = e.message }
})
</script>
