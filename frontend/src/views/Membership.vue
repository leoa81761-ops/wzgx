<template>
  <div class="stack">
    <div>
      <h1 style="font-size: 32px">会员</h1>
      <p>当前档位：{{ quota.tier_label }}（{{ quota.state_label }}）</p>
      <p class="muted">今日 {{ quota.daily_used }} / {{ quota.daily_limit }} · {{ quota.period_label }} {{ quota.period_used }} / {{ quota.period_limit }}</p>
      <p v-if="quota.expires_at" class="muted">到期时间：{{ formatTime(quota.expires_at) }}。到期后有 7 天宽限期，宽限期内不能新增公众号和方向。</p>
    </div>
    <section class="card">
      <h2>开通方式</h2>
      <p>当前版本没有在线支付。付款后请联系管理员，由管理员在配置中心手工开通，并登记备注。</p>
      <p class="muted">{{ quota.summary }}</p>
    </section>
    <section class="card">
      <h2>同意记录</h2>
      <table>
        <thead><tr><th>文件</th><th>版本</th><th>时间</th></tr></thead>
        <tbody>
          <tr v-for="item in consents" :key="item.id">
            <td>{{ item.title }}</td>
            <td>{{ item.version }}</td>
            <td>{{ formatTime(item.created_at) }}</td>
          </tr>
        </tbody>
      </table>
    </section>
  </div>
</template>

<script setup>
import { onMounted, ref } from "vue";
import { api, formatTime } from "../api";

const quota = ref({});
const consents = ref([]);

onMounted(async () => {
  quota.value = await api("/api/me/quota");
  consents.value = await api("/api/me/consents");
});
</script>
