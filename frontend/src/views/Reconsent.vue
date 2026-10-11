<template>
  <form class="card stack" style="max-width: 640px" @submit.prevent="submit">
    <h1 style="font-size: 32px">请确认更新后的协议</h1>
    <p class="muted">
      《用户协议》和《隐私政策》已更新到版本 {{ version || "当前版本" }}。继续使用前，请重新阅读并勾选同意。勾选不会默认选中。
    </p>
    <p v-if="error" class="banner">{{ error }}</p>
    <label class="check">
      <input v-model="agree" type="checkbox" />
      <span>
        我已阅读并同意更新后的
        <router-link to="/legal/user_agreement" target="_blank">《用户协议》</router-link>
        和
        <router-link to="/legal/privacy" target="_blank">《隐私政策》</router-link>。
      </span>
    </label>
    <button class="btn" type="submit" :disabled="!agree || loading">{{ loading ? "正在保存…" : "同意并继续" }}</button>
  </form>
</template>

<script setup>
import { onMounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { api } from "../api";
import { loadSession, session } from "../session";

const route = useRoute();
const router = useRouter();
const agree = ref(false);
const error = ref("");
const loading = ref(false);
const version = ref("");

onMounted(async () => {
  const status = await api("/api/me/consent-status");
  version.value = status.legal_version || "";
  if (session.user) session.user.needs_reconsent = !!status.needs_reconsent;
  if (!status.needs_reconsent) router.replace(route.query.next || "/app");
});

async function submit() {
  error.value = "";
  loading.value = true;
  try {
    await api("/api/me/reconsent", { method: "POST", body: { agree: true } });
    await loadSession();
    if (session.user) session.user.needs_reconsent = false;
    router.replace(typeof route.query.next === "string" ? route.query.next : "/app");
  } catch (err) {
    error.value = err.message;
  } finally {
    loading.value = false;
  }
}
</script>
