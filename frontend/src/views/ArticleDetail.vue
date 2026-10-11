<template>
  <div class="stack" v-if="article">
    <div class="split">
      <div>
        <p class="muted">{{ article.subtopic }} · {{ article.column_type }}</p>
        <h1 style="font-size: 32px">{{ article.title }}</h1>
      </div>
      <span class="pill" :class="article.status === 'in_draft' ? 'ok' : article.status === 'failed' ? 'bad' : 'warn'">{{ article.status_label }}</span>
    </div>
    <p v-if="article.failure_reason" class="banner">{{ article.failure_reason }}</p>
    <p v-if="article.delivery_note" class="banner info">{{ article.delivery_note }}</p>
    <p v-if="article.draft_media_id" class="muted">草稿 media_id：{{ article.draft_media_id }}</p>
    <p v-if="themeNote" class="muted">{{ themeNote }}</p>
    <div class="row">
      <button class="btn" type="button" :disabled="loading" @click="regenerate">{{ loading ? "正在重写…" : "重新生成" }}</button>
      <button class="btn-ghost" type="button" @click="copyHtml">复制 HTML</button>
      <button class="btn-ghost" type="button" @click="downloadHtml">下载 HTML</button>
      <button class="btn-ghost" type="button" @click="toggleTheme">{{ dark ? "浅色预览" : "暗色预览" }}</button>
    </div>
    <p v-if="error" class="banner">{{ error }}</p>
    <p v-if="copied" class="banner ok">已复制</p>
    <div v-if="article.alt_titles?.length" class="card">
      <strong>备选标题</strong>
      <p v-for="title in article.alt_titles" :key="title">{{ title }}</p>
    </div>
    <div class="phone" :class="{ dark }">
      <iframe title="文章预览" :srcdoc="frame"></iframe>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from "vue";
import { useRoute } from "vue-router";
import { api, getToken } from "../api";

const route = useRoute();
const article = ref(null);
const error = ref("");
const copied = ref(false);
const loading = ref(false);
const dark = ref(false);
const themeNote = ref("");

const frame = computed(() => {
  const html = article.value?.body_html || "";
  return `<!DOCTYPE html><html><head><meta charset="utf-8"></head><body style="margin:0">${html}</body></html>`;
});

async function load(theme = "light") {
  const data = await api(`/api/articles/${route.params.id}?theme=${theme}`);
  article.value = data;
  themeNote.value = data.theme_note || "";
}

async function toggleTheme() {
  dark.value = !dark.value;
  await load(dark.value ? "dark" : "light");
}

async function regenerate() {
  error.value = "";
  loading.value = true;
  try {
    article.value = await api(`/api/articles/${route.params.id}/regenerate`, { method: "POST" });
    dark.value = false;
  } catch (err) {
    error.value = err.message;
  } finally {
    loading.value = false;
  }
}

async function downloadHtml() {
  const response = await fetch(`/api/articles/${article.value.id}/export`, {
    headers: { Authorization: `Bearer ${getToken()}` },
  });
  const blob = await response.blob();
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `article-${article.value.id}.html`;
  link.click();
  URL.revokeObjectURL(url);
}

async function copyHtml() {
  const html = article.value?.body_html || "";
  try {
    await navigator.clipboard.writeText(html);
  } catch {
    const area = document.createElement("textarea");
    area.value = html;
    document.body.appendChild(area);
    area.select();
    document.execCommand("copy");
    area.remove();
  }
  copied.value = true;
}

onMounted(() => load());
</script>
