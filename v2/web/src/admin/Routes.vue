<template>
  <h1>环节路由</h1>
  <p class="muted">每个环节用哪个模型配置：第一个是主用，后面的依次备用。这里的设置对所有套餐生效。</p>
  <p v-if="loaded && !anyRoute" class="hint">还没有设置任何环节的模型，文章写不出来。</p>
  <div v-if="msg" class="msg" :class="{ ok }">{{ msg }}</div>
  <table>
    <tr><th>环节</th><th>使用的配置（按顺序）</th><th v-if="isRoot"></th></tr>
    <tr v-for="s in stages" :key="s">
      <td>{{ stageNames[s] || s }}</td>
      <td>
        <div v-for="(id, i) in routes[s]" :key="i" class="row" style="margin-bottom:4px">
          <span>{{ i === 0 ? '主用' : '备用' + i }}</span>
          <select v-model.number="routes[s][i]" :disabled="!isRoot" style="max-width:320px"><option v-for="c in forStage(s)" :key="c.id" :value="c.id">{{ c.name }}（{{ c.model }}）</option></select>
          <button v-if="isRoot" @click="routes[s].splice(i, 1)">移除</button>
        </div>
        <button v-if="isRoot && forStage(s).length" @click="routes[s].push(forStage(s)[0].id)">添加</button>
        <div v-if="s === 'image'" class="muted">不设置就不生成图片：文中用默认图片，封面用默认封面。哪些套餐有 AI 配图在「套餐设置」里改。</div>
      </td>
      <td v-if="isRoot" class="act"><button class="primary" @click="save(s)">保存</button></td>
    </tr>
  </table>
</template>
<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { get, put, del } from '../api.js'
import { session } from '../store.js'
const stageNames = { topic: '选题', research: '检索资料', write: '写作', review: '自查', image: '配图（封面和文中图片）' }
const forStage = (s) => configs.value.filter((c) => (s === 'image') === (c.kind === 'image'))
const isRoot = computed(() => session.me?.role === 'super_admin')
const loaded = ref(false), anyRoute = computed(() => stages.value.some((s) => routes[s]?.length))
const stages = ref([]), routes = reactive({}), configs = ref([]), msg = ref(''), ok = ref(false)
const say = (m, good = false) => { msg.value = m; ok.value = good }
async function load() {
  const r = await get('/api/admin/routes'); configs.value = await get('/api/admin/configs')
  stages.value = r.stages
  loaded.value = true
  for (const s of r.stages) routes[s] = [...((r.routes || []).find((x) => x.stage === s && (!x.tier || x.tier === '*'))?.config_ids || [])]
}
async function save(s) {
  try {
    if (!routes[s].length) { await del(`/api/admin/routes/${s}`).catch(() => {}); say('已清空这个环节', true); return }
    await put(`/api/admin/routes/${s}`, { tier: '*', config_ids: routes[s] }); say('已保存', true)
  } catch (e) { say(e.message) }
}
onMounted(() => load().catch((e) => say(e.message)))
</script>
