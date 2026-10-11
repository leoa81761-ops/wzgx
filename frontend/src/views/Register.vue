<template>
  <form class="card stack" style="max-width: 560px" @submit.prevent="submit">
    <h1 style="font-size: 32px">注册</h1>
    <p class="muted">本服务仅面向年满 18 周岁的成年人。请使用手机号或邮箱。</p>
    <p v-if="error" class="banner">{{ error }}</p>
    <label>手机号或邮箱<input v-model="account" autocomplete="username" required /></label>
    <label>密码（至少 8 位）<input v-model="password" type="password" minlength="8" autocomplete="new-password" required /></label>
    <label class="check">
      <input v-model="agree" type="checkbox" />
      <span>我已阅读并同意 <router-link to="/legal/user_agreement" target="_blank">《用户协议》</router-link> 和 <router-link to="/legal/privacy" target="_blank">《隐私政策》</router-link>，并确认本人已满 18 周岁。</span>
    </label>
    <button class="btn" type="submit" :disabled="!agree || password.length < 8 || loading">
      {{ loading ? "正在创建…" : "创建账号" }}
    </button>
  </form>
</template>

<script setup>
import { ref } from "vue";
import { useRouter } from "vue-router";
import { api } from "../api";
import { acceptLogin } from "../session";

const router = useRouter();
const account = ref("");
const password = ref("");
const agree = ref(false);
const error = ref("");
const loading = ref(false);

async function submit() {
  error.value = "";
  loading.value = true;
  try {
    const data = await api("/api/auth/register", {
      method: "POST",
      body: { account: account.value, password: password.value, agree: agree.value },
    });
    await acceptLogin(data);
    router.push("/app");
  } catch (err) {
    error.value = err.message;
  } finally {
    loading.value = false;
  }
}
</script>
