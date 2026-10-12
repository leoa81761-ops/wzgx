<template>
  <form class="card stack" style="max-width: 560px" @submit.prevent="submit">
    <h1 style="font-size: 32px">注册</h1>
    <p class="muted">本服务仅面向年满 18 周岁的成年人。请使用手机号或邮箱。</p>
    <p v-if="inviteCode" class="banner info">你正在通过邀请链接注册。奖励在观察期结束后才会计入邀请人的免费额度。</p>
    <p v-if="error" class="banner">{{ error }}</p>
    <label>手机号或邮箱<input v-model="account" autocomplete="username" required /></label>
    <label>密码（至少 8 位）<input v-model="password" type="password" minlength="8" autocomplete="new-password" required /></label>
    <div v-if="registerVerify" class="row">
      <label style="flex: 1">验证码<input v-model="code" inputmode="numeric" maxlength="6" autocomplete="one-time-code" required /></label>
      <button class="btn-ghost" type="button" :disabled="sending || !account" @click="sendCode">{{ sending ? "发送中…" : "获取验证码" }}</button>
    </div>
    <p v-if="registerVerify" class="muted">手机号收取短信验证码，邮箱收取邮件验证码。验证码 5 分钟内有效。</p>
    <label class="check">
      <input v-model="deviceConsent" type="checkbox" />
      <span>同意保存本机标识，仅用于邀请防刷。不勾选也可以注册，但无法用设备信息识别互刷。</span>
    </label>
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
import { onMounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { api } from "../api";
import { acceptLogin } from "../session";

const router = useRouter();
const route = useRoute();
const account = ref("");
const password = ref("");
const agree = ref(false);
const deviceConsent = ref(false);
const error = ref("");
const loading = ref(false);
const registerVerify = ref(false);
const code = ref("");
const sending = ref(false);
const inviteCode = ref(typeof route.query.invite === "string" ? route.query.invite : "");

function deviceID() {
  const key = "gonghao-device";
  let id = localStorage.getItem(key);
  if (!id) {
    id = crypto.randomUUID();
    localStorage.setItem(key, id);
  }
  return id;
}

onMounted(async () => {
  try {
    const options = await api("/api/auth/options");
    registerVerify.value = !!options.register_verify;
  } catch {
    registerVerify.value = false;
  }
});

async function sendCode() {
  error.value = "";
  sending.value = true;
  try {
    await api("/api/auth/register/code", { method: "POST", body: { account: account.value } });
  } catch (err) {
    error.value = err.message;
  } finally {
    sending.value = false;
  }
}

async function submit() {
  error.value = "";
  loading.value = true;
  try {
    const data = await api("/api/auth/register", {
      method: "POST",
      body: {
        account: account.value,
        password: password.value,
        agree: agree.value,
        invite_code: inviteCode.value,
        device_id: deviceConsent.value ? deviceID() : "",
        device_consent: deviceConsent.value,
        code: code.value,
      },
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
