<template>
  <h1>套餐设置</h1>
  <p class="muted">这里的套餐会显示在公开的<a href="/pricing" target="_blank">价格页</a>上，手动开通订单时也按这里的数量预填。价格只是展示文字（目前没有在线支付），留空会显示「联系管理员」。<span v-if="!isRoot">运营人员只能查看。</span></p>
  <div v-if="msg" class="msg" :class="{ ok: msg.ok }">{{ msg.text }}</div>
  <table>
    <tr><th>套餐</th><th>名称</th><th>价格说明</th><th>可绑公众号</th><th>每天篇数</th><th>介绍</th><th>AI 封面和配图</th><th>显示</th><th>排序</th><th></th></tr>
    <tr v-for="t in list" :key="t.key">
      <td class="muted nowrap">{{ t.key }}</td>
      <td style="width:110px"><input v-model="t.name" :disabled="!isRoot" /></td>
      <td style="width:130px"><input v-model="t.price_text" placeholder="联系管理员" :disabled="!isRoot" /></td>
      <td style="width:80px"><input type="number" min="0" v-model.number="t.max_accounts" :disabled="!isRoot" /></td>
      <td style="width:80px"><input type="number" min="0" v-model.number="t.daily_quota" :disabled="!isRoot" /></td>
      <td><input v-model="t.description" :disabled="!isRoot" /></td>
      <td><input type="checkbox" v-model="t.ai_images" :disabled="!isRoot" /></td>
      <td><input type="checkbox" v-model="t.visible" :disabled="!isRoot" /></td>
      <td style="width:64px"><input type="number" v-model.number="t.sort" :disabled="!isRoot" /></td>
      <td><button v-if="isRoot" @click="save(t)">保存</button></td>
    </tr>
  </table>
  <p class="muted">AI 封面和配图：开启的套餐，系统会给每篇文章生成封面，并自己决定文中要不要配图（最多 3 张）；关闭的套餐用默认封面和默认图片。</p>
  <p class="muted">联系方式在「平台设置」里填写，会显示在价格页。</p>
</template>
<script setup>
import { ref, computed, onMounted } from 'vue'
import { get, put } from '../api.js'
import { session } from '../store.js'
const isRoot = computed(() => session.me?.role === 'super_admin')
const list = ref([]), msg = ref(null)
async function save(t) { try { await put(`/api/admin/tiers/${t.key}`, t); msg.value = { ok: true, text: `已保存「${t.name}」` } } catch (e) { msg.value = { ok: false, text: e.message } } }
onMounted(async () => { try { list.value = await get('/api/admin/tiers') } catch (e) { msg.value = { ok: false, text: e.message } } })
</script>
