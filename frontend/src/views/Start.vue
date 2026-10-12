<template>
  <div class="stack">
    <div>
      <p class="muted"><router-link to="/app">工作台</router-link></p>
      <h1>给这个号做一次设置</h1>
      <p class="muted">四步：绑定公众号，选方向，看 200 字试读，然后启动。设置只属于这一个号。</p>
    </div>
    <ol class="steps">
      <li class="step"><span class="num">1</span><span>绑定公众号</span></li>
      <li class="step"><span class="num">2</span><span>选择方向</span></li>
      <li class="step"><span class="num">3</span><span>看一段试读</span></li>
      <li class="step"><span class="num">4</span><span>启动</span></li>
    </ol>
    <p v-if="error" class="banner">{{ error }}</p>
    <p v-if="message" class="banner ok">{{ message }}</p>

    <form v-if="!accountId" class="card stack" @submit.prevent="bind">
      <h2>1. 绑定公众号</h2>
      <p v-if="mode === 'mock'" class="banner info">当前是模拟模式，填写任意 AppID 即可，不会请求微信。</p>
      <label>显示名称<input v-model="bindForm.name" required placeholder="例如：弱电陈工" /></label>
      <label>AppID<input v-model="bindForm.app_id" required placeholder="wx_mock_demo" /></label>
      <label>AppSecret<input v-model="bindForm.app_secret" type="password" required /></label>
      <label>主体类型
        <select v-model="bindForm.subject_type">
          <option value="personal">个人</option>
          <option value="enterprise">企业</option>
        </select>
      </label>
      <label>账号类型
        <select v-model="bindForm.service_type">
          <option value="subscription">订阅号</option>
          <option value="service">服务号</option>
        </select>
      </label>
      <label class="check">
        <input v-model="bindForm.consent" type="checkbox" />
        <span>我同意系统加密保存 AppID 与 AppSecret，用于草稿箱检测和写入。我有权管理这个公众号。</span>
      </label>
      <button class="btn" :disabled="!bindForm.consent || binding">{{ binding ? "正在检测…" : "绑定并继续" }}</button>
      <p v-if="accounts.length" class="muted">也可以从已绑定的号里选一个：</p>
      <div v-if="accounts.length" class="row">
        <button v-for="account in accounts" :key="account.id" class="btn-ghost" type="button" @click="choose(account.id)">{{ account.name }}</button>
      </div>
    </form>

    <section v-else class="card stack">
      <h2>{{ accountName || "这个号" }}</h2>
      <p class="muted">2. 选择方向。下面是按号的类型和垂类给出的建议，可以改。</p>
      <div class="row">
        <button v-for="item in directions" :key="item.key" type="button" class="choice" :class="{ active: direction === item.key }" @click="pickDirection(item.key)">
          {{ item.name }}
        </button>
        <button type="button" class="choice" :class="{ active: custom }" @click="custom = true">自己填写</button>
      </div>
      <label v-if="custom">方向<input v-model="direction" maxlength="40" placeholder="例如：弱电智能化" /></label>
      <button class="btn" type="button" :disabled="loading" @click="loadPreview">{{ loading ? "正在准备…" : "3. 看 200 字试读" }}</button>
      <article v-if="preview.sample" class="stack">
        <p class="banner info">{{ preview.reason }}</p>
        <p>{{ preview.sample }}</p>
        <p class="muted">{{ preview.runes }} 字 · {{ preview.style_name }} · {{ preview.window_label }} · 读者：{{ preview.audience }}</p>
        <p v-if="preview.tone" class="muted">语气：{{ preview.tone }} · 口头禅：{{ preview.signatures }} · 不写：{{ preview.taboos }}</p>
        <button class="btn" type="button" :disabled="starting" @click="start">{{ starting ? "正在启动…" : "4. 启动这个号" }}</button>
      </article>
    </section>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { api } from "../api";

const route = useRoute();
const router = useRouter();
const accounts = ref([]);
const directions = ref([]);
const mode = ref("");
const accountId = ref(0);
const accountName = ref("");
const direction = ref("");
const custom = ref(false);
const preview = ref({});
const error = ref("");
const message = ref("");
const binding = ref(false);
const loading = ref(false);
const starting = ref(false);
const bindForm = reactive({
  name: "",
  app_id: "",
  app_secret: "",
  subject_type: "personal",
  service_type: "subscription",
  verified: false,
  consent: false,
});

function knownDirection(value) {
  return directions.value.some((item) => item.key === value);
}

async function choose(id) {
  error.value = "";
  accountId.value = id;
  const account = accounts.value.find((item) => item.id === id);
  accountName.value = account ? account.name : "";
  preview.value = {};
  await loadPreview();
}

async function loadPreview() {
  if (!accountId.value) return;
  error.value = "";
  loading.value = true;
  try {
    const body = { account_id: accountId.value };
    if (direction.value && (custom.value || knownDirection(direction.value))) body.direction = direction.value;
    const data = await api("/api/setup/preview", { method: "POST", body });
    preview.value = data;
    direction.value = data.direction || "";
    custom.value = !knownDirection(direction.value);
    accountName.value = data.account_name || accountName.value;
  } catch (err) {
    error.value = err.message;
  } finally {
    loading.value = false;
  }
}

function pickDirection(key) {
  custom.value = false;
  direction.value = key;
  loadPreview();
}

async function bind() {
  error.value = "";
  binding.value = true;
  try {
    const saved = await api("/api/accounts", { method: "POST", body: { ...bindForm } });
    accounts.value = await api("/api/accounts");
    await choose(saved.id);
  } catch (err) {
    error.value = err.message;
  } finally {
    binding.value = false;
  }
}

async function start() {
  if (!preview.value.direction) return;
  error.value = "";
  starting.value = true;
  try {
    const plan = await api("/api/plans", {
      method: "POST",
      body: {
        wx_account_id: accountId.value,
        direction: direction.value || preview.value.direction,
        style_key: preview.value.style_key,
        template_key: preview.value.template_key,
        word_target: preview.value.word_target,
        schedule_type: "daily",
        time_window: preview.value.time_window,
        daily_count: 1,
        keywords: preview.value.keywords || "",
        audience: preview.value.audience || "",
        vertical_key: preview.value.vertical_key || "",
        tone: preview.value.tone || "",
        persona_columns: preview.value.persona_columns || "",
        signatures: preview.value.signatures || "",
        taboos: preview.value.taboos || "",
        topic_mode: preview.value.topic_mode || "auto",
        image_mode: preview.value.image_mode || "auto",
      },
    });
    message.value = "已启动";
    router.push(`/app/plans/${plan.id}`);
  } catch (err) {
    error.value = err.message;
  } finally {
    starting.value = false;
  }
}

onMounted(async () => {
  const [meta, list] = await Promise.all([api("/api/meta"), api("/api/accounts")]);
  directions.value = meta.directions || [];
  mode.value = meta.wechat_mode || "";
  accounts.value = list;
  const query = Number(route.query.account || 0);
  if (query && list.some((item) => item.id === query)) {
    await choose(query);
  }
});
</script>
