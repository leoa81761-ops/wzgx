<template>
  <form @submit.prevent="$emit('save')">
    <label class="f"><span>账号定位（写什么、给谁看，可写关键词）</span><textarea v-model="p.positioning" /></label>
    <div class="two">
      <label class="f"><span>人设（以谁的口吻写，第一人称还是第三人称）</span><input v-model="p.persona" /></label>
      <label class="f"><span>读者</span><input v-model="p.audience" /></label>
      <label class="f"><span>语气</span><input v-model="p.tone" placeholder="例如：亲切、专业、简洁" /></label>
      <label class="f"><span>不要写的内容</span><input v-model="p.taboos" /></label>
      <label class="f"><span>开头语（可不填）</span><input v-model="p.opening" /></label>
      <label class="f"><span>结尾语（可不填）</span><input v-model="p.ending" /></label>
    </div>
    <label class="f"><span>文末签名（可不填）</span><input v-model="p.signature" /></label>
    <label class="row" style="margin-bottom:6px"><input type="checkbox" v-model="p.self_review" /><span>写完后让模型自查一遍（更慢，更稳）</span></label>
    <label class="row" style="margin-bottom:6px"><input type="checkbox" v-model="p.agent_mode" /><span>智能体写作：先想清楚读者痛点、切入角度和能直接拿走的东西再写，写完对照你的设置和范文自查语气、改一遍（多两次模型调用，更慢）</span></label>
    <label class="row" style="margin-bottom:6px"><input type="checkbox" v-model="p.fact_check" /><span>写完后核对文中的数字、标准号、日期和文号：资料里找不到依据的会在预览里标黄给你看（不改文章，不拦推送）</span></label>
    <label class="row" style="margin-bottom:12px"><input type="checkbox" v-model="p.ai_label" /><span>文末标注“本文由 AI 辅助生成”</span></label>
    <button class="primary">保存设置</button>
  </form>
</template>
<script setup>
defineProps({ p: Object })
defineEmits(['save'])
</script>
