<template>
  <div class="stack">
    <h1 style="font-size: 32px">定价</h1>
    <p class="muted">价格和额度来自后台套餐配置。在线支付即将开通，现在不能在这里付款。付费档位仍由管理员在确认收款后手工开通。</p>
    <div class="price-grid">
      <article v-for="plan in plans" :key="plan.tier" class="card stack">
        <strong>{{ plan.name }}</strong>
        <p>{{ plan.price_label }}</p>
        <p class="muted">每天 {{ plan.daily_quota }} 篇 · {{ periodLabel(plan) }} {{ plan.period_quota }} 篇</p>
        <p class="muted">{{ plan.max_accounts }} 个公众号 · {{ plan.max_plans }} 个方向 · 单篇上限 {{ plan.max_words }} 字</p>
        <p class="muted">{{ plan.summary }}</p>
        <button class="btn" type="button" disabled>即将开通</button>
      </article>
    </div>
    <p class="muted">不承诺阅读量、关注增长或收益。文章进入草稿箱后，由你本人审阅再发布。</p>
  </div>
</template>

<script setup>
import { onMounted, ref } from "vue";
import { api } from "../api";

const plans = ref([]);

function periodLabel(plan) {
  return plan.period_unit === "year" ? "每周年" : "每月";
}

onMounted(async () => {
  const meta = await api("/api/meta");
  plans.value = meta.plans || [];
});
</script>
