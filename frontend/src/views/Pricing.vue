<template>
  <div class="stack">
    <h1 style="font-size: 32px">定价</h1>
    <p v-if="!pay.enabled" class="muted">价格和额度来自后台套餐配置。在线支付即将开通，现在不能在这里付款。付费档位仍由管理员在确认收款后手工开通。</p>
    <p v-else class="muted">价格和额度来自后台套餐配置。支付成功后档位会开通。同周期升级按剩余天数补差。首次购买且 7 天内未使用，可在会员页申请全额退款并回到免费档；款项仍需在商户平台手工退回。管理员标记退款不会自动改档位。</p>
    <p v-if="error" class="banner">{{ error }}</p>
    <p v-if="message" class="banner info">{{ message }}</p>
    <div class="price-grid">
      <article v-for="plan in plans" :key="plan.tier" class="card stack">
        <strong>{{ plan.name }}</strong>
        <p>{{ plan.price_label }}</p>
        <p class="muted">每天 {{ plan.daily_quota }} 篇 · {{ periodLabel(plan) }} {{ plan.period_quota }} 篇</p>
        <p class="muted">{{ plan.max_accounts }} 个公众号 · {{ plan.max_plans }} 个方向 · 单篇上限 {{ plan.max_words }} 字</p>
        <p class="muted">{{ plan.summary }}</p>
        <button v-if="!pay.enabled || plan.tier === 'free'" class="btn" type="button" disabled>即将开通</button>
        <div v-else class="stack">
          <label v-if="pay.providers.length > 1">支付方式
            <select v-model="provider">
              <option v-for="item in pay.providers" :key="item" :value="item">{{ providerLabel(item) }}</option>
            </select>
          </label>
          <label v-if="provider === 'alipay' && pay.alipay_page">支付宝方式
            <select v-model="channel">
              <option value="qr">当面付扫码</option>
              <option value="page">电脑网站支付</option>
            </select>
          </label>
          <div class="row" v-if="loggedIn">
            <button v-if="plan.tier !== 'lifetime'" class="btn" type="button" @click="buy(plan.tier, 'month')">月付</button>
            <button v-if="plan.tier !== 'lifetime'" class="btn" type="button" @click="buy(plan.tier, 'year')">年付</button>
            <button v-if="plan.tier === 'lifetime'" class="btn" type="button" @click="buy(plan.tier, 'once')">购买</button>
          </div>
          <router-link v-else class="btn" to="/login?next=/pricing">登录后购买</router-link>
        </div>
      </article>
    </div>
    <section v-if="pay.enabled" class="card stack">
      <h2>加量包</h2>
      <p>{{ (pay.pack_articles || 10) }} 篇，{{ yuan(pay.pack_price_cents || 1900) }} 元，90 天内有效。订阅当月额度用完后才扣加量包，每天篇数上限仍然有效。</p>
      <button v-if="loggedIn" class="btn" type="button" @click="buy('pack', 'pack')">购买加量包</button>
      <router-link v-else class="btn" to="/login?next=/pricing">登录后购买</router-link>
    </section>
    <section v-if="order" class="card stack">
      <h2>订单 {{ order.out_trade_no }}</h2>
      <p>{{ order.message }}</p>
      <p class="muted">金额 {{ yuan(order.amount_cents) }} 元</p>
      <label v-if="order.code_url">收款链接
        <input :value="order.code_url" readonly />
      </label>
      <p v-if="order.pay_url"><a :href="order.pay_url">打开支付宝付款</a></p>
      <div class="row">
        <button v-if="pay.sandbox" class="btn" type="button" @click="completeSandbox">模拟支付成功</button>
        <button class="btn-ghost" type="button" @click="refreshOrder">查看支付结果</button>
      </div>
    </section>
    <p class="muted">不承诺阅读量、关注增长或收益。没开启认证号自动发布时，文章进入草稿箱后由你本人审阅再发布。</p>
  </div>
</template>

<script setup>
import { onMounted, ref } from "vue";
import { api, getToken } from "../api";

const plans = ref([]);
const pay = ref({ enabled: false, sandbox: false, providers: [], alipay_page: false });
const provider = ref("");
const channel = ref("qr");
const order = ref(null);
const error = ref("");
const message = ref("");
const loggedIn = ref(false);

function periodLabel(plan) {
  return plan.period_unit === "year" ? "每周年" : "每月";
}

function providerLabel(item) {
  if (item === "wechat") return "微信支付";
  if (item === "alipay") return "支付宝";
  if (item === "sandbox") return "模拟支付";
  return item;
}

function yuan(cents) {
  const value = Number(cents) || 0;
  return (value / 100).toFixed(2);
}

async function buy(tier, period) {
  error.value = "";
  message.value = "";
  try {
    order.value = await api("/api/billing/checkout", {
      method: "POST",
      body: { tier, period, provider: provider.value, channel: channel.value },
    });
  } catch (err) {
    error.value = err.message;
  }
}

async function completeSandbox() {
  error.value = "";
  try {
    await api("/api/billing/sandbox/complete", { method: "POST", body: { out_trade_no: order.value.out_trade_no } });
    message.value = "模拟支付已入账，档位已开通。可在会员页查看。";
    await refreshOrder();
  } catch (err) {
    error.value = err.message;
  }
}

async function refreshOrder() {
  if (!order.value) return;
  error.value = "";
  try {
    const fresh = await api(`/api/billing/orders/${order.value.out_trade_no}`);
    if (fresh.status === "paid") message.value = "已收到付款，档位已开通。";
    else if (fresh.status === "pending") message.value = "还没收到支付结果。";
    order.value = { ...order.value, ...fresh };
  } catch (err) {
    error.value = err.message;
  }
}

onMounted(async () => {
  loggedIn.value = Boolean(getToken());
  const meta = await api("/api/meta");
  plans.value = meta.plans || [];
  pay.value = meta.pay || pay.value;
  if (!pay.value.providers) pay.value.providers = [];
  provider.value = pay.value.providers[0] || "";
});
</script>
