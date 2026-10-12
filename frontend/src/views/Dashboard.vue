<template>
  <div class="stack">
    <div class="split">
      <div>
        <h1 style="font-size: 36px">工作台</h1>
        <p class="muted">{{ quota.tier_label || "免费版" }} · {{ quota.state_label || "有效" }}</p>
      </div>
      <router-link class="btn" to="/app/plans">去设置方向</router-link>
    </div>
    <section class="cards">
      <article class="card">
        <p class="muted">今日剩余</p>
        <strong>{{ quota.daily_remaining ?? 0 }} 篇</strong>
        <p class="muted">已用 {{ quota.daily_used ?? 0 }} / {{ quota.daily_limit ?? 1 }}</p>
      </article>
      <article class="card">
        <p class="muted">{{ quota.period_label || "本月" }}剩余</p>
        <strong>{{ quota.period_remaining ?? 0 }} 篇</strong>
        <p class="muted">已用 {{ quota.period_used ?? 0 }} / {{ quota.period_limit ?? 10 }}</p>
      </article>
      <article class="card">
        <p class="muted">最近文章</p>
        <strong>{{ articles.length }} 篇</strong>
      </article>
    </section>
    <section class="card" v-if="!accounts.length">
      <h2>先绑定一个公众号</h2>
      <p>模拟模式下可以使用任意 AppID，走完检测后就能立即生成。</p>
      <router-link class="btn" to="/app/accounts">绑定公众号</router-link>
    </section>
    <section class="card" v-else-if="!plans.length">
      <h2>还没有写作方向</h2>
      <p>全托管只需要选一个方向、风格和更新节奏。</p>
      <router-link class="btn" to="/app/plans">创建计划</router-link>
    </section>
    <section class="card" v-else>
      <h2>进行中的计划</h2>
      <div v-for="plan in plans" :key="plan.id" class="split" style="padding: 8px 0">
        <div>
          <strong>{{ plan.direction }}</strong>
          <p class="muted">{{ plan.schedule_label }} · {{ plan.window_label }} · 下次 {{ formatTime(plan.next_run_at) }}</p>
        </div>
        <router-link class="btn-ghost" :to="'/app/plans'">管理</router-link>
      </div>
    </section>
  </div>
</template>

<script setup>
import { onMounted, ref } from "vue";
import { api, formatTime } from "../api";

const quota = ref({});
const accounts = ref([]);
const plans = ref([]);
const articles = ref([]);

onMounted(async () => {
  const [q, a, p, arts] = await Promise.all([
    api("/api/me/quota"),
    api("/api/accounts"),
    api("/api/plans"),
    api("/api/articles"),
  ]);
  quota.value = q;
  accounts.value = a;
  plans.value = p.filter((item) => item.status === "active");
  articles.value = arts;
});
</script>
