<template>
  <h1>平台设置</h1>
  <div class="card" style="max-width:560px">
    <h2 style="margin-top:0">没人理的草稿，自动暂停计划</h2>
    <p class="muted">如果一个公众号最近连续这么多篇推送到草稿箱后，三天里都没改动、用户也没评价，就自动暂停它的写作计划，免得一直白写。用户在写作计划页一点就能恢复。填 0 表示不自动暂停（默认）。</p>
    <label class="row"><span>连续</span><input type="number" min="0" max="50" v-model.number="s.auto_pause_after" style="width:90px" /><span>篇</span></label>
    <h2>全平台每日模型花费上限</h2>
    <p class="muted">所有模型调用当天（北京时间）的估算花费到了这个数，就暂停取新任务，任务排队等待，后台顶部会提示。花费按“模型配置”里的单价估算。填 0 表示不限（默认）。只有超级管理员可以修改。</p>
    <label class="row"><span>每天最多</span><input type="number" min="0" step="1" v-model.number="s.daily_cap" style="width:120px" :disabled="!isRoot" /><span>元</span></label>
    <h2>自动发表总开关</h2>
    <p class="muted">暂停后全平台都不会自动发表，用户等待中的文章会一直等，恢复后继续。<span v-if="pub && !pub.enabled">（服务器没有开启自动发表功能 V2_AUTOPUBLISH，现在什么都不会发。）</span></p>
    <div v-if="pub" class="row"><button :class="pub.paused ? 'primary' : 'danger'" @click="togglePub">{{ pub.paused ? '恢复全平台自动发表' : '暂停全平台自动发表' }}</button>
      <span class="muted">{{ pub.paused ? '现在：已暂停' : '现在：正常' }}</span></div>
    <h2>邀请奖励</h2>
    <p class="muted">用户邀请的好友注册、绑定公众号并写出第一篇后，邀请人得到这么多篇额外写作次数（当天额度用完后使用）。同一网络或设备 30 天内重复注册的不算。填 0 表示不开启（默认）。只有超级管理员可以修改。</p>
    <label class="row"><span>每个有效邀请奖励</span><input type="number" min="0" max="100" v-model.number="s.invite_bonus" style="width:90px" :disabled="!isRoot" /><span>篇</span></label>
    <h2>联系方式</h2>
    <p class="muted">显示在公开的价格页，用户想开通套餐时联系你。比如：微信 xxx，或邮箱。</p>
    <input v-model="s.contact" maxlength="200" placeholder="微信 / 电话 / 邮箱" />
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
const pub = ref(null)
async function togglePub() { try { pub.value = await put('/api/admin/publish-pause', { paused: !pub.value.paused }) } catch (e) { msg.value = { ok: false, text: e.message } } }
const s = reactive({ auto_pause_after: 0, daily_cap: 0, invite_bonus: 0, contact: '' }), msg = ref(null), busy = ref(false)
async function save() {
  busy.value = true
  try { Object.assign(s, await put('/api/admin/settings', { auto_pause_after: s.auto_pause_after || 0, daily_cap: s.daily_cap || 0, invite_bonus: s.invite_bonus || 0, contact: s.contact || '' }))
    msg.value = { ok: true, text: (s.auto_pause_after ? `已保存：连续 ${s.auto_pause_after} 篇没人理就暂停` : '已保存：不自动暂停') + (s.daily_cap ? `；每天花费上限 ¥${s.daily_cap}` : '；花费不设上限') } }
  catch (e) { msg.value = { ok: false, text: e.message } } finally { busy.value = false }
}
onMounted(async () => { get('/api/admin/publish-pause').then((p) => { pub.value = p }).catch(() => {}); try { Object.assign(s, await get('/api/admin/settings')) } catch (e) { msg.value = { ok: false, text: e.message } } })
</script>
