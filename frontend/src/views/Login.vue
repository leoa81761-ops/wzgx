<template>
  <div class="grid-2">
    <form class="card stack" @submit.prevent="submit">
      <h1 style="font-size: 32px">登录</h1>
      <p v-if="error" class="banner">{{ error }}</p>
      <label>手机号或邮箱<input v-model="account" autocomplete="username" required /></label>
      <label>密码<input v-model="password" type="password" autocomplete="current-password" required /></label>
      <button class="btn" type="submit" :disabled="loading">{{ loading ? "正在登录…" : "登录" }}</button>
      <p class="muted">还没有账号？ <router-link to="/register">注册</router-link></p>
      <button class="btn-ghost" type="button" disabled>微信扫码登录（即将开放）</button>
    </form>
    <aside class="card">
      <h2>管理员</h2>
      <p class="muted">管理员账号由部署时的环境变量决定，不写在页面上。本地默认值见 README。</p>
    </aside>
  </div>
</template>

<script setup>
import { ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { api } from "../api";
import { acceptLogin } from "../session";

const route = useRoute();
const router = useRouter();
const account = ref("");
const password = ref("");
const error = ref("");
const loading = ref(false);

async function submit() {
  error.value = "";
  loading.value = true;
  try {
    const data = await api("/api/auth/login", { method: "POST", body: { account: account.value, password: password.value } });
    await acceptLogin(data);
    router.push(route.query.next || "/app");
  } catch (err) {
    error.value = err.message;
  } finally {
    loading.value = false;
  }
}
</script>
