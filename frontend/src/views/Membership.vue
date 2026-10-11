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
    <section class="card stack">
      <h2>数据与账号</h2>
      <p class="muted">导出的 JSON 含账号、计划和文章，不含 AppSecret。注销会立刻删除公众号密钥和账号数据，且不能恢复。</p>
      <div class="row">
        <button class="btn" type="button" @click="downloadExport">导出我的数据</button>
      </div>
      <label>注销账号（请输入密码）<input v-model="password" type="password" autocomplete="current-password" /></label>
      <button class="btn-ghost" type="button" @click="removeAccount">注销并删除数据</button>
      <p v-if="message" class="banner ok">{{ message }}</p>
      <p v-if="error" class="banner">{{ error }}</p>
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
import { useRouter } from "vue-router";
import { api, formatTime } from "../api";
import { logout } from "../session";

const router = useRouter();
const quota = ref({});
const consents = ref([]);
const password = ref("");
const message = ref("");
const error = ref("");

async function downloadExport() {
  error.value = "";
  const data = await api("/api/me/export");
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = "gonghao-export.json";
  link.click();
  URL.revokeObjectURL(url);
  message.value = "已开始下载";
}

async function removeAccount() {
  error.value = "";
  if (!window.confirm("注销后无法恢复，公众号密钥会立即删除。继续？")) return;
  try {
    await api("/api/me", { method: "DELETE", body: { password: password.value } });
    logout();
    router.push("/");
  } catch (err) {
    error.value = err.message;
  }
}

onMounted(async () => {
  quota.value = await api("/api/me/quota");
  consents.value = await api("/api/me/consents");
});
</script>
