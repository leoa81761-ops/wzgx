<template>
  <div class="pub">
    <PublicNav />
    <div class="doc" style="max-width:640px">
      <h1>投诉与举报</h1>
      <p class="muted">发现本站生成的文章侵犯了你的权益、内容不实或违法违规，请告诉我们。我们会尽快核实处理。</p>
      <div v-if="done" class="msg ok">已收到，谢谢。我们会尽快处理{{ f.contact ? '，并通过你留的联系方式回复' : '' }}。</div>
      <div v-else class="card form">
        <label>类型
          <select v-model="f.kind">
            <option value="">请选择</option>
            <option value="infringe">侵权（抄袭、盗用图片等）</option>
            <option value="false">内容不实</option>
            <option value="illegal">违法违规</option>
            <option value="privacy">泄露个人信息</option>
            <option value="other">其他</option>
          </select>
        </label>
        <label>文章链接（可不填）<input v-model.trim="f.link" placeholder="https://mp.weixin.qq.com/s/…" maxlength="500" /></label>
        <label>情况说明<textarea v-model="f.content" rows="6" maxlength="2000" placeholder="请写清楚哪篇文章、哪里有问题；如果是侵权，请说明原作品出处"></textarea></label>
        <label>联系方式（可不填，方便我们回复）<input v-model.trim="f.contact" maxlength="100" placeholder="邮箱、手机号或微信号" /></label>
        <div v-if="err" class="msg">{{ err }}</div>
        <button class="primary" :disabled="busy || !f.kind || f.content.trim().length < 10" @click="submit">提交</button>
        <span v-if="f.content && f.content.trim().length < 10" class="muted" style="margin-left:8px">再多写几句（至少 10 个字）</span>
      </div>
    </div>
    <PublicFoot />
  </div>
</template>
<script setup>
import { reactive, ref } from 'vue'
import { post } from '../api.js'
import PublicNav from './PublicNav.vue'
import PublicFoot from './PublicFoot.vue'
const f = reactive({ kind: '', link: '', content: '', contact: '' }), err = ref(''), busy = ref(false), done = ref(false)
async function submit() {
  busy.value = true; err.value = ''
  try { await post('/api/complaints', f); done.value = true } catch (e) { err.value = e.message } finally { busy.value = false }
}
</script>
