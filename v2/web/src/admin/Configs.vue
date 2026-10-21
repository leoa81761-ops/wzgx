<template>
  <h1>模型配置</h1>
  <p class="muted" v-if="!isRoot">运营人员只能查看；修改需要超级管理员。</p>
  <p class="muted" v-else>密钥只写不读：保存后只显示前后几位；留空表示不修改。接口需兼容 OpenAI（或选 Anthropic 协议）。支持一键导出和导入全站配置。</p>
  <div v-if="msg" class="msg" :class="{ ok }">{{ msg }}</div>
  <table>
    <tr><th>名称</th><th>模型</th><th>推理强度</th><th>密钥</th><th>超时（首字/空闲/总）</th><th>单价（元/百万 tokens 或每张）</th><th>每日上限</th><th>启用</th><th class="act"></th></tr>
    <tr v-for="c in list" :key="c.id">
      <td>{{ c.name }}<div class="muted">{{ kindNames[c.kind] || c.kind }} · {{ c.protocol }} · {{ c.base_url }}</div></td>
      <td>{{ c.model }}</td><td>{{ c.reasoning_effort || '—' }}</td><td class="muted">{{ c.has_key ? c.key_mask : '未设置' }}</td>
      <td class="muted">{{ c.first_byte_timeout_s }}s / {{ c.idle_timeout_s }}s / {{ c.total_timeout_s }}s</td>
      <td class="muted nowrap">{{ c.kind === 'image' ? (c.price_per_image == null ? '未填' : `每张 ${c.price_per_image} 元`) : c.price_in_per_m == null && c.price_out_per_m == null ? '未填' : `输入 ${c.price_in_per_m ?? 0} / 输出 ${c.price_out_per_m ?? 0}` }}</td>
      <td class="muted nowrap">{{ c.daily_cap ? '¥' + c.daily_cap : '不限' }}</td>
      <td>{{ c.enabled ? '是' : '否' }}</td>
      <td class="act"><div class="row" v-if="isRoot"><button @click="edit(c)">编辑</button><button @click="test(c)" :disabled="busy">连接测试</button></div></td>
    </tr>
  </table>
  <div class="row" v-if="isRoot" style="margin-top:12px;gap:8px;flex-wrap:wrap">
    <button class="primary" @click="edit(null)">新增配置</button>
    <button @click="exportConfigs" :disabled="busy">📤 导出全部配置</button>
    <label class="btn-import" :class="{ disabled: busy }">
      <input type="file" accept=".json" @change="importConfigs" style="display:none" :disabled="busy" />
      📥 导入配置
    </label>
  </div>
  <div v-if="testOut" class="card" style="margin-top:12px"><b>{{ testOut.ok ? '连接正常' : '连接失败' }}</b>
    <div class="muted">{{ testOut.model }} {{ testOut.error }}</div><div>{{ testOut.reply }}</div>
    <img v-if="testOut.image" :src="testOut.image" alt="测试图片" style="max-width:360px;margin-top:8px;border-radius:4px" /></div>
  <div v-if="f" class="card" style="margin-top:16px">
    <h2 style="margin-top:0">{{ f.id ? '编辑配置' : '新增配置' }}</h2>
    <div class="two">
      <label class="f"><span>类型</span><select v-model="f.kind" @change="kindChanged"><option value="text">文本模型（写作）</option><option value="search">搜索模型（联网检索）</option><option value="image">图片模型（封面和配图）</option></select></label>
      <label class="f"><span>名称</span><input v-model.trim="f.name" /></label>
      <label class="f"><span>协议</span><select v-model="f.protocol"><option value="openai">OpenAI 兼容</option><option value="anthropic" v-if="f.kind !== 'image'">Anthropic</option></select></label>
      <label class="f"><span>接口地址</span><input v-model.trim="f.base_url" placeholder="https://.../v1" /></label>
      <label class="f"><span>模型名</span><input v-model.trim="f.model" /></label>
      <label class="f" v-if="f.kind !== 'image'"><span>推理强度</span><select v-model="f.reasoning_effort"><option value="">不设置</option><option>low</option><option>medium</option><option>high</option></select></label>
      <label class="f"><span>API 密钥（留空不修改）</span><input v-model.trim="f.api_key" type="password" autocomplete="off" /></label>
      <label class="f" v-if="f.kind !== 'image'"><span>首字超时（秒）</span><input type="number" v-model.number="f.first_byte_timeout_s" /></label>
      <label class="f" v-if="f.kind !== 'image'"><span>空闲超时（秒）</span><input type="number" v-model.number="f.idle_timeout_s" /></label>
      <label class="f"><span>{{ f.kind === 'image' ? '超时（秒，最多按 180 算）' : '总超时（秒）' }}</span><input type="number" v-model.number="f.total_timeout_s" /></label>
      <label class="f"><span>额外参数（JSON，可不填）</span><input v-model="f.paramsText" placeholder='{"max_tokens":16000}' /></label>
    </div>
    <p v-if="f.kind === 'image'" class="hint">图片模型走 OpenAI 图片接口：POST 接口地址/images/generations，一次一张；连接测试会真的生成一张小图（会产生一次费用）。</p>
    <h3>花费</h3>
    <p class="muted">单价用来估算花费，可不填。每日上限：这个配置当天（北京时间）估算花费到了上限就先跳过，改用路由里的下一个；都到上限时任务排队等第二天或上限调高。留空表示不限。</p>
    <div class="two">
      <label class="f" v-if="f.kind === 'image'"><span>每张图片单价（元）</span><input type="number" min="0" step="0.01" v-model="f.price_per_image" /></label>
      <label class="f" v-if="f.kind !== 'image'"><span>输入单价（元 / 百万 tokens）</span><input type="number" min="0" step="0.01" v-model="f.price_in_per_m" /></label>
      <label class="f" v-if="f.kind !== 'image'"><span>输出单价（元 / 百万 tokens）</span><input type="number" min="0" step="0.01" v-model="f.price_out_per_m" /></label>
      <label class="f"><span>每日花费上限（元）</span><input type="number" min="0" step="0.01" v-model="f.daily_cap" placeholder="不限" /></label>
    </div>
    <label class="row" style="margin-bottom:12px"><input type="checkbox" v-model="f.enabled" /><span>启用</span></label>
    <div class="row"><button class="primary" @click="save">保存</button><button @click="f = null">取消</button></div>
  </div>
</template>
<script setup>
import { ref, computed, onMounted } from 'vue'
import { get, post, put } from '../api.js'
import { session } from '../store.js'
const isRoot = computed(() => session.me?.role === 'super_admin')
const kindNames = { text: '文本', search: '搜索', image: '图片' }
function kindChanged() { if (f.value.kind === 'image') { f.value.protocol = 'openai'; f.value.reasoning_effort = ''; if (!f.value.id) f.value.total_timeout_s = 180 } }
const list = ref([]), f = ref(null), msg = ref(''), ok = ref(false), busy = ref(false), testOut = ref(null)
const say = (m, good = false) => { msg.value = m; ok.value = good }
async function load() { list.value = await get('/api/admin/configs') }
function edit(c) {
  f.value = c ? { ...c, api_key: '', paramsText: c.params && JSON.stringify(c.params) !== '{}' ? JSON.stringify(c.params) : '' }
    : { kind: 'text', name: '', protocol: 'openai', base_url: '', model: '', reasoning_effort: '', api_key: '', first_byte_timeout_s: 30, idle_timeout_s: 180, total_timeout_s: 900, enabled: true, paramsText: '' }
}
async function save() {
  const c = { ...f.value }
  try { c.params = c.paramsText ? JSON.parse(c.paramsText) : {} } catch { return say('额外参数不是有效的 JSON') }
  const num = (v) => (v === '' || v == null ? null : Number(v))
  const pricing = { price_in_per_m: num(c.price_in_per_m), price_out_per_m: num(c.price_out_per_m), daily_cap: num(c.daily_cap), price_per_image: num(c.price_per_image) }
  if (Object.values(pricing).some((v) => v != null && (isNaN(v) || v < 0))) return say('单价和上限请填 0 或正数')
  delete c.paramsText; delete c.key_mask; delete c.has_key; delete c.price_in_per_m; delete c.price_out_per_m; delete c.daily_cap; delete c.price_per_image
  if (!c.api_key) delete c.api_key
  try {
    const saved = c.id ? await put(`/api/admin/configs/${c.id}`, c) : await post('/api/admin/configs', c)
    await put(`/api/admin/configs/${saved.id || c.id}/pricing`, pricing)
    f.value = null; await load(); say('已保存', true)
  } catch (e) { say(e.message) }
}
async function test(c) { busy.value = true; testOut.value = null; try { testOut.value = await post(`/api/admin/configs/${c.id}/test`) } catch (e) { say(e.message) } finally { busy.value = false } }

async function exportConfigs() {
  busy.value = true
  try {
    const data = await get('/api/admin/configs/export')
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `gonghao-configs-${new Date().toISOString().slice(0, 10)}.json`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
    say('配置导出成功！', true)
  } catch (e) {
    say('导出失败：' + e.message)
  } finally {
    busy.value = false
  }
}

async function importConfigs(e) {
  const file = e.target.files?.[0]
  if (!file) return
  busy.value = true
  try {
    const text = await file.text()
    const json = JSON.parse(text)
    const res = await post('/api/admin/configs/import', json)
    await load()
    say(`配置导入成功！已处理 ${res.configs_count || 0} 个模型配置、${res.routes_count || 0} 条环节路由、${res.types_count || 0} 种栏目类型、${res.settings_count || 0} 项平台设置。`, true)
  } catch (e) {
    say('导入失败：' + e.message)
  } finally {
    busy.value = false
    e.target.value = ''
  }
}

onMounted(() => load().catch((e) => say(e.message)))
</script>
<style scoped>
.btn-import {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 6px 12px;
  border: 1px solid var(--line, #ccc);
  border-radius: 4px;
  background: var(--btn-bg, #f5f5f5);
  cursor: pointer;
  font-size: 14px;
}
.btn-import:hover {
  background: var(--btn-hover, #eee);
}
.btn-import.disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
</style>
