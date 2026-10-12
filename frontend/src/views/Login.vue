<template>
  <div class="grid-2">
    <form class="card stack" @submit.prevent="submit">
      <h1 style="font-size: 32px">登录</h1>
      <p v-if="error" class="banner">{{ error }}</p>
      <p v-if="notice" class="banner ok">{{ notice }}</p>
      <label>手机号或邮箱<input v-model="account" autocomplete="username" required /></label>
      <label>密码<input v-model="password" type="password" autocomplete="current-password" required /></label>
      <button class="btn" type="submit" :disabled="loading">{{ loading ? "正在登录…" : "登录" }}</button>
      <p class="muted">还没有账号？ <router-link to="/register">注册</router-link></p>
      <button class="btn-ghost" type="button" @click="showRecover = !showRecover">找回密码</button>
      <div v-if="showRecover" class="stack">
        <p class="muted">向已注册的手机号或邮箱发送验证码。手机找回需要配置短信，邮箱找回需要配置邮件。未注册的账号不会提示是否存在。</p>
        <label>手机号或邮箱<input v-model="recoverAccount" autocomplete="username" /></label>
        <button class="btn-ghost" type="button" @click="sendRecover">发送验证码</button>
        <label>验证码<input v-model="recoverCode" inputmode="numeric" /></label>
        <label>新密码<input v-model="recoverPassword" type="password" autocomplete="new-password" /></label>
        <button class="btn" type="button" @click="resetRecover">重置密码</button>
      </div>
      <button v-if="scanEnabled" class="btn-ghost" type="button" @click="startScan">微信扫码登录</button>
    </form>
    <div class="stack">
      <form v-if="smsEnabled" class="card stack" @submit.prevent="submitSMS">
        <h2>手机验证码登录</h2>
        <p class="muted">新手机号会直接注册。请先阅读协议。</p>
        <label>手机号<input v-model="phone" inputmode="numeric" autocomplete="tel" required /></label>
        <div class="row">
          <button class="btn-ghost" type="button" :disabled="sending" @click="sendCode">{{ sending ? "正在发送…" : "获取验证码" }}</button>
        </div>
        <label>验证码<input v-model="smsCode" inputmode="numeric" maxlength="6" autocomplete="one-time-code" required /></label>
        <label class="check">
          <input v-model="deviceConsent" type="checkbox" />
          <span>同意保存本机标识，仅用于邀请防刷。不勾选也可以登录。</span>
        </label>
        <label class="check">
          <input v-model="agree" type="checkbox" />
          <span>新手机号注册时，我已阅读并同意 <router-link to="/legal/user_agreement" target="_blank">《用户协议》</router-link> 和 <router-link to="/legal/privacy" target="_blank">《隐私政策》</router-link>，并确认本人已满 18 周岁。</span>
        </label>
        <button class="btn" type="submit" :disabled="loading">验证码登录</button>
      </form>
      <form v-if="ticket" class="card stack" @submit.prevent="exchangeTicket">
        <h2>微信登录</h2>
        <p v-if="needAgree" class="muted">第一次用这个微信登录需要同意协议。</p>
        <label v-if="needAgree" class="check">
          <input v-model="agree" type="checkbox" />
          <span>我已阅读并同意 <router-link to="/legal/user_agreement" target="_blank">《用户协议》</router-link> 和 <router-link to="/legal/privacy" target="_blank">《隐私政策》</router-link>，并确认本人已满 18 周岁。</span>
        </label>
        <button class="btn" type="submit" :disabled="loading || (needAgree && !agree)">继续</button>
      </form>
      <aside class="card">
        <h2>管理员</h2>
        <p class="muted">管理员账号由部署时的环境变量决定，不写在页面上。本地默认值见 README。</p>
      </aside>
    </div>
  </div>
</template>

<script setup>
import { onMounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { api } from "../api";
import { acceptLogin } from "../session";

const route = useRoute();
const router = useRouter();
const account = ref("");
const password = ref("");
const phone = ref("");
const smsCode = ref("");
const agree = ref(false);
const error = ref("");
const loading = ref(false);
const sending = ref(false);
const smsEnabled = ref(false);
const scanEnabled = ref(false);
const deviceConsent = ref(false);
const ticket = ref(typeof route.query.ticket === "string" ? route.query.ticket : "");
const needAgree = ref(false);
const inviteCode = typeof route.query.invite === "string" ? route.query.invite : "";
const showRecover = ref(false);
const recoverAccount = ref("");
const recoverCode = ref("");
const recoverPassword = ref("");
const notice = ref("");

function deviceID() {
  const key = "gonghao-device";
  let id = localStorage.getItem(key);
  if (!id) {
    id = crypto.randomUUID();
    localStorage.setItem(key, id);
  }
  return id;
}

async function sendRecover() {
  error.value = "";
  try {
    await api("/api/auth/recover/send", { method: "POST", body: { account: recoverAccount.value } });
    notice.value = "如果这个账号已注册，并且短信或邮件已配置，验证码会发到该账号。";
    account.value = recoverAccount.value;
  } catch (err) {
    error.value = err.message;
  }
}

async function resetRecover() {
  error.value = "";
  loading.value = true;
  try {
    await api("/api/auth/recover/reset", {
      method: "POST",
      body: { account: recoverAccount.value, code: recoverCode.value, password: recoverPassword.value },
    });
    account.value = recoverAccount.value;
    password.value = recoverPassword.value;
    showRecover.value = false;
    notice.value = "密码已重置，请登录。";
  } catch (err) {
    error.value = err.message;
  } finally {
    loading.value = false;
  }
}

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

async function sendCode() {
  error.value = "";
  sending.value = true;
  try {
    await api("/api/auth/sms/send", { method: "POST", body: { phone: phone.value } });
  } catch (err) {
    error.value = err.message;
  } finally {
    sending.value = false;
  }
}

async function submitSMS() {
  error.value = "";
  loading.value = true;
  try {
    const data = await api("/api/auth/sms/login", {
      method: "POST",
      body: {
        phone: phone.value,
        code: smsCode.value,
        agree: agree.value,
        invite_code: inviteCode,
        device_id: deviceConsent.value ? deviceID() : "",
        device_consent: deviceConsent.value,
      },
    });
    await acceptLogin(data);
    router.push(route.query.next || "/app");
  } catch (err) {
    error.value = err.message;
  } finally {
    loading.value = false;
  }
}

async function startScan() {
  error.value = "";
  try {
    const data = await api("/api/auth/wechat/start");
    window.location.href = data.url;
  } catch (err) {
    error.value = err.message;
  }
}

async function exchangeTicket() {
  error.value = "";
  loading.value = true;
  try {
    const data = await api("/api/auth/wechat/ticket", { method: "POST", body: { ticket: ticket.value, agree: agree.value } });
    if (data.need_agree) {
      needAgree.value = true;
      return;
    }
    await acceptLogin(data);
    router.push(route.query.next || "/app");
  } catch (err) {
    error.value = err.message;
  } finally {
    loading.value = false;
  }
}

onMounted(async () => {
  if (typeof route.query.error === "string" && route.query.error) error.value = route.query.error;
  try {
    const options = await api("/api/auth/options");
    smsEnabled.value = !!options.sms;
    scanEnabled.value = !!options.wechat_scan;
  } catch (err) {
    error.value = err.message;
  }
  if (ticket.value) await exchangeTicket();
});
</script>
