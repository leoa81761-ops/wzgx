<template>
  <div>
    <header class="topbar">
      <router-link class="brand" to="/"><span class="seal">稿</span> 公号智写</router-link>
      <nav class="nav">
        <template v-if="session.user">
          <router-link to="/app">工作台</router-link>
          <router-link to="/app/accounts">公众号</router-link>
          <router-link to="/app/plans">写作计划</router-link>
          <router-link to="/app/articles">文章</router-link>
          <router-link to="/app/membership">会员</router-link>
          <router-link v-if="session.user.is_admin" to="/admin">配置中心</router-link>
          <button class="text-btn" type="button" @click="onLogout">退出</button>
        </template>
        <template v-else>
          <router-link to="/tutorial">绑定教程</router-link>
          <router-link to="/login">登录</router-link>
          <router-link class="btn" to="/register">注册</router-link>
        </template>
      </nav>
    </header>
    <main class="main container">
      <router-view />
    </main>
    <footer class="footer">
      <nav>
        <router-link to="/legal/user_agreement">用户协议</router-link>
        <router-link to="/legal/privacy">隐私政策</router-link>
        <router-link to="/legal/disclaimer">免责声明</router-link>
        <router-link to="/legal/membership">会员与退款</router-link>
        <router-link to="/legal/content_rules">内容规范</router-link>
        <router-link to="/tutorial">绑定教程</router-link>
      </nav>
      <p>内测版本。协议文本待法务审核。本服务只把文章送入草稿箱，不代替你发布，也不承诺阅读或关注增长。</p>
    </footer>
  </div>
</template>

<script setup>
import { useRouter } from "vue-router";
import { logout, session } from "./session";

const router = useRouter();

function onLogout() {
  logout();
  router.push("/");
}
</script>
