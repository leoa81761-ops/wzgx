<template>
  <header class="pubnav">
    <router-link to="/welcome" class="brand">公号智写</router-link>
    <nav>
      <router-link to="/pricing">价格</router-link>
      <router-link to="/help">帮助</router-link>
      <router-link to="/complaint">投诉</router-link>
      <router-link v-if="session.me" :to="isStaff(session.me) ? '/admin' : '/'">进入控制台</router-link>
      <template v-else><router-link to="/login">登录</router-link><router-link to="/register" class="btn-primary">免费注册</router-link></template>
    </nav>
  </header>
</template>
<script setup>
import { session, isStaff } from '../store.js'
</script>
