<template>
  <article class="card">
    <p v-if="error" class="banner">{{ error }}</p>
    <h1 style="font-size: 32px">{{ doc.title || "文档" }}</h1>
    <p class="muted" v-if="doc.version">版本 {{ doc.version }} · 生效日期 {{ doc.effective_date }}</p>
    <pre class="legal">{{ doc.content }}</pre>
    <p v-if="doc.versions && doc.versions.length > 1" class="muted">其他版本：
      <router-link v-for="item in doc.versions" :key="item.version" :to="`/legal/${doc.doc_type}?version=${encodeURIComponent(item.version)}`">{{ item.version }}（{{ item.status === 'active' ? '当前' : '旧版' }}） </router-link>
    </p>
  </article>
</template>

<script setup>
import { onMounted, ref, watch } from "vue";
import { useRoute } from "vue-router";
import { api } from "../api";

const route = useRoute();
const doc = ref({});
const error = ref("");

async function load() {
  error.value = "";
  try {
    const version = route.query.version ? `?version=${encodeURIComponent(route.query.version)}` : "";
    doc.value = await api(`/api/legal/${route.params.docType}${version}`);
  } catch (err) {
    doc.value = {};
    error.value = err.message;
  }
}

onMounted(load);
watch(() => [route.params.docType, route.query.version], load);
</script>

<style scoped>
.legal { white-space: pre-wrap; font-family: inherit; line-height: 1.7; }
</style>
