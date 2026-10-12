<template>
  <div class="stack">
    <section class="grid-2">
      <div>
        <p class="muted">内测 · 设定方向后自动成稿</p>
        <h1>把写作方向交给系统，文章进草稿箱再由你审阅。</h1>
        <p class="lede">选择一个方向和更新节奏。系统按周期选题、写稿，并尝试写入公众号草稿箱。发布仍由你在手机「公众号助手」里完成。</p>
        <div class="row" style="margin-top: 18px">
          <router-link class="btn" to="/register">注册并试用</router-link>
          <router-link class="btn-ghost" to="/tutorial">查看绑定教程</router-link>
          <router-link class="btn-ghost" to="/pricing">查看定价</router-link>
        </div>
      </div>
      <div class="card stack">
        <h2>三步开始</h2>
        <div class="steps">
          <div class="step"><span class="num">1</span><div>注册时勾选同意协议。本服务仅面向成年人。</div></div>
          <div class="step"><span class="num">2</span><div>绑定公众号并选择主体类型。检测通过后写入草稿，否则改为导出 HTML。</div></div>
          <div class="step"><span class="num">3</span><div>只选一个方向。系统轮换子话题，避免短时间内重复同一主题。</div></div>
        </div>
      </div>
    </section>
    <section>
      <h2>会员额度</h2>
      <p class="muted">价格与额度来自后台套餐配置。当前不提供在线支付，付费档位由管理员手工开通。</p>
      <div class="price-grid">
        <article v-for="plan in plans" :key="plan.tier" class="card">
          <strong>{{ plan.name }}</strong>
          <p>{{ plan.price_label }}</p>
          <p class="muted">每天 {{ plan.daily_quota }} 篇 · {{ periodLabel(plan) }} {{ plan.period_quota }} 篇</p>
          <p class="muted">{{ plan.max_accounts }} 个公众号 · 单篇上限 {{ plan.max_words }} 字</p>
          <button v-if="!pay.enabled" class="btn" type="button" disabled>即将开通</button>
          <router-link v-else class="btn" to="/pricing">查看定价并购买</router-link>
        </article>
      </div>
    </section>
    <section class="card">
      <h2>常见问题</h2>
      <p><strong>文章会直接发给读者吗？</strong> 个人号和未认证号只进入草稿箱。企业认证号要在写作计划里单独开启自动发布，默认关闭，并且只提交低风险稿。失败时稿件留在草稿箱，并提示到公众号助手查看。</p>
      <p><strong>需要自己填写模型密钥吗？</strong> 不需要。模型地址和密钥由管理员在配置中心填写，用户看不到密钥。</p>
      <p><strong>免费版有什么限制？</strong> 每天 1 篇、每月 10 篇、1 个公众号、1 个方向，并安排在夜间低峰生成。每天篇数按账号合计，可以分配到已绑定的公众号。</p>
      <p><strong>页面上的协议是正式文本吗？</strong> 不是。文首标有「待法务审核」，不能当作正式法律文件。</p>
    </section>
  </div>
</template>

<script setup>
import { onMounted, ref } from "vue";
import { api } from "../api";

const plans = ref([]);
const pay = ref({ enabled: false });

function periodLabel(plan) {
  return plan.period_unit === "year" ? "每周年" : "每月";
}

onMounted(async () => {
  try {
    const meta = await api("/api/meta");
    plans.value = meta.plans || [];
    pay.value = meta.pay || { enabled: false };
  } catch {
    plans.value = [];
  }
});
</script>
