<template>
  <div class="pub">
    <PublicNav />
    <section class="hero">
      <h1>公众号文章，每天自动写好放进草稿箱</h1>
      <p>绑定你的公众号，说清楚账号定位和读者，系统按计划选题、查资料、写作、排版，推送到公众号草稿箱。发不发表、改不改，都由你决定。</p>
      <div class="row" style="justify-content:center;gap:10px">
        <router-link to="/register" class="btn-primary big">免费注册试用</router-link>
        <router-link to="/pricing" class="btn big">看看价格</router-link>
      </div>
    </section>
    <section class="feat">
      <div class="card"><h3>按你的号来写</h3><p>账号定位、人设、读者、语气、开头结尾和签名，都按你的设置来，一个号一个样。</p></div>
      <div class="card"><h3>写作计划</h3><p>选好每周哪几天、几点写，到点自动开写；也可以随时点「现在写一篇」，或者自己给题目。</p></div>
      <div class="card"><h3>只进草稿箱</h3><p>写好的文章推送到公众号草稿箱，不会替你发表。你可以先在手机预览里看浅色和深色效果。</p></div>
      <div class="card"><h3>依据可查</h3><p>涉及国家标准、政府文件时注明出处，其他网上说法不当作依据。</p></div>
      <div class="card"><h3>选题可确认</h3><p>想自己把关的话，打开「选题先给我确认」，每篇先给你看题目，不满意就换。</p></div>
      <div class="card"><h3>排版和日历</h3><p>几套干净的排版模板任选；内容日历里一眼看到这个月写了什么、接下来写什么。</p></div>
    </section>
    <section class="steps card">
      <h2>三步开始</h2>
      <ol>
        <li>注册账号，在「公众号」里填入公众号的 AppID 和 AppSecret（<router-link to="/help">怎么找？</router-link>）。</li>
        <li>填写账号定位和栏目，设好写作计划。</li>
        <li>到点自动写好放进草稿箱，你在公众号后台检查后发表。</li>
      </ol>
    </section>
    <PublicFoot />
  </div>
</template>
<script setup>
import PublicNav from './PublicNav.vue'
import PublicFoot from './PublicFoot.vue'
</script>
