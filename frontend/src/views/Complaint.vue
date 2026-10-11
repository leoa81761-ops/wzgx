<template>
  <form class="card stack" style="max-width: 640px" @submit.prevent="submit">
    <h1 style="font-size: 32px">投诉</h1>
    <p class="muted">内容、隐私或账号问题都可以写在这里。已登录时会带上你的账号；未登录请留下邮箱或手机号。处理期限以页面说明为准，当前版本会由管理员在配置中心回复。</p>
    <p v-if="error" class="banner">{{ error }}</p>
    <p v-if="done" class="banner ok">已提交，编号 {{ done }}。</p>
    <label>类型
      <select v-model="kind">
        <option value="content">内容</option>
        <option value="privacy">隐私</option>
        <option value="account">账号</option>
        <option value="other">其他</option>
      </select>
    </label>
    <label>联系方式<input v-model="contact" placeholder="邮箱或手机号，已登录可留空" /></label>
    <label>说明<textarea v-model="content" rows="6" required></textarea></label>
    <button class="btn" :disabled="loading">{{ loading ? "提交中…" : "提交投诉" }}</button>
  </form>
</template>

<script setup>
import { ref } from "vue";
import { api } from "../api";

const kind = ref("content");
const contact = ref("");
const content = ref("");
const error = ref("");
const done = ref(0);
const loading = ref(false);

async function submit() {
  error.value = "";
  done.value = 0;
  loading.value = true;
  try {
    const data = await api("/api/complaints", {
      method: "POST",
      body: { kind: kind.value, contact: contact.value, content: content.value },
    });
    done.value = data.id;
    content.value = "";
  } catch (err) {
    error.value = err.message;
  } finally {
    loading.value = false;
  }
}
</script>
