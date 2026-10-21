<template>
  <h2 style="margin-top:0">我的授权图片</h2>
  <p class="muted">放几张你自己拍的或有授权的图片（填图片网址）。文章没有生成封面时，系统会从这里挑一张当封面；也可以在“文章”里把某张配图换成这里的图片。最多 30 张。</p>
  <div v-if="list.length" class="refgrid">
    <div v-for="x in list" :key="x.id" class="refimg">
      <img :src="x.url" :alt="x.caption" />
      <div class="muted">{{ x.caption || '（无说明）' }} · 用过 {{ x.used }} 次</div>
      <button @click="remove(x)">删除</button>
    </div>
  </div>
  <label class="f"><span>图片网址</span><input v-model="url" placeholder="https://…/photo.jpg" /></label>
  <label class="f"><span>图片说明（可不填）</span><input v-model="caption" maxlength="60" /></label>
  <label class="check"><input type="checkbox" v-model="authorized" /> {{ statement || '我确认拥有这张图片的使用权，或已获得权利人授权。' }}</label>
  <div class="row"><button class="primary" :disabled="busy || !url.trim() || !authorized" @click="add">{{ busy ? '正在下载…' : '添加图片' }}</button></div>
  <p class="muted">只支持 JPG / PNG / GIF，不超过 5MB；内网地址、本机地址不会被下载。</p>
  <div v-if="msg" class="msg" :class="{ ok: msg.ok }">{{ msg.text }}</div>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import { get, post, del } from '../api.js'
const props = defineProps({ base: String })
const list = ref([]), statement = ref(''), url = ref(''), caption = ref(''), authorized = ref(false), busy = ref(false), msg = ref(null)
async function load() { const r = await get(`${props.base}/ref-images`); list.value = r.images; statement.value = r.statement }
async function add() {
  busy.value = true; msg.value = null
  try {
    await post(`${props.base}/ref-images`, { url: url.value.trim(), caption: caption.value.trim(), authorized: authorized.value })
    url.value = ''; caption.value = ''; authorized.value = false; await load(); msg.value = { ok: true, text: '已添加' }
  } catch (e) { msg.value = { ok: false, text: e.message } } finally { busy.value = false }
}
async function remove(x) { if (!confirm('删除这张图片？已经用在文章里的不受影响。')) return; try { await del(`${props.base}/ref-images/${x.id}`); await load() } catch (e) { msg.value = { ok: false, text: e.message } } }
onMounted(() => load().catch(() => {}))
</script>
<style scoped>
.refgrid { display: flex; flex-wrap: wrap; gap: 10px; margin-bottom: 10px; }
.refimg { width: 160px; } .refimg img { width: 160px; height: 110px; object-fit: cover; border-radius: 4px; display: block; }
.check { display: block; margin: 8px 0; }
</style>
