<template>
  <div class="stack">
    <h1 style="font-size: 32px">通知</h1>
    <p class="muted">生成失败、高风险未推送，以及没有参考来源的稿会记在这里。若管理员配置了通知地址，同一条也会外发。</p>
    <p v-if="error" class="banner">{{ error }}</p>
    <p v-if="!items.length" class="muted">暂无通知。</p>
    <article v-for="item in items" :key="item.id" class="card stack">
      <div class="split">
        <strong>{{ item.title }}</strong>
        <span class="pill" :class="item.read ? '' : 'warn'">{{ item.read ? "已读" : "未读" }}</span>
      </div>
      <p>{{ item.body }}</p>
      <p class="muted">{{ formatTime(item.created_at) }}</p>
      <button v-if="!item.read" class="btn-ghost" type="button" @click="mark(item)">标为已读</button>
    </article>
  </div>
</template>

<script setup>
import { onMounted, ref } from "vue";
import { api, formatTime } from "../api";

const items = ref([]);
const error = ref("");

async function load() {
  const data = await api("/api/notifications");
  items.value = data.items || [];
}

async function mark(item) {
  error.value = "";
  try {
    await api(`/api/notifications/${item.id}/read`, { method: "POST" });
    item.read = true;
  } catch (err) {
    error.value = err.message;
  }
}

onMounted(load);
</script>
