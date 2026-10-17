<template>
  <div class="shell">
    <nav class="nav">
      <div class="brand">公号智写</div>
      <router-link to="/" active-class="" exact-active-class="router-link-active">首页</router-link>
      <router-link to="/accounts">公众号</router-link>
      <router-link to="/plans">写作计划</router-link>
      <router-link to="/articles">文章</router-link>
      <router-link to="/me">我的</router-link>
      <div class="foot">{{ session.me?.display_name || session.me?.email }}</div>
    </nav>
    <main class="main"><router-view /></main>
  </div>
</template>
<script setup>
import { session } from '../store.js'
</script>
