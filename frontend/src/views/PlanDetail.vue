<template>
  <div class="stack">
    <div class="split">
      <div>
        <p class="muted"><router-link to="/app/plans">写作计划</router-link></p>
        <h1 style="font-size: 32px">{{ plan.account_name || "公众号" }} · {{ plan.direction || "计划" }}</h1>
      </div>
      <span v-if="plan.id" class="pill" :class="plan.status === 'active' ? 'ok' : 'warn'">{{ plan.status === "active" ? "运行中" : "已暂停" }}</span>
    </div>
    <p v-if="error" class="banner">{{ error }}</p>
    <p v-if="message" class="banner ok">{{ message }}</p>
    <section v-if="plan.id" class="card stack">
      <p class="muted">{{ plan.style_name }} · {{ plan.template_name }} · {{ plan.schedule_label }} · {{ plan.window_label }} · 每天 {{ plan.daily_count || 1 }} 篇</p>
      <p class="muted">{{ plan.status === "active" ? "下次生成：" + formatTime(plan.next_run_at) : "已暂停，启动后才会按点生成。" }}</p>
      <p v-if="plan.pause_reason" class="muted">原因：{{ plan.pause_reason }}</p>
      <div class="row">
        <button v-if="plan.status !== 'active'" class="btn" type="button" @click="toggle">启动</button>
        <button v-else class="btn-ghost" type="button" @click="toggle">暂停</button>
        <button class="btn" type="button" :disabled="busy" @click="generate">{{ busy ? "正在生成…" : "立即生成一篇" }}</button>
      </div>
      <p class="muted">立即生成一篇只写这一次。计划暂停时，这一次不会把它改成运行中。</p>
    </section>
    <form v-if="plan.id" class="card stack" @submit.prevent="save">
      <h2>这个号的设置</h2>
      <p class="muted">方向、关键词、读者、风格、排版、篇幅、周期、禁用词、开头结尾、垂类、选题和配图都只属于「{{ plan.account_name || "这个公众号" }}」。另一个号请单独建计划。</p>
      <label>方向
        <select v-model="directionChoice">
          <option v-for="item in directions" :key="item.key" :value="item.key">{{ item.name }}</option>
          <option value="__custom">自己填写</option>
        </select>
      </label>
      <label v-if="directionChoice === '__custom'">自定义方向<input v-model="customDirection" maxlength="40" /></label>
      <label>关键词<input v-model="form.keywords" maxlength="80" placeholder="例如：验收,机房" /></label>
      <label>读者<input v-model="form.audience" maxlength="80" placeholder="例如：物业工程负责人" /></label>
      <label>禁用词<input v-model="form.banned_words" maxlength="80" placeholder="逗号分隔" /></label>
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
      <label>每天几篇<input v-model.number="form.daily_count" type="number" min="1" :max="quota.daily_limit || 1" /></label>
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
      <label>固定开头<input v-model="form.opening" maxlength="200" /></label>
      <label>固定结尾<input v-model="form.ending" maxlength="200" /></label>
      <label>栏目名<input v-model="form.column_name" /></label>
      <label>该栏目风格<input v-model="form.column_style" /></label>
      <label>垂类资料包
        <select v-model="form.vertical_key">
          <option value="">按方向自动匹配</option>
          <option v-for="item in verticals" :key="item.key" :value="item.key">{{ item.name }}</option>
        </select>
      </label>
      <label>选题方式
        <select v-model="form.topic_mode">
          <option value="auto">自动</option>
          <option value="rotate">只按方向轮换</option>
          <option value="hot">优先热点</option>
          <option value="confirm" :disabled="quota.tier !== 'premium' && quota.tier !== 'lifetime'">先确认选题</option>
        </select>
      </label>
      <label>配图
        <select v-model="form.image_mode">
          <option value="auto">自动</option>
          <option value="none">不用 AI 配图</option>
          <option value="cover">只要封面</option>
          <option value="asset">优先我的素材</option>
        </select>
      </label>
      <button class="btn" :disabled="saving">{{ saving ? "保存中…" : "保存这个号的设置" }}</button>
    </form>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { WEEKDAYS, api, formatTime } from "../api";

const route = useRoute();
const router = useRouter();
const plan = ref({});
const quota = ref({});
const styles = ref([]);
const templates = ref([]);
const windows = ref([]);
const directions = ref([]);
const verticals = ref([]);
const error = ref("");
const message = ref("");
const busy = ref(false);
const saving = ref(false);
const directionChoice = ref("职场成长");
const customDirection = ref("");
const form = reactive({
  style_key: "warm",
  template_key: "simple",
  word_target: 800,
  daily_count: 1,
  schedule_type: "daily",
  weekdays: [1, 3, 5],
  time_window: "night",
  opening: "",
  ending: "",
  column_name: "",
  column_style: "",
  keywords: "",
  audience: "",
  banned_words: "",
  vertical_key: "",
  topic_mode: "auto",
  image_mode: "auto",
});

function tierRank(tier) {
  return { free: 0, standard: 1, premium: 2, lifetime: 3 }[tier] || 0;
}

const styleChoices = computed(() => styles.value.filter((item) => tierRank(quota.value.tier) >= tierRank(item.min_tier || "free")));
const templateChoices = computed(() => templates.value.filter((item) => tierRank(quota.value.tier) >= tierRank(item.min_tier || "free")));
const wordOptions = computed(() => {
  const max = quota.value.max_words || 1000;
  return [800, 1000, 1500, 3000].filter((item) => item <= max || item === form.word_target);
});

function fill(row) {
  plan.value = row;
  const known = directions.value.some((item) => item.key === row.direction);
  directionChoice.value = known ? row.direction : "__custom";
  customDirection.value = known ? "" : row.direction || "";
  form.style_key = row.style_key;
  form.template_key = row.template_key;
  form.word_target = row.word_target;
  form.daily_count = row.daily_count || 1;
  form.schedule_type = row.schedule_type || "daily";
  form.weekdays = Array.isArray(row.weekdays) && row.weekdays.length ? [...row.weekdays] : [1, 3, 5];
  form.time_window = row.time_window || "night";
  form.opening = row.opening || "";
  form.ending = row.ending || "";
  form.column_name = row.column_name || "";
  form.column_style = row.column_style || "";
  form.keywords = row.keywords || "";
  form.audience = row.audience || "";
  form.banned_words = row.banned_words || "";
  form.vertical_key = row.vertical_key || "";
  form.topic_mode = row.topic_mode || "auto";
  form.image_mode = row.image_mode || "auto";
}

async function load() {
  const id = route.params.id;
  const [meta, quotaData, row] = await Promise.all([
    api("/api/meta"),
    api("/api/me/quota"),
    api(`/api/plans/${id}`),
  ]);
  styles.value = meta.styles || [];
  templates.value = meta.templates || [];
  windows.value = meta.windows || [];
  directions.value = meta.directions || [];
  verticals.value = meta.verticals || [];
  quota.value = quotaData;
  fill(row);
}

async function toggle() {
  error.value = "";
  message.value = "";
  const action = plan.value.status === "active" ? "pause" : "resume";
  try {
    const row = await api(`/api/plans/${plan.value.id}/${action}`, { method: "POST" });
    fill({ ...plan.value, ...row });
    message.value = action === "resume" ? "已启动" : "已暂停";
  } catch (err) {
    error.value = err.message;
  }
}

async function generate() {
  error.value = "";
  busy.value = true;
  try {
    const article = await api(`/api/plans/${plan.value.id}/generate`, { method: "POST" });
    router.push(`/app/articles/${article.id}`);
  } catch (err) {
    error.value = err.message;
  } finally {
    busy.value = false;
  }
}

async function save() {
  error.value = "";
  message.value = "";
  saving.value = true;
  try {
    const direction = directionChoice.value === "__custom" ? customDirection.value.trim() : directionChoice.value;
    const row = await api(`/api/plans/${plan.value.id}`, {
      method: "PUT",
      body: {
        wx_account_id: plan.value.wx_account_id,
        direction,
        style_key: form.style_key,
        template_key: form.template_key,
        word_target: Number(form.word_target),
        daily_count: Number(form.daily_count || 1),
        schedule_type: form.schedule_type,
        weekdays: form.schedule_type === "weekly" ? form.weekdays : [],
        time_window: form.time_window,
        opening: form.opening,
        ending: form.ending,
        column_name: form.column_name,
        column_style: form.column_style,
        keywords: form.keywords,
        audience: form.audience,
        banned_words: form.banned_words,
        vertical_key: form.vertical_key,
        topic_mode: form.topic_mode,
        image_mode: form.image_mode,
      },
    });
    fill(row);
    message.value = "已保存，只影响这个公众号";
  } catch (err) {
    error.value = err.message;
  } finally {
    saving.value = false;
  }
}

onMounted(load);
</script>
