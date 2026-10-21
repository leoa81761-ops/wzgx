<template>
  <div v-for="p in list" :key="p.job_id" class="card hint" style="margin-bottom:12px">
    <div class="muted">{{ p.account_name }}<span v-if="p.column_name"> · {{ p.column_name }}</span> · 等你确认选题</div>
    <div style="font-size:17px;margin:6px 0"><b>{{ p.proposal.topic }}</b></div>
    <div v-if="p.proposal.angle" class="muted">写法：{{ p.proposal.angle }}</div>
    <div v-if="p.proposal.kind === 'news'" class="muted">这是一条近期动态。</div>
    <div class="row" style="margin-top:10px">
      <button class="primary" :disabled="busy[p.job_id]" @click="decide(p, 'approve')">就写这个</button>
      <button :disabled="busy[p.job_id]" @click="decide(p, 'reject')">换一个<span class="muted">（还能换 {{ p.rejects_left - 1 > 0 ? p.rejects_left - 1 : 0 }} 次）</span></button>
      <button :disabled="busy[p.job_id]" @click="editing[p.job_id] = !editing[p.job_id]">我来改题目</button>
    </div>
    <p v-if="p.rejects_left <= 1" class="muted" style="margin:6px 0 0">再点「换一个」这次就不写了，额度会退回。</p>
    <div v-if="editing[p.job_id]" class="row" style="margin-top:8px">
      <input v-model="own[p.job_id]" placeholder="写上你想要的题目（2–60 字）" style="max-width:420px" />
      <button class="primary" :disabled="!own[p.job_id] || busy[p.job_id]" @click="decide(p, 'change')">用这个题目写</button>
    </div>
    <div v-if="msg[p.job_id]" class="msg">{{ msg[p.job_id] }}</div>
  </div>
  <div v-if="done" class="msg ok">{{ done }}</div>
</template>
<script setup>
// topics waiting for the user's decision (topic confirmation mode)
import { ref, reactive, onMounted, onUnmounted } from 'vue'
import { get, post } from '../api.js'
const list = ref([]), busy = reactive({}), editing = reactive({}), own = reactive({}), msg = reactive({}), done = ref('')
async function load() { try { list.value = await get('/api/me/topics') } catch { /* keep the last list */ } }
async function decide(p, action) {
  busy[p.job_id] = true; msg[p.job_id] = ''
  try {
    const r = await post(`/api/jobs/${p.job_id}/topic`, { action, topic: own[p.job_id] || '' })
    done.value = action === 'reject'
      ? (r.status === 'cancelled' ? '已经换了 3 次，这次不写了，额度已退回。' : '好的，正在换一个题目，稍等片刻会出现在这里。')
      : '好的，开始写了，几分钟后在「文章」里能看到。'
    await load()
  } catch (e) { msg[p.job_id] = e.message } finally { busy[p.job_id] = false }
}
let timer
onMounted(() => { load(); timer = setInterval(load, 10000) })
onUnmounted(() => clearInterval(timer))
</script>
