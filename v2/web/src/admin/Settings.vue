<template>
  <h1>平台设置</h1>
  <div class="card" style="max-width:560px">
    <h2 style="margin-top:0">没人理的草稿，自动暂停计划</h2>
    <p class="muted">如果一个公众号最近连续这么多篇推送到草稿箱后，三天里都没改动、用户也没评价，就自动暂停它的写作计划，免得一直白写。用户在写作计划页一点就能恢复。填 0 表示不自动暂停（默认）。</p>
    <label class="row"><span>连续</span><input type="number" min="0" max="50" v-model.number="s.auto_pause_after" style="width:90px" /><span>篇</span></label>
    <h2>全平台每日模型花费上限</h2>
    <p class="muted">所有模型调用当天（北京时间）的估算花费到了这个数，就暂停取新任务，任务排队等待，后台顶部会提示。花费按“模型配置”里的单价估算。填 0 表示不限（默认）。只有超级管理员可以修改。</p>
    <label class="row"><span>每天最多</span><input type="number" min="0" step="1" v-model.number="s.daily_cap" style="width:120px" :disabled="!isRoot" /><span>元</span></label>
    <div v-if="msg" class="msg" :class="{ ok: msg.ok }">{{ msg.text }}</div>
    <button class="primary" style="margin-top:10px" :disabled="busy" @click="save">保存</button>
  </div>
</template>
<script setup>
import { reactive, ref, onMounted } from 'vue'
import { computed } from 'vue'
import { get, put } from '../api.js'
import { session } from '../store.js'
const isRoot = computed(() => session.me?.role === 'super_admin')
const s = reactive({ auto_pause_after: 0, daily_cap: 0 }), msg = ref(null), busy = ref(false)
async function save() {
  busy.value = true
  try { Object.assign(s, await put('/api/admin/settings', { auto_pause_after: s.auto_pause_after || 0, daily_cap: s.daily_cap || 0 }))
    msg.value = { ok: true, text: (s.auto_pause_after ? `已保存：连续 ${s.auto_pause_after} 篇没人理就暂停` : '已保存：不自动暂停') + (s.daily_cap ? `；每天花费上限 ¥${s.daily_cap}` : '；花费不设上限') } }
  catch (e) { msg.value = { ok: false, text: e.message } } finally { busy.value = false }
}
onMounted(async () => { try { Object.assign(s, await get('/api/admin/settings')) } catch (e) { msg.value = { ok: false, text: e.message } } })
</script>
