<template>
  <h1>我的</h1>
  <div class="card">
    <h2 style="margin-top:0">Tavily 检索密钥（可选，不填也能正常写作）</h2>
    <p class="muted">Tavily 是一个联网搜索服务。填上你自己的 Tavily 密钥后，写作前除了平台自带的检索，还会再用它多搜一路资料，事实和出处会更充实；搜索费用记在你的 Tavily 账户上。
      没有的话可以到 tavily.com 免费注册获取，以 tvly- 开头。密钥只保存加密后的内容，这里只显示前后几位。</p>
    <p v-if="tv.has_key">当前：<span class="mono">{{ tv.mask }}</span></p>
    <p v-else class="muted">当前：未填写</p>
    <div class="row">
      <div style="flex:1;max-width:360px"><PwInput v-model="tavilyKey" trim placeholder="tvly-..." /></div>
      <button @click="saveKey" :disabled="!tavilyKey">{{ tv.has_key ? '替换' : '保存' }}</button>
    </div>
    <div v-if="keyMsg" class="msg" :class="{ ok: keyMsg.ok }">{{ keyMsg.text }}</div>
  </div>
  <div class="card">
    <h2 style="margin-top:0">修改密码</h2>
    <form @submit.prevent="changePw" style="max-width:360px">
      <label class="f"><span>当前密码</span><PwInput v-model="pw.old" autocomplete="current-password" required /></label>
      <label class="f"><span>新密码（至少 8 位）</span><PwInput v-model="pw.new" autocomplete="new-password" required /></label>
      <div v-if="pwMsg" class="msg">{{ pwMsg }}</div>
      <button :disabled="!pw.old || pw.new.length < 8">修改密码</button> <span class="muted">改完需要重新登录</span>
    </form>
  </div>
  <div class="card" v-if="inv">
    <h2 style="margin-top:0">邀请好友</h2>
    <p v-if="inv.bonus_per_invite" class="muted">好友通过你的链接注册，绑定公众号并写出第一篇文章后，你会得到 {{ inv.bonus_per_invite }} 篇额外写作次数（当天额度用完后自动使用）。同一网络或设备反复注册的不算。</p>
    <p v-else class="muted">平台暂时没有开启邀请奖励，邀请链接仍然可以分享给朋友。</p>
    <div class="row"><input readonly :value="inviteLink" style="max-width:420px" @focus="$event.target.select()" /><button @click="copy">{{ copied ? '已复制' : '复制链接' }}</button></div>
    <p>额外写作次数：<b>{{ inv.bonus_articles }}</b> 篇</p>
    <table v-if="inv.invites.length">
      <tr><th>好友</th><th>状态</th><th>奖励</th><th>注册时间</th></tr>
      <tr v-for="(x, i) in inv.invites" :key="i">
        <td>{{ x.email }}</td>
        <td>{{ { pending: '等好友绑定并写出第一篇', valid: '有效', rejected: '不计入' }[x.status] }}<span v-if="x.reason" class="muted">（{{ x.reason }}）</span></td>
        <td>{{ x.status === 'valid' ? '+' + x.bonus + ' 篇' : '—' }}</td><td class="nowrap">{{ fmtTime(x.created_at) }}</td>
      </tr>
    </table>
  </div>
  <div class="card">
    <h2 style="margin-top:0">导出我的数据</h2>
    <p class="muted">下载一个 JSON 文件，包含你的账号信息、公众号（不含 AppSecret）、写作设置、栏目、写作计划、全部文章、评价和同意记录。</p>
    <a class="btn" href="/api/me/export" download>下载数据</a>
  </div>
  <div class="card">
    <h2 style="margin-top:0">注销账号</h2>
    <p class="muted">注销后会立即退出登录，并在 <b>7 天</b>后删除你的公众号绑定、写作设置、计划和全部文章。7 天内重新登录就会取消注销。建议先导出数据。订单和同意记录按规定保留，但不再和你的邮箱关联。</p>
    <div v-if="!delOpen"><button class="danger" @click="delOpen = true">我要注销</button></div>
    <div v-else style="max-width:360px">
      <label class="f"><span>输入登录密码确认</span><PwInput v-model="delPw" autocomplete="current-password" /></label>
      <div v-if="delMsg" class="msg">{{ delMsg }}</div>
      <div class="row"><button class="danger" :disabled="!delPw || delBusy" @click="requestDelete">确认注销</button><button @click="delOpen = false; delPw = ''">算了</button></div>
    </div>
  </div>
  <button class="danger" @click="logout">退出登录</button>
</template>
<script setup>
import { ref, reactive, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { get, put, post, fmtTime } from '../api.js'
import { computed } from 'vue'
import { session } from '../store.js'
import PwInput from './PwInput.vue'
const tv = ref({}), tavilyKey = ref(''), pw = reactive({ old: '', new: '' }), keyMsg = ref(null), pwMsg = ref('')
const router = useRouter()
const inv = ref(null), copied = ref(false), delOpen = ref(false), delPw = ref(''), delMsg = ref(''), delBusy = ref(false)
const inviteLink = computed(() => inv.value ? `${location.origin}/register?invite=${inv.value.code}` : '')
async function copy() { try { await navigator.clipboard.writeText(inviteLink.value); copied.value = true } catch { /* select and copy by hand */ } }
async function requestDelete() {
  if (!confirm('确定注销？7 天内重新登录可以取消。')) return
  delBusy.value = true; delMsg.value = ''
  try {
    const r = await post('/api/me/delete', { password: delPw.value })
    session.me = null
    alert(`已申请注销，${fmtTime(r.delete_at)} 后删除数据。期间重新登录即可取消。`)
    router.push('/welcome')
  } catch (e) { delMsg.value = e.message } finally { delBusy.value = false }
}
async function saveKey() {
  try { await put('/api/me/tavily', { key: tavilyKey.value }); tavilyKey.value = ''; tv.value = await get('/api/me/tavily'); keyMsg.value = { ok: true, text: '已保存' } }
  catch (e) { keyMsg.value = { ok: false, text: e.message } }
}
async function changePw() {
  pwMsg.value = ''
  try { await post('/api/auth/password', { old_password: pw.old, new_password: pw.new }); session.me = null; router.push('/login') } catch (e) { pwMsg.value = e.message }
}
async function logout() { await post('/api/auth/logout').catch(() => {}); session.me = null; router.push('/login') }
onMounted(async () => { get('/api/me/invite').then((r) => { inv.value = r }).catch(() => {}); try { tv.value = await get('/api/me/tavily') } catch (e) { keyMsg.value = { ok: false, text: e.message } } })
</script>
