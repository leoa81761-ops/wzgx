<template>
  <div class="grid-2">
    <form class="card stack" @submit.prevent="submit">
      <h1 style="font-size: 32px">绑定公众号</h1>
      <p class="muted">请先看 <router-link to="/tutorial">绑定教程</router-link>。同一公众号同一时间只能绑在一个账号上。</p>
      <p v-if="error" class="banner">{{ error }}</p>
      <p v-if="message" class="banner ok">{{ message }}</p>
      <p v-if="mode === 'mock'" class="banner info">当前是模拟模式，填写任意 AppID 即可完成检测，不会请求微信。</p>
      <label>显示名称<input v-model="form.name" required placeholder="例如：周末厨房" /></label>
      <label>AppID<input v-model="form.app_id" required placeholder="wx_mock_demo" /></label>
      <label>AppSecret<input v-model="form.app_secret" type="password" required /></label>
      <label>主体类型
        <select v-model="form.subject_type">
          <option value="personal">个人</option>
          <option value="enterprise">企业</option>
        </select>
      </label>
      <label>账号类型
        <select v-model="form.service_type">
          <option value="subscription">订阅号</option>
          <option value="service">服务号</option>
        </select>
      </label>
      <label class="check" v-if="form.subject_type === 'enterprise'">
        <input v-model="form.verified" type="checkbox" />
        <span>已认证（可在写作计划里开启自动发布，默认关闭）</span>
      </label>
      <label class="check">
        <input v-model="form.consent" type="checkbox" />
        <span>我同意系统加密保存 AppID 与 AppSecret，用于草稿箱检测和写入。我有权管理这个公众号。可随时解绑。</span>
      </label>
      <button class="btn" :disabled="!form.consent || loading">{{ loading ? "正在检测…" : "绑定并检测权限" }}</button>
    </form>
    <div class="stack">
      <article v-for="account in accounts" :key="account.id" class="card stack">
        <div class="split">
          <strong>{{ account.name }}</strong>
          <span class="pill" :class="account.delivery_mode === 'draft' ? 'ok' : 'warn'">{{ account.delivery_label }}</span>
        </div>
        <p class="muted">{{ account.app_id }} · {{ account.type_label }}</p>
        <ul>
          <li v-for="check in account.permission_report.checks || []" :key="check.name">
            {{ check.ok ? "通过" : "未通过" }} · {{ check.detail }}
          </li>
        </ul>
        <div class="row">
          <button class="btn-ghost" type="button" @click="recheck(account)">重新检测</button>
          <button class="btn-ghost" type="button" @click="unbind(account)">解绑</button>
        </div>
      </article>
      <p v-if="!accounts.length" class="muted">还没有绑定公众号。</p>
    </div>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from "vue";
import { api } from "../api";

const accounts = ref([]);
const error = ref("");
const message = ref("");
const loading = ref(false);
const mode = ref("");
const form = reactive({
  name: "",
  app_id: "",
  app_secret: "",
  subject_type: "personal",
  service_type: "subscription",
  verified: false,
  consent: false,
});

async function load() {
  accounts.value = await api("/api/accounts");
  const meta = await api("/api/meta");
  mode.value = meta.wechat_mode;
}

async function submit() {
  error.value = "";
  message.value = "";
  loading.value = true;
  try {
    const saved = await api("/api/accounts", { method: "POST", body: { ...form } });
    message.value = saved.auto_publish_note || `已绑定，交付方式：${saved.delivery_label}`;
    form.app_secret = "";
    form.consent = false;
    await load();
  } catch (err) {
    error.value = err.message;
  } finally {
    loading.value = false;
  }
}

async function recheck(account) {
  error.value = "";
  try {
    await api(`/api/accounts/${account.id}/recheck`, { method: "POST", body: { app_secret: "" } });
    message.value = "检测完成";
    await load();
  } catch (err) {
    error.value = err.message;
    await load();
  }
}

async function unbind(account) {
  if (!window.confirm(`解绑「${account.name}」？凭证会被删除。`)) return;
  await api(`/api/accounts/${account.id}`, { method: "DELETE" });
  await load();
}

onMounted(load);
</script>
