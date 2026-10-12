<template>
  <div class="stack">
    <h1 style="font-size: 32px">阅读数据</h1>
    <p v-if="error" class="banner">{{ error }}</p>
    <p v-if="message" class="banner ok">{{ message }}</p>
    <section class="card stack">
      <p class="muted">认证号可以在同意后拉取前一天的发表内容阅读、分享和新增关注。未认证号请手工填写。收藏字段目前的发表内容接口不返回，可以手工补。</p>
      <button class="btn" type="button" @click="agree">同意读取本号统计</button>
      <button class="btn-ghost" type="button" @click="pull">拉取昨天的微信数据</button>
    </section>
    <section class="card stack">
      <h2>阅读柱</h2>
      <p v-if="!series.length" class="muted">还没有阅读数。</p>
      <div v-for="item in series" :key="item.date" class="row">
        <span style="min-width: 7em">{{ item.date }}</span>
        <span :style="barStyle(item.reads)">{{ item.reads }}</span>
      </div>
    </section>
    <form class="card stack" @submit.prevent="saveManual">
      <h2>手工填写</h2>
      <label>日期<input v-model="form.date" required placeholder="2026-10-11" /></label>
      <label>阅读<input v-model.number="form.reads" type="number" min="0" /></label>
      <label>分享<input v-model.number="form.shares" type="number" min="0" /></label>
      <label>收藏<input v-model.number="form.favors" type="number" min="0" /></label>
      <label>新增关注<input v-model.number="form.follows" type="number" min="0" /></label>
      <button class="btn" type="submit">保存</button>
    </form>
    <section class="card stack">
      <h2>周报</h2>
      <p class="muted">普通版给出阅读和关注概览。高级版和买断版在样本够的时候按反馈调整选题权重，可以撤销。</p>
      <button class="btn" type="button" @click="report">生成周报</button>
      <article v-for="item in reports" :key="item.id">
        <p>{{ item.summary }}</p>
        <p class="muted">{{ item.advice }} {{ item.adjustment }}</p>
      </article>
    </section>
    <form v-if="templateEnabled" class="card stack" @submit.prevent="bindTemplate">
      <h2>服务号模板消息</h2>
      <p class="muted">填写你在本服务号下的 OpenID。没配服务号时这一栏不会出现。</p>
      <label>OpenID<input v-model="openid" required /></label>
      <button class="btn" type="submit">保存</button>
    </form>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from "vue";
import { useRoute } from "vue-router";
import { api } from "../api";

const route = useRoute();
const error = ref("");
const message = ref("");
const series = ref([]);
const reports = ref([]);
const templateEnabled = ref(false);
const openid = ref("");
const form = reactive({ date: "", reads: 0, shares: 0, favors: 0, follows: 0 });

function barStyle(reads) {
  const width = Math.max(8, Math.min(240, Number(reads) || 0));
  return { display: "inline-block", minWidth: width + "px", background: "#9c3b24", color: "#fff", padding: "2px 8px" };
}

async function load() {
  const data = await api("/api/accounts/" + route.params.id + "/stats");
  series.value = data.series || [];
  reports.value = await api("/api/accounts/" + route.params.id + "/reports");
  const meta = await api("/api/meta");
  templateEnabled.value = !!meta.template_enabled;
}

async function agree() {
  error.value = "";
  message.value = "";
  try {
    await api("/api/me/consents/analytics", { method: "POST", body: { agree: true } });
    message.value = "已同意读取本号统计";
  } catch (err) {
    error.value = err.message;
  }
}

async function pull() {
  error.value = "";
  message.value = "";
  try {
    const data = await api("/api/accounts/" + route.params.id + "/stats/pull", { method: "POST", body: {} });
    series.value = data.series || [];
    message.value = "已保存微信返回的统计";
  } catch (err) {
    error.value = err.message;
  }
}

async function saveManual() {
  error.value = "";
  message.value = "";
  try {
    await api("/api/accounts/" + route.params.id + "/stats", { method: "POST", body: form });
    message.value = "已保存手工数字";
    await load();
  } catch (err) {
    error.value = err.message;
  }
}

async function report() {
  error.value = "";
  message.value = "";
  try {
    await api("/api/accounts/" + route.params.id + "/reports", { method: "POST", body: {} });
    reports.value = await api("/api/accounts/" + route.params.id + "/reports");
  } catch (err) {
    error.value = err.message;
  }
}

async function bindTemplate() {
  error.value = "";
  message.value = "";
  try {
    await api("/api/me/template-bind", { method: "POST", body: { openid: openid.value } });
    message.value = "已保存 OpenID";
  } catch (err) {
    error.value = err.message;
  }
}

onMounted(() => {
  load().catch((err) => {
    error.value = err.message;
  });
});
</script>
