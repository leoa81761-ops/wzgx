<template>
  <h1>文章类型</h1>
  <p class="muted">用户的每个栏目选一种类型。「需要近期动态」的类型会先搜近期事件再选题；「检索资料」关掉后，这类文章不再联网查资料（适合故事、随笔）。</p>
  <div v-if="msg" class="msg" :class="{ ok }">{{ msg }}</div>
  <table>
    <tr><th>名称</th><th>标识</th><th>说明（给写作模型看）</th><th>需要近期动态</th><th>检索资料</th><th>启用</th><th>排序</th><th></th></tr>
    <tr v-for="t in list" :key="t.id || 'new'">
      <td><input v-model.trim="t.name" /></td>
      <td style="width:120px"><input v-model.trim="t.key" :disabled="!!t.id" placeholder="英文小写" /></td>
      <td><input v-model="t.description" /></td>
      <td><input type="checkbox" v-model="t.needs_news" /></td>
      <td><input type="checkbox" v-model="t.research" /></td>
      <td><input type="checkbox" v-model="t.enabled" /></td>
      <td style="width:70px"><input type="number" v-model.number="t.sort" /></td>
      <td><button @click="save(t)">保存</button></td>
    </tr>
  </table>
  <button style="margin-top:10px" @click="list.push({ name: '', key: '', description: '', needs_news: false, research: true, enabled: true, sort: 0 })">新增类型</button>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import { get, post, put } from '../api.js'
const list = ref([]), msg = ref(''), ok = ref(false)
const say = (m, good = false) => { msg.value = m; ok.value = good }
async function load() { list.value = await get('/api/article-types') }
async function save(t) {
  try { if (t.id) await put(`/api/admin/article-types/${t.id}`, t); else await post('/api/admin/article-types', t); await load(); say('已保存', true) } catch (e) { say(e.message) }
}
onMounted(() => load().catch((e) => say(e.message)))
</script>
