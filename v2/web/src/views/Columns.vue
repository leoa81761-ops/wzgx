<template>
  <table v-if="cols.length">
    <tr><th>栏目</th><th>文章类型</th><th>栏目说明</th><th>篇幅参考</th><th>占比</th><th>启用</th><th></th></tr>
    <tr v-for="c in cols" :key="c.id">
      <td><input v-model.trim="c.name" /></td>
      <td><select v-model.number="c.article_type_id"><option v-for="t in types" :key="t.id" :value="t.id">{{ t.name }}</option></select></td>
      <td><input v-model="c.note" /></td>
      <td style="width:110px"><input v-model.number="c.length_hint" type="number" min="0" placeholder="不限" /></td>
      <td style="width:80px"><input v-model.number="c.weight" type="number" min="1" /></td>
      <td><input type="checkbox" v-model="c.enabled" /></td>
      <td class="row"><button @click="save(c)">保存</button><button class="danger" @click="remove(c)">删除</button></td>
    </tr>
  </table>
  <p v-else class="muted">还没有栏目。每个栏目选一种文章类型；篇幅参考只是给写作的提示，不会强制。</p>
  <div class="row" style="margin-top:10px">
    <input v-model.trim="nc.name" placeholder="新栏目名称" style="max-width:200px" />
    <select v-model.number="nc.article_type_id" style="max-width:180px"><option :value="0" disabled>选择文章类型</option>
      <option v-for="t in types" :key="t.id" :value="t.id">{{ t.name }}</option></select>
    <button @click="add" :disabled="!nc.name || !nc.article_type_id">添加栏目</button>
  </div>
  <div v-if="msg" class="msg" :class="{ ok: ok }">{{ msg }}</div>
</template>
<script setup>
import { ref, reactive, onMounted } from 'vue'
import { get, post, put, del } from '../api.js'
const props = defineProps({ base: String }) // /api/accounts/{id} or /api/admin/accounts/{id}
const cols = ref([]), types = ref([]), msg = ref(''), ok = ref(false)
const nc = reactive({ name: '', article_type_id: 0 })
const say = (m, good = false) => { msg.value = m; ok.value = good }
const body = (c) => ({ ...c, length_hint: c.length_hint === '' || c.length_hint == null ? null : Number(c.length_hint), weight: c.weight || 1 })
async function load() { cols.value = await get(`${props.base}/columns`); types.value = await get('/api/article-types') }
async function save(c) { try { await put(`${props.base}/columns/${c.id}`, body(c)); say('已保存', true) } catch (e) { say(e.message) } }
async function remove(c) { if (!confirm(`删除栏目「${c.name}」？`)) return; try { await del(`${props.base}/columns/${c.id}`); await load() } catch (e) { say(e.message) } }
async function add() {
  try { await post(`${props.base}/columns`, { ...nc, weight: 1, enabled: true }); nc.name = ''; nc.article_type_id = 0; await load(); say('已添加', true) } catch (e) { say(e.message) }
}
onMounted(() => load().catch((e) => say(e.message)))
</script>
