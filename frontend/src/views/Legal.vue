<template>
  <article class="card">
    <p v-if="error" class="banner">{{ error }}</p>
    <h1 style="font-size: 32px">{{ doc.title || "文档" }}</h1>
    <p class="muted" v-if="doc.version">版本 {{ doc.version }} · 生效日期 {{ doc.effective_date }}</p>
    <pre class="legal">{{ doc.content }}</pre>
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
    doc.value = await api(`/api/legal/${route.params.docType}`);
  } catch (err) {
    doc.value = {};
    error.value = err.message;
  }
}

onMounted(load);
watch(() => route.params.docType, load);
</script>

<style scoped>
.legal { white-space: pre-wrap; font-family: inherit; line-height: 1.7; }
</style>
