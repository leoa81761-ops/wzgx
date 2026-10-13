<template>
  <div class="stack">
    <div>
      <h1 style="font-size: 32px">会员</h1>
      <p>当前档位：{{ quota.tier_label }}（{{ quota.state_label }}）</p>
      <p v-if="quota.unlimited" class="muted">测试账号不限额。已用 {{ quota.daily_used ?? 0 }} 篇。</p>
      <p v-else class="muted">今日剩余 {{ quota.daily_remaining ?? 0 }} 篇（已用 {{ quota.daily_used }} / {{ quota.daily_limit }}）· {{ quota.period_label }}剩余 {{ quota.period_remaining ?? 0 }} 篇（已用 {{ quota.period_used }} / {{ quota.period_limit }}）</p>
      <p v-if="quota.pack_remaining" class="muted">加量包剩余 {{ quota.pack_remaining }} 篇。订阅额度用完后才扣加量包，每天篇数上限仍然有效，加量包 90 天内有效。</p>
      <p v-if="quota.expires_at" class="muted">到期时间：{{ formatTime(quota.expires_at) }}。到期前 7 天、3 天、1 天会在通知里提醒。到期后有 7 天宽限期，宽限期内不能新增公众号和方向。</p>
      <p v-if="quota.pending_tier" class="muted">到期后改为{{ quota.pending_label }}{{ quota.applies_at ? `（${formatTime(quota.applies_at)}）` : "" }}。</p>
    </div>
    <section class="card stack">
      <h2>邀请奖励</h2>
      <p v-if="referral.code">你的邀请链接：{{ inviteLink }}</p>
      <p class="muted">免费用户每有 1 个有效邀请，当月多 {{ referral.reward_per || 2 }} 篇，最多加 {{ referral.reward_cap || 20 }} 篇，免费额度不超过 {{ referral.month_max || 30 }} 篇。付费期间不加，降回免费后恢复。有效邀请需要新用户、非虚拟号、公众号此前没被绑定过、写出 {{ referral.drafts_need || 3 }} 篇，并观察 {{ referral.observe_days || 7 }} 天。同设备或同一 IP 段集中注册不计入。每天最多 {{ referral.daily_cap || 3 }} 个有效邀请。</p>
      <p class="muted">已生效 {{ referral.valid_count || 0 }}，观察中 {{ referral.pending_count || 0 }}，待审核 {{ referral.frozen_count || 0 }}，已撤销 {{ referral.revoked_count || 0 }}。当前加篇 {{ referral.bonus || 0 }}。</p>
    </section>
    <section class="card">
      <h2>开通方式</h2>
      <p v-if="pay.enabled">可以在定价页开通、续费或购买加量包。同周期升级按剩余天数补差，到期时间不变。首次购买且 7 天内没有使用付费额度，可以在下面申请全额退款，档位会回到免费。钱要在微信或支付宝商户平台手工退回。</p>
      <p v-else>在线支付还没配置。付款后请联系管理员，由管理员在配置中心手工开通，并登记备注。</p>
      <p class="muted">{{ quota.summary }}</p>
      <button v-if="pay.enabled" class="btn-ghost" type="button" @click="askRefund">申请 7 天内未使用退款</button>
    </section>
    <section class="card stack">
      <h2>我的 Tavily 检索</h2>
      <p class="muted">填了自己的密钥后，生成先用你的 Tavily，再用站内 SearXNG 和免费搜索。这把钥匙只属于你，不会计入平台额度。密钥加密保存，页面上只显示末四位。</p>
      <p v-if="tavily.notice" class="banner">{{ tavily.notice }}</p>
      <p v-if="tavily.set" class="muted">已保存 {{ tavily.mask }}</p>
      <label>Tavily API Key<input v-model="tavilyKey" type="password" autocomplete="off" :placeholder="tavily.set ? '留空则不修改，填写则替换' : '粘贴你的密钥'" /></label>
      <div class="row">
        <button class="btn" type="button" @click="saveTavily">{{ tavily.set ? "替换密钥" : "保存密钥" }}</button>
        <button class="btn-ghost" type="button" :disabled="!tavily.set" @click="testTavily">测试连通</button>
        <button class="btn-ghost" type="button" :disabled="!tavily.set" @click="deleteTavily">删除密钥</button>
      </div>
    </section>
    <section class="card stack">
      <h2>数据与账号</h2>
      <p class="muted">导出的 JSON 含账号、计划和文章，不含 AppSecret。注销会立刻删除公众号密钥和账号数据，且不能恢复。</p>
      <div class="row">
        <button class="btn" type="button" @click="downloadExport">导出我的数据</button>
      </div>
      <h3>换绑手机</h3>
      <p class="muted">需要先配置短信。验证码发到新手机号。</p>
      <label>新手机号<input v-model="phone" autocomplete="tel" /></label>
      <button class="btn-ghost" type="button" @click="sendRebind">发送换绑验证码</button>
      <label>验证码<input v-model="phoneCode" inputmode="numeric" /></label>
      <button class="btn-ghost" type="button" @click="confirmRebind">确认换绑</button>
      <label>注销账号（请输入密码）<input v-model="password" type="password" autocomplete="current-password" /></label>
      <button class="btn-ghost" type="button" @click="removeAccount">注销并删除数据</button>
      <p v-if="message" class="banner ok">{{ message }}</p>
      <p v-if="error" class="banner">{{ error }}</p>
    </section>
    <section class="card">
      <h2>同意记录</h2>
      <table>
        <thead><tr><th>文件</th><th>版本</th><th>时间</th><th></th></tr></thead>
        <tbody>
          <tr v-for="item in consents" :key="item.id">
            <td>{{ item.title }}</td>
            <td>{{ item.version }}</td>
            <td>{{ item.withdrawn_at ? `已于 ${formatTime(item.withdrawn_at)} 撤回` : formatTime(item.created_at) }}</td>
            <td><button v-if="!item.withdrawn_at" class="btn-ghost" type="button" @click="withdraw(item)">撤回</button></td>
          </tr>
        </tbody>
      </table>
    </section>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from "vue";
import { useRouter } from "vue-router";
import { api, formatTime } from "../api";
import { logout } from "../session";

const router = useRouter();
const quota = ref({});
const referral = ref({});
const consents = ref([]);
const pay = ref({ enabled: false });
const inviteLink = computed(() => {
  if (!referral.value.path) return "";
  return window.location.origin + referral.value.path;
});
const password = ref("");
const phone = ref("");
const phoneCode = ref("");
const message = ref("");
const error = ref("");
const tavily = ref({ set: false, mask: "", notice: "" });
const tavilyKey = ref("");

async function loadTavily() {
  tavily.value = await api("/api/me/tavily");
}

async function saveTavily() {
  error.value = "";
  message.value = "";
  try {
    tavily.value = await api("/api/me/tavily", { method: "PUT", body: { api_key: tavilyKey.value } });
    tavilyKey.value = "";
    message.value = "Tavily 密钥已保存";
  } catch (err) {
    error.value = err.message;
  }
}

async function testTavily() {
  error.value = "";
  message.value = "";
  try {
    tavily.value = await api("/api/me/tavily/test", { method: "POST", body: {} });
    message.value = "Tavily 可以连通";
  } catch (err) {
    error.value = err.message;
    await loadTavily();
  }
}

async function deleteTavily() {
  error.value = "";
  message.value = "";
  if (!window.confirm("删除后，生成不再使用你的 Tavily 密钥。")) return;
  try {
    tavily.value = await api("/api/me/tavily", { method: "DELETE" });
    tavilyKey.value = "";
    message.value = "已删除 Tavily 密钥";
  } catch (err) {
    error.value = err.message;
  }
}

async function askRefund() {
  error.value = "";
  message.value = "";
  if (!window.confirm("确认申请全额退款？成功后档位会回到免费。退款到账仍需在支付平台操作。")) return;
  try {
    await api("/api/billing/refund-request", { method: "POST", body: {} });
    message.value = "已标记退款，档位已回到免费。请到商户平台把款项退回。";
    quota.value = await api("/api/me/quota");
  } catch (err) {
    error.value = err.message;
  }
}

async function sendRebind() {
  error.value = "";
  try {
    await api("/api/me/rebind/send", { method: "POST", body: { phone: phone.value } });
    message.value = "验证码已发送";
  } catch (err) {
    error.value = err.message;
  }
}

async function confirmRebind() {
  error.value = "";
  try {
    await api("/api/me/rebind", { method: "POST", body: { phone: phone.value, code: phoneCode.value } });
    message.value = "手机号已更换";
  } catch (err) {
    error.value = err.message;
  }
}

async function withdraw(item) {
  error.value = "";
  if (!window.confirm("撤回后，用户协议或隐私政策需要重新同意才能继续使用。自动发布的同意撤回后，相关计划会关闭自动发布。")) return;
  try {
    const saved = await api(`/api/me/consents/${item.id}/withdraw`, { method: "POST", body: {} });
    message.value = saved.message || "已撤回";
    consents.value = await api("/api/me/consents");
  } catch (err) {
    error.value = err.message;
  }
}

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
  referral.value = await api("/api/me/referral");
  consents.value = await api("/api/me/consents");
  await loadTavily();
  const meta = await api("/api/meta");
  pay.value = meta.pay || pay.value;
});
</script>
