<template>
  <h2 style="margin-top:0">我的写作风格</h2>
  <p class="muted">贴 3 到 5 篇你自己写的文章，系统只提炼风格特点（语气、句子长短、结构习惯、用词），写文章时照这个风格写。范文原文不保存，用完就丢。可以给整个公众号设一个，也可以给某个栏目单独设（栏目的优先）。</p>
  <table v-if="list.length">
    <tr><th>用在</th><th>风格特点</th><th>启用</th><th></th></tr>
    <tr v-for="s in list" :key="s.id">
      <td class="nowrap">{{ s.column_id ? '栏目：' + s.column_name : '整个公众号' }}<div class="muted">来自 {{ s.samples }} 篇范文</div></td>
      <td><div v-for="k in keys" :key="k[0]" v-show="s.traits[k[0]]"><span class="muted">{{ k[1] }}：</span>{{ s.traits[k[0]] }}</div></td>
      <td><input type="checkbox" :checked="s.enabled" @change="toggle(s, $event.target.checked)" /></td>
      <td><button @click="remove(s)">删除</button></td>
    </tr>
  </table>
  <button v-if="!open" @click="start">{{ list.length ? '再提炼一个 / 重新提炼' : '贴范文提炼风格' }}</button>
  <div v-if="open" style="margin-top:10px">
    <label class="f"><span>用在</span>
      <select v-model="scope"><option :value="null">整个公众号</option><option v-for="c in columns" :key="c.id" :value="c.id">栏目：{{ c.name }}</option></select></label>
    <label v-for="(x, i) in samples" :key="i" class="f"><span>范文 {{ i + 1 }}（{{ x.length }} 字，至少 200 字）</span><textarea v-model="samples[i]" rows="5"></textarea></label>
    <div class="row">
      <button v-if="samples.length < 5" @click="samples.push('')">再加一篇</button>
      <button class="primary" :disabled="busy || filled < 3" @click="learn">{{ busy ? '正在提炼…' : '提炼风格' }}</button>
      <button @click="open = false">取消</button>
    </div>
  </div>
  <div v-if="msg" class="msg" :class="{ ok: msg.ok }">{{ msg.text }}</div>
</template>
<script setup>
import { ref, computed, onMounted } from 'vue'
import { get, post, put, del } from '../api.js'
const props = defineProps({ base: String })
const keys = [['tone', '语气'], ['sentence', '句子'], ['structure', '结构'], ['vocabulary', '用词'], ['other', '其他']]
const list = ref([]), columns = ref([]), open = ref(false), scope = ref(null), samples = ref(['', '', '']), busy = ref(false), msg = ref(null)
const filled = computed(() => samples.value.filter((x) => x.trim().length >= 200).length)
async function load() { list.value = await get(`${props.base}/style`) }
function start() { open.value = true; samples.value = ['', '', '']; msg.value = null; get(`${props.base}/columns`).then((c) => { columns.value = c }).catch(() => {}) }
async function learn() {
  busy.value = true; msg.value = null
  try {
    await post(`${props.base}/style`, { column_id: scope.value, samples: samples.value.filter((x) => x.trim()) })
    samples.value = ['', '', '']; open.value = false; await load(); msg.value = { ok: true, text: '已提炼并启用，范文原文没有保存' }
  } catch (e) { msg.value = { ok: false, text: e.message } } finally { busy.value = false }
}
async function toggle(s, on) { try { await put(`${props.base}/style/${s.id}`, { enabled: on }); await load() } catch (e) { msg.value = { ok: false, text: e.message } } }
async function remove(s) { if (!confirm('删除这个风格？')) return; try { await del(`${props.base}/style/${s.id}`); await load() } catch (e) { msg.value = { ok: false, text: e.message } } }
onMounted(() => load().catch(() => {}))
</script>
