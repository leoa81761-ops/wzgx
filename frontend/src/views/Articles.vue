<template>
  <div class="stack">
    <h1 style="font-size: 32px">文章</h1>
    <p v-if="error" class="banner">{{ error }}</p>
    <article v-for="article in articles" :key="article.id" class="card split">
      <div>
        <router-link :to="`/app/articles/${article.id}`"><strong>{{ article.title || "未命名" }}</strong></router-link>
        <p class="muted">{{ article.subtopic }} · {{ formatTime(article.created_at) }}</p>
        <p v-if="article.failure_reason" class="banner">{{ article.failure_reason }}</p>
        <p v-if="article.delivery_note" class="banner info">{{ article.delivery_note }}</p>
      </div>
      <span class="pill" :class="pillClass(article.status)">{{ article.status_label }}</span>
    </article>
    <p v-if="!articles.length" class="muted">还没有文章。在写作计划里点「立即生成」。</p>
  </div>
</template>

<script setup>
import { onMounted, onUnmounted, ref } from "vue";
import { api, formatTime } from "../api";

const articles = ref([]);
const error = ref("");
let timer = 0;

function pillClass(status) {
  if (status === "in_draft") return "ok";
  if (status === "exported") return "warn";
  if (status === "failed") return "bad";
  return "";
}

async function load() {
  try {
    articles.value = await api("/api/articles");
  } catch (err) {
    error.value = err.message;
  }
}

onMounted(() => {
  load();
  timer = window.setInterval(load, 8000);
});
onUnmounted(() => window.clearInterval(timer));
</script>
