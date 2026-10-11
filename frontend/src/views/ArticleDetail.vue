<template>
  <div class="stack" v-if="article">
    <div class="split">
      <div>
        <p class="muted">{{ article.subtopic }} · {{ article.column_type }}</p>
        <h1 style="font-size: 32px">{{ article.title }}</h1>
      </div>
      <span class="pill" :class="article.status === 'in_draft' || article.status === 'published' ? 'ok' : article.status === 'failed' || article.status === 'publish_failed' ? 'bad' : 'warn'">{{ article.status_label }}</span>
    </div>
    <p v-if="article.status === 'generating'" class="banner info">正在生成，完成后会自动刷新。模型较慢时可能要几分钟。</p>
    <p v-if="article.failure_reason" class="banner">{{ article.failure_reason }}</p>
    <p v-if="article.delivery_note" class="banner info">{{ article.delivery_note }}</p>
    <p v-if="article.risk_level && article.risk_level !== 'low'" class="banner">{{ article.risk_label }}</p>
    <p v-if="article.draft_media_id" class="muted">草稿 media_id：{{ article.draft_media_id }}</p>
    <p v-if="article.article_url" class="muted">发布链接：{{ article.article_url }}</p>
    <p v-if="themeNote" class="muted">{{ themeNote }}</p>
    <label>排版预览
      <select v-model="previewTemplate" @change="load(dark ? 'dark' : 'light')">
        <option v-for="item in templates" :key="item.key" :value="item.key">{{ item.name }}</option>
      </select>
    </label>
    <div class="row">
      <button class="btn" type="button" :disabled="loading" @click="regenerate">{{ loading ? "正在重写…" : "重新生成" }}</button>
      <button class="btn-ghost" type="button" @click="copyHtml">复制 HTML</button>
      <button class="btn-ghost" type="button" @click="downloadHtml">下载 HTML</button>
      <button class="btn-ghost" type="button" @click="toggleTheme">{{ dark ? "浅色预览" : "暗色预览" }}</button>
      <button class="btn-ghost" type="button" @click="reread">回读草稿</button>
      <button class="btn-ghost" type="button" @click="feedback('published')">已发布</button>
      <button class="btn-ghost" type="button" @click="feedback('edited_published')">修改后发布</button>
      <button class="btn-ghost" type="button" @click="feedback('discarded')">弃用</button>
    </div>
    <p v-if="feedbackNote" class="banner ok">{{ feedbackNote }}</p>
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
import { computed, onMounted, onUnmounted, ref } from "vue";
import { useRoute } from "vue-router";
import { api, getToken } from "../api";

const route = useRoute();
const article = ref(null);
const error = ref("");
const copied = ref(false);
const loading = ref(false);
const dark = ref(false);
const themeNote = ref("");
const feedbackNote = ref("");
const templates = ref([]);
const previewTemplate = ref("");

let pollTimer = 0;

const frame = computed(() => {
  const html = article.value?.body_html || "";
  return `<!DOCTYPE html><html><head><meta charset="utf-8"></head><body style="margin:0">${html}</body></html>`;
});

function schedulePoll() {
  window.clearTimeout(pollTimer);
  if (article.value?.status !== "generating") return;
  pollTimer = window.setTimeout(async () => {
    await load(dark.value ? "dark" : "light");
    schedulePoll();
  }, 2000);
}

async function load(theme = "light") {
  const query = new URLSearchParams();
  query.set("theme", theme);
  if (previewTemplate.value) query.set("template", previewTemplate.value);
  const data = await api(`/api/articles/${route.params.id}?${query}`);
  article.value = data;
  themeNote.value = data.theme_note || "";
  if (!previewTemplate.value && data.template_key) previewTemplate.value = data.template_key;
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
    schedulePoll();
  } catch (err) {
    error.value = err.message;
  } finally {
    loading.value = false;
  }
}

async function reread() {
  error.value = "";
  feedbackNote.value = "";
  try {
    const data = await api(`/api/articles/${route.params.id}/reread`, { method: "POST" });
    feedbackNote.value = data.note_text || "已记录回读结果";
  } catch (err) {
    error.value = err.message;
  }
}

async function feedback(kind) {
  error.value = "";
  feedbackNote.value = "";
  try {
    await api(`/api/articles/${route.params.id}/feedback`, { method: "POST", body: { kind, score: 0, note: "" } });
    const labels = { published: "已记下：已发布", edited_published: "已记下：修改后发布", discarded: "已记下：弃用" };
    feedbackNote.value = labels[kind];
  } catch (err) {
    error.value = err.message;
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

onMounted(async () => {
  const meta = await api("/api/meta");
  templates.value = meta.templates || [];
  await load();
  schedulePoll();
});
onUnmounted(() => window.clearTimeout(pollTimer));
</script>
