<template>
  <h1>文章</h1>
  <div v-if="err" class="msg">{{ err }}</div>
  <div class="split">
    <div class="list">
      <table v-if="list.length">
        <tr><th>标题</th><th>状态</th><th>时间</th></tr>
        <tr v-for="a in list" :key="a.id" @click="open(a.id)" style="cursor:pointer" :style="{ background: cur?.id === a.id ? '#f0f7f4' : '' }">
          <td>{{ a.title || a.topic || '（无标题）' }} <span v-if="a.topic_kind === 'practical'" class="tag">实用题</span></td>
          <td><span class="tag" :class="a.status">{{ statusText[a.status] || a.status }}</span></td>
          <td class="muted">{{ fmtTime(a.created_at) }}</td>
        </tr>
      </table>
      <p v-else class="muted">还没有文章。到「写作计划」点「现在写一篇」。</p>
    </div>
    <div v-if="cur">
      <div class="card" style="width:390px">
        <b>{{ cur.title }}</b>
        <p class="muted" v-if="cur.summary">{{ cur.summary }}</p>
        <div v-if="cur.status === 'blocked'" class="msg">没有通过检查：{{ cur.block_reason }}</div>
        <div v-if="cur.status === 'pushed'" class="msg ok">已在公众号草稿箱，可以去公众号后台检查后发表。</div>
        <div v-if="pushErr" class="msg">{{ pushErr }}</div>
        <button v-if="cur.status === 'ready'" class="primary" @click="confirming = true">推送到草稿箱</button>
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
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { get, post, fmtTime, statusText } from '../api.js'
const list = ref([]), cur = ref(null), err = ref(''), pushErr = ref(''), confirming = ref(false), busy = ref(false)
const route = useRoute()
async function load() { list.value = await get('/api/articles') }
async function open(id) { pushErr.value = ''; cur.value = await get(`/api/articles/${id}`) }
async function push() {
  busy.value = true; pushErr.value = ''
  try { await post(`/api/articles/${cur.value.id}/push`); confirming.value = false; await open(cur.value.id); await load() }
  catch (e) { pushErr.value = e.message; confirming.value = false } finally { busy.value = false }
}
onMounted(async () => {
  try { await load(); const id = route.query.id || list.value[0]?.id; if (id) await open(id) } catch (e) { err.value = e.message }
})
</script>
