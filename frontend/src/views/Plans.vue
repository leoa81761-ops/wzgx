<template>
  <div class="grid-2">
    <form class="card stack" @submit.prevent="submit">
      <h1 style="font-size: 32px">写作方向</h1>
      <p class="muted">全托管：只选方向。系统按子话题和栏目轮换，90 天内尽量不重复同一主题。</p>
      <p v-if="error" class="banner">{{ error }}</p>
      <p class="banner info">今天还剩 {{ quota.daily_remaining ?? 0 }} 篇，{{ quota.period_label || "本月" }}还剩 {{ quota.period_remaining ?? 0 }} 篇。各方向每天篇数加起来不能超过 {{ quota.daily_limit || 1 }} 篇。</p>
      <p v-if="quota.tier === 'free'" class="banner info">免费版固定在夜间低峰生成，每天最多 {{ quota.daily_limit || 1 }} 篇，单篇不超过 {{ quota.max_words || 1000 }} 字。</p>
      <label>公众号
        <select v-model="form.wx_account_id" required>
          <option disabled value="">请选择</option>
          <option v-for="account in accounts" :key="account.id" :value="account.id">{{ account.name }}（{{ account.delivery_label }}）</option>
        </select>
      </label>
      <label>方向
        <select v-model="directionChoice">
          <option v-for="item in directions" :key="item.key" :value="item.key">{{ item.name }}</option>
          <option value="__custom">自己填写</option>
        </select>
      </label>
      <label v-if="directionChoice === '__custom'">自定义方向<input v-model="customDirection" placeholder="例如：城市徒步" /></label>
      <div class="stack">
        <span>风格</span>
        <button v-for="style in styleChoices" :key="style.key" type="button" class="choice" :class="{ active: form.style_key === style.key }" @click="form.style_key = style.key">
          <strong>{{ style.name }}</strong>
          <div class="muted">{{ style.description }}</div>
        </button>
      </div>
      <div class="stack">
        <span>排版模板</span>
        <div class="row">
          <button v-for="item in templateChoices" :key="item.key" type="button" class="choice" :class="{ active: form.template_key === item.key }" @click="form.template_key = item.key">
            {{ item.name }}
          </button>
        </div>
      </div>
      <label>目标篇幅
        <select v-model.number="form.word_target">
          <option v-for="words in wordOptions" :key="words" :value="words">{{ words }} 字以内</option>
        </select>
      </label>
      <label>这个方向每天几篇
        <input v-model.number="form.daily_count" type="number" min="1" :max="quota.daily_limit || 1" />
      </label>
      <label>周期
        <select v-model="form.schedule_type">
          <option value="daily">每天</option>
          <option value="weekly">每周</option>
        </select>
      </label>
      <div v-if="form.schedule_type === 'weekly'" class="row">
        <label v-for="day in WEEKDAYS" :key="day.value" class="check">
          <input type="checkbox" :value="day.value" v-model="form.weekdays" /> {{ day.label }}
        </label>
      </div>
      <label>固定开头<input v-model="form.opening" maxlength="200" placeholder="例如：关注后回复「目录」可看往期" /></label>
      <label>固定结尾<input v-model="form.ending" maxlength="200" placeholder="例如：觉得有用就转给同事" /></label>
      <label>栏目名<input v-model="form.column_name" placeholder="留空则整号用上面的风格" /></label>
      <label>该栏目风格<input v-model="form.column_style" placeholder="例如 plain，只在这一栏切换" /></label>
      <label>时段
        <select v-model="form.time_window" :disabled="quota.tier === 'free'">
          <option v-for="item in windows" :key="item.key" :value="item.key">{{ item.label }}</option>
        </select>
      </label>
      <button class="btn" :disabled="loading">{{ loading ? "保存中…" : "保存计划" }}</button>
    </form>
    <div class="stack">
      <article v-for="plan in plans" :key="plan.id" class="card stack">
        <div class="split">
          <strong>{{ plan.direction }}</strong>
          <span class="pill" :class="plan.status === 'active' ? 'ok' : 'warn'">{{ plan.status === "active" ? "运行中" : "已暂停" }}</span>
        </div>
        <p>{{ plan.style_name }} · {{ plan.template_name }} · {{ plan.schedule_label }} · {{ plan.window_label }} · 每天 {{ plan.daily_count || 1 }} 篇</p>
        <label>每天篇数
          <input v-model.number="dailyCounts[plan.id]" type="number" min="1" :max="quota.daily_limit || 1" />
        </label>
        <button class="btn-ghost" type="button" @click="saveDaily(plan)">保存篇数</button>
        <p class="muted" v-if="plan.pause_reason">原因：{{ plan.pause_reason }}</p>
        <p class="muted">下次生成：{{ formatTime(plan.next_run_at) }}</p>
        <label v-if="quota.tier === 'premium' || quota.tier === 'lifetime' || plan.confirm_mode" class="check">
          <input :checked="plan.confirm_mode" type="checkbox" @change="setConfirm(plan, $event.target.checked)" />
          <span>选题确认：提前推送下一篇选题，24 小时内可以采用或换一个，超时按这个选题写。</span>
        </label>
        <div v-if="plan.proposal" class="stack">
          <p>选题：{{ plan.proposal.title }}（{{ plan.proposal.status === "approved" ? "已采用，到点后写" : "待处理" }}，请在 {{ formatTime(plan.proposal.deadline) }} 前决定）</p>
          <div v-if="plan.proposal.status === 'pending'" class="row">
            <button class="btn-ghost" type="button" @click="acceptProposal(plan)">采用</button>
            <button class="btn-ghost" type="button" @click="rejectProposal(plan)">换一个</button>
          </div>
        </div>
        <p v-if="plan.specified_title" class="muted">下一篇指定题目：{{ plan.specified_title }}。成功生成后会自动清掉。</p>
        <label>指定下一篇题目
          <input v-model="pins[plan.id]" maxlength="64" placeholder="不填则继续自动选题" />
        </label>
        <div class="row">
          <button class="btn-ghost" type="button" @click="savePin(plan)">保存题目</button>
          <button class="btn-ghost" type="button" @click="showCalendar(plan)">内容日历</button>
        </div>
        <p v-if="plan.vetoes?.length" class="muted">
          已否决：
          <button v-for="tag in plan.vetoes" :key="tag" class="text-btn" type="button" @click="unveto(plan, tag)">{{ tag }} ×</button>
        </p>
        <ul v-if="calendars[plan.id]" class="stack">
          <li v-for="item in calendars[plan.id]" :key="item.at + item.topic_tag">
            {{ formatTime(item.at) }} · {{ item.subtopic }} / {{ item.column }} / {{ item.angle }}
            <button class="text-btn" type="button" @click="veto(plan, item.topic_tag)">否决</button>
          </li>
        </ul>
        <div v-if="enterpriseVerified(plan)" class="stack">
          <p class="muted">认证号自动发布默认关闭。开启后，只有低风险稿会在审阅窗口结束后提交。失败、超限或草稿被删时，这篇仍留在草稿箱，并提示到公众号助手查看。</p>
          <p v-if="plan.publish_block && !plan.publish_ready" class="muted">{{ plan.publish_block }}</p>
          <label v-if="plan.publish_ready && !plan.auto_publish" class="check">
            <input v-model="publishConsent[plan.id]" type="checkbox" />
            <span>我确认内容责任由公众号主体承担，并同意系统按审阅窗口自动提交低风险稿。</span>
          </label>
          <label v-if="plan.publish_ready || plan.auto_publish">审阅窗口（分钟，0 到 180。0 表示生成后马上提交）
            <input v-model.number="publishDelay[plan.id]" type="number" min="0" max="180" />
          </label>
          <div class="row">
            <button v-if="plan.auto_publish" class="btn-ghost" type="button" @click="savePublish(plan, false)">关闭自动发布</button>
            <button v-else-if="plan.publish_ready" class="btn-ghost" type="button" @click="savePublish(plan, true)">开启自动发布</button>
          </div>
          <p v-if="plan.auto_publish" class="muted">已开启，审阅窗口 {{ plan.delay_minutes }} 分钟。</p>
        </div>
        <p v-else class="muted">这个公众号只写入草稿箱。自动发布只对企业认证号开放，并且默认关闭。</p>
        <div class="row">
          <button class="btn" type="button" :disabled="busyId === plan.id" @click="generate(plan)">
            {{ busyId === plan.id ? "正在生成…" : "立即生成" }}
          </button>
          <button class="btn-ghost" type="button" @click="toggle(plan)">{{ plan.status === "active" ? "暂停" : "恢复" }}</button>
          <button class="btn-ghost" type="button" @click="remove(plan)">删除</button>
        </div>
      </article>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from "vue";
import { useRouter } from "vue-router";
import { WEEKDAYS, api, formatTime } from "../api";

const router = useRouter();
const accounts = ref([]);
const plans = ref([]);
const styles = ref([]);
const templates = ref([]);
const windows = ref([]);
const directions = ref([]);
const quota = ref({});
const error = ref("");
const loading = ref(false);
const busyId = ref(0);
const pins = reactive({});
const dailyCounts = reactive({});
const calendars = reactive({});
const publishConsent = reactive({});
const publishDelay = reactive({});
const directionChoice = ref("职场成长");
const customDirection = ref("");
const form = reactive({
  wx_account_id: "",
  style_key: "warm",
  template_key: "simple",
  word_target: 800,
  schedule_type: "daily",
  weekdays: [1, 3, 5],
  time_window: "night",
  daily_count: 1,
  opening: "",
  ending: "",
  column_name: "",
  column_style: "",
});

const wordOptions = computed(() => {
  const max = quota.value.max_words || 1000;
  return [800, 1000, 1500, 3000].filter((item) => item <= max);
});

function tierRank(tier) {
  return { free: 0, standard: 1, premium: 2, lifetime: 3 }[tier] || 0;
}

const styleChoices = computed(() => styles.value.filter((item) => tierRank(quota.value.tier) >= tierRank(item.min_tier || "free")));
const templateChoices = computed(() => templates.value.filter((item) => tierRank(quota.value.tier) >= tierRank(item.min_tier || "free")));

async function load() {
  const [meta, quotaData, accountData, planData] = await Promise.all([
    api("/api/meta"),
    api("/api/me/quota"),
    api("/api/accounts"),
    api("/api/plans"),
  ]);
  styles.value = meta.styles;
  templates.value = meta.templates;
  windows.value = meta.windows;
  directions.value = meta.directions;
  quota.value = quotaData;
  accounts.value = accountData;
  plans.value = planData;
  for (const plan of planData) {
    if (pins[plan.id] === undefined) pins[plan.id] = plan.specified_title || "";
    dailyCounts[plan.id] = plan.daily_count || 1;
    if (publishDelay[plan.id] === undefined) publishDelay[plan.id] = plan.delay_minutes ?? 30;
  }
  if (!form.wx_account_id && accountData[0]) form.wx_account_id = accountData[0].id;
  if (quotaData.tier === "free") form.time_window = "night";
  const allowedStyles = styleChoices.value;
  if (allowedStyles.length && !allowedStyles.some((item) => item.key === form.style_key)) form.style_key = allowedStyles[0].key;
  const allowedTemplates = templateChoices.value;
  if (allowedTemplates.length && !allowedTemplates.some((item) => item.key === form.template_key)) form.template_key = allowedTemplates[0].key;
}

async function submit() {
  error.value = "";
  loading.value = true;
  try {
    const direction = directionChoice.value === "__custom" ? customDirection.value.trim() : directionChoice.value;
    await api("/api/plans", { method: "POST", body: { ...form, direction } });
    await load();
  } catch (err) {
    error.value = err.message;
  } finally {
    loading.value = false;
  }
}

async function setConfirm(plan, enabled) {
  error.value = "";
  try {
    await api(`/api/plans/${plan.id}/confirm`, { method: "PUT", body: { enabled } });
    await load();
  } catch (err) {
    error.value = err.message;
    await load();
  }
}

async function acceptProposal(plan) {
  error.value = "";
  try {
    await api(`/api/plans/${plan.id}/proposal/accept`, { method: "POST", body: {} });
    await load();
  } catch (err) {
    error.value = err.message;
  }
}

async function rejectProposal(plan) {
  error.value = "";
  try {
    await api(`/api/plans/${plan.id}/proposal/reject`, { method: "POST", body: {} });
    await load();
  } catch (err) {
    error.value = err.message;
  }
}

async function saveDaily(plan) {
  error.value = "";
  try {
    await api(`/api/plans/${plan.id}/daily`, { method: "PUT", body: { daily_count: Number(dailyCounts[plan.id] || 1) } });
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

async function toggle(plan) {
  const action = plan.status === "active" ? "pause" : "resume";
  try {
    await api(`/api/plans/${plan.id}/${action}`, { method: "POST" });
    await load();
  } catch (err) {
    error.value = err.message;
  }
}

async function savePin(plan) {
  error.value = "";
  try {
    const title = (pins[plan.id] || "").trim();
    if (!title) await api(`/api/plans/${plan.id}/pin`, { method: "DELETE" });
    else await api(`/api/plans/${plan.id}/pin`, { method: "PUT", body: { title } });
    pins[plan.id] = title;
    await load();
    pins[plan.id] = title;
  } catch (err) {
    error.value = err.message;
  }
}

async function showCalendar(plan) {
  error.value = "";
  try {
    const data = await api(`/api/plans/${plan.id}/calendar?count=8`);
    calendars[plan.id] = data.items || [];
  } catch (err) {
    error.value = err.message;
  }
}

async function veto(plan, tag) {
  error.value = "";
  try {
    await api(`/api/plans/${plan.id}/veto`, { method: "POST", body: { topic_tag: tag } });
    await load();
    await showCalendar(plan);
  } catch (err) {
    error.value = err.message;
  }
}

async function unveto(plan, tag) {
  error.value = "";
  try {
    await api(`/api/plans/${plan.id}/unveto`, { method: "POST", body: { topic_tag: tag } });
    await load();
    if (calendars[plan.id]) await showCalendar(plan);
  } catch (err) {
    error.value = err.message;
  }
}

function enterpriseVerified(plan) {
  const account = accounts.value.find((item) => item.id === plan.wx_account_id);
  return account && account.subject_type === "enterprise" && account.verified;
}

async function savePublish(plan, enabled) {
  error.value = "";
  try {
    if (enabled && !publishConsent[plan.id]) {
      error.value = "开启前请勾选内容责任确认";
      return;
    }
    await api(`/api/plans/${plan.id}/publish`, {
      method: "PUT",
      body: { enabled, delay_minutes: Number(publishDelay[plan.id] ?? 30), consent: Boolean(publishConsent[plan.id]) },
    });
    await load();
  } catch (err) {
    error.value = err.message;
  }
}

async function remove(plan) {
  if (!window.confirm("删除这个写作计划？已生成的文章仍会保留。")) return;
  await api(`/api/plans/${plan.id}`, { method: "DELETE" });
  await load();
}

onMounted(load);
</script>
