<template>
  <div class="stack">
    <div class="split">
      <div>
        <h1>工作台</h1>
        <p class="muted">{{ quota.tier_label || "免费版" }} · {{ quota.state_label || "有效" }}</p>
      </div>
      <router-link class="btn" to="/app/start">给一个号做设置</router-link>
    </div>
    <p v-if="error" class="banner">{{ error }}</p>
    <section class="cards">
      <article class="card">
        <p class="muted">今天还剩</p>
        <strong>{{ quota.daily_remaining ?? 0 }} 篇</strong>
        <p class="muted">已用 {{ quota.daily_used ?? 0 }} / {{ quota.daily_limit ?? 1 }}，各号共用</p>
      </article>
      <article class="card">
        <p class="muted">{{ quota.period_label || "本月" }}还剩</p>
        <strong>{{ quota.period_remaining ?? 0 }} 篇</strong>
        <p class="muted">已用 {{ quota.period_used ?? 0 }} / {{ quota.period_limit ?? 10 }}</p>
      </article>
      <article class="card">
        <p class="muted">已绑定</p>
        <strong>{{ accounts.length }} 个号</strong>
        <p class="muted">{{ plans.length }} 个写作计划</p>
      </article>
    </section>
    <section class="card" v-if="!accounts.length">
      <h2>先绑定一个公众号</h2>
      <p>绑定之后选一个方向，看一段 200 字试读，再启动。每个号的设置分开保存。</p>
      <router-link class="btn" to="/app/start">按步骤开始</router-link>
    </section>
    <section v-else class="account-grid">
      <article v-for="card in cards" :key="card.account.id" class="card stack">
        <div class="split">
          <h2 class="account-head">{{ card.account.name }}</h2>
          <span class="pill" :class="statusClass(card)">{{ statusText(card) }}</span>
        </div>
        <p class="muted">{{ card.account.type_label }}</p>
        <p v-if="card.plan">
          {{ card.plan.direction }}
          · {{ card.plan.status === "active" ? "下次 " + formatTime(card.plan.next_run_at) : "启动后才会按点写" }}
        </p>
        <p v-if="card.plan && (card.plan.tone || card.plan.suggested_tone)" class="muted">
          {{ card.plan.tone || card.plan.suggested_tone }}<span v-if="card.plan.audience || card.plan.suggested_audience"> · {{ card.plan.audience || card.plan.suggested_audience }}</span>
        </p>
        <p v-else class="muted">这个号还没有写作计划。</p>
        <p v-if="card.plan && card.plan.pause_reason" class="muted">原因：{{ card.plan.pause_reason }}</p>
        <p class="muted">今天全账号还剩 {{ quota.daily_remaining ?? 0 }} 篇。<span v-if="card.plan">这个号计划每天 {{ card.plan.daily_count || 1 }} 篇。</span></p>
        <p v-if="card.article">
          最近一篇：<router-link :to="'/app/articles/' + card.article.id">{{ card.article.title || "未命名" }}</router-link>
        </p>
        <p v-else class="muted">还没有文章。</p>
        <p v-if="card.extra" class="muted">
          <router-link :to="'/app/plans?account=' + card.account.id">这个号还有 {{ card.extra }} 个计划</router-link>
        </p>
        <div class="row">
          <template v-if="card.plan">
            <button v-if="card.plan.status !== 'active'" class="btn" type="button" @click="resume(card.plan)">启动</button>
            <button v-else class="btn-ghost" type="button" @click="pause(card.plan)">暂停</button>
            <button class="btn" type="button" :disabled="busyId === card.plan.id" @click="generate(card.plan)">
              {{ busyId === card.plan.id ? "正在生成…" : "立即生成一篇" }}
            </button>
            <router-link class="btn-ghost" :to="'/app/plans/' + card.plan.id">编辑</router-link>
          </template>
          <router-link v-else class="btn" :to="'/app/start?account=' + card.account.id">按步骤开始</router-link>
        </div>
      </article>
    </section>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from "vue";
import { useRouter } from "vue-router";
import { api, formatTime } from "../api";

const router = useRouter();
const quota = ref({});
const accounts = ref([]);
const plans = ref([]);
const articles = ref([]);
const error = ref("");
const busyId = ref(0);

const cards = computed(() => accounts.value.map((account) => {
  const mine = plans.value.filter((plan) => plan.wx_account_id === account.id);
  const plan = mine.find((item) => item.status === "active") || mine[0] || null;
  const article = articles.value.find((item) => item.wx_account_id === account.id) || null;
  return { account, plan, article, extra: Math.max(mine.length - (plan ? 1 : 0), 0) };
}));

function statusText(card) {
  if (!card.plan) return "还没开始";
  return card.plan.status === "active" ? "运行中" : "已暂停";
}

function statusClass(card) {
  if (!card.plan) return "";
  return card.plan.status === "active" ? "ok" : "warn";
}

async function load() {
  const [q, a, p, arts] = await Promise.all([
    api("/api/me/quota"),
    api("/api/accounts"),
    api("/api/plans"),
    api("/api/articles"),
  ]);
  quota.value = q;
  accounts.value = a;
  plans.value = p;
  articles.value = arts;
}

async function resume(plan) {
  error.value = "";
  try {
    await api(`/api/plans/${plan.id}/resume`, { method: "POST" });
    await load();
  } catch (err) {
    error.value = err.message;
  }
}

async function pause(plan) {
  error.value = "";
  try {
    await api(`/api/plans/${plan.id}/pause`, { method: "POST" });
    await load();
  } catch (err) {
    error.value = err.message;
  }
}

async function generate(plan) {
  error.value = "";
  busyId.value = plan.id;
  try {
    const article = await api(`/api/plans/${plan.id}/generate`, { method: "POST" });
    router.push(`/app/articles/${article.id}`);
  } catch (err) {
    error.value = err.message;
  } finally {
    busyId.value = 0;
  }
}

onMounted(load);
</script>
