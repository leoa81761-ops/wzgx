<template>
  <h1>敏感词</h1>
  <p class="muted">每行一个词。文章里出现任何一个词，就会被拦下、不进草稿箱，并在文章列表里写明原因。平台不会替换或改写文章。</p>
  <div v-if="msg" class="msg" :class="{ ok }">{{ msg }}</div>
  <textarea v-model="text" rows="18" style="width:100%;max-width:520px" placeholder="每行一个词"></textarea>
  <p class="muted">共 {{ count }} 个词</p>
  <button class="primary" :disabled="busy" @click="save">保存</button>
</template>
<script setup>
import { ref, computed, onMounted } from 'vue'
import { get, put } from '../api.js'
const text = ref(''), msg = ref(''), ok = ref(false), busy = ref(false)
const words = () => [...new Set(text.value.split('\n').map((s) => s.trim()).filter(Boolean))]
const count = computed(() => words().length)
const say = (m, good = false) => { msg.value = m; ok.value = good }
async function load() { text.value = (await get('/api/admin/sensitive-words')).words.join('\n') }
async function save() {
  busy.value = true
  try { const r = await put('/api/admin/sensitive-words', { words: words() }); text.value = r.words.join('\n'); say('已保存', true) } catch (e) { say(e.message) } finally { busy.value = false }
}
onMounted(() => load().catch((e) => say(e.message)))
</script>
