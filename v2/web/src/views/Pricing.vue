<template>
  <div class="pub">
    <PublicNav />
    <h1 style="text-align:center">价格</h1>
    <p class="muted" style="text-align:center">目前还没有在线支付。想开通或升级，请联系管理员，付款后由管理员为你开通。</p>
    <div v-if="err" class="msg">{{ err }}</div>
    <div class="tiers">
      <div v-for="t in tiers" :key="t.key" class="card tier">
        <h2>{{ t.name }}</h2>
        <div class="price">{{ t.price_text || (t.key === 'free' ? '免费' : '联系管理员') }}</div>
        <ul>
          <li>可绑定 {{ t.max_accounts }} 个公众号</li>
          <li>每天最多写 {{ t.daily_quota }} 篇</li>
          <li>{{ t.ai_images ? 'AI 生成封面和文中配图' : '使用默认封面' }}</li>
        </ul>
        <p class="muted">{{ t.description }}</p>
        <router-link v-if="t.key === 'free'" to="/register" class="btn-primary">免费注册</router-link>
      </div>
    </div>
    <div class="card" style="max-width:640px;margin:20px auto;text-align:center">
      <h3 style="margin-top:0">联系管理员开通</h3>
      <p>{{ contact || '请登录后在「我的」页面查看联系方式，或通过投诉与建议页留言。' }}</p>
      <p class="muted">开通时告诉管理员你的注册邮箱和想要的套餐即可。</p>
    </div>
    <PublicFoot />
  </div>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import { get } from '../api.js'
import PublicNav from './PublicNav.vue'
import PublicFoot from './PublicFoot.vue'
const tiers = ref([]), contact = ref(''), err = ref('')
onMounted(async () => { try { const r = await get('/api/site/plans'); tiers.value = r.tiers; contact.value = r.contact } catch (e) { err.value = e.message } })
</script>
