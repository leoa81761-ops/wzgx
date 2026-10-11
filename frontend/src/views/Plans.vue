<template>
  <div class="grid-2">
    <form class="card stack" @submit.prevent="submit">
      <h1 style="font-size: 32px">写作方向</h1>
      <p class="muted">全托管：只选方向。系统按子话题和栏目轮换，90 天内尽量不重复同一主题。</p>
      <p v-if="error" class="banner">{{ error }}</p>
      <p v-if="quota.tier === 'free'" class="banner info">免费版固定在夜间低峰生成，每天 1 批，单篇不超过 {{ quota.max_words || 1000 }} 字。</p>
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
        <button v-for="style in styles" :key="style.key" type="button" class="choice" :class="{ active: form.style_key === style.key }" @click="form.style_key = style.key">
          <strong>{{ style.name }}</strong>
          <div class="muted">{{ style.description }}</div>
        </button>
      </div>
      <div class="stack">
        <span>排版模板</span>
        <div class="row">
          <button v-for="item in templates" :key="item.key" type="button" class="choice" :class="{ active: form.template_key === item.key }" @click="form.template_key = item.key">
            {{ item.name }}
          </button>
        </div>
      </div>
      <label>目标篇幅
        <select v-model.number="form.word_target">
          <option v-for="words in wordOptions" :key="words" :value="words">{{ words }} 字以内</option>
        </select>
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
        <p>{{ plan.style_name }} · {{ plan.template_name }} · {{ plan.schedule_label }} · {{ plan.window_label }}</p>
        <p class="muted" v-if="plan.pause_reason">原因：{{ plan.pause_reason }}</p>
        <p class="muted">下次生成：{{ formatTime(plan.next_run_at) }}</p>
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
const calendars = reactive({});
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
});

const wordOptions = computed(() => {
  const max = quota.value.max_words || 1000;
  return [800, 1000, 1500, 3000].filter((item) => item <= max);
});

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
  }
  if (!form.wx_account_id && accountData[0]) form.wx_account_id = accountData[0].id;
  if (quotaData.tier === "free") form.time_window = "night";
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

async function remove(plan) {
  if (!window.confirm("删除这个写作计划？已生成的文章仍会保留。")) return;
  await api(`/api/plans/${plan.id}`, { method: "DELETE" });
  await load();
}

onMounted(load);
</script>
