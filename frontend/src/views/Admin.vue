<template>
  <div class="stack">
    <div class="split">
      <h1 style="font-size: 32px">配置中心</h1>
      <div class="row">
        <button v-for="item in tabs" :key="item.key" class="btn-ghost" :class="{ btn: tab === item.key }" type="button" @click="tab = item.key">{{ item.label }}</button>
      </div>
    </div>
    <p v-if="error" class="banner">{{ error }}</p>
    <p v-if="message" class="banner ok">{{ message }}</p>

    <section v-if="tab === 'users'" class="card stack">
      <h2>手工开通会员</h2>
      <p class="muted">没有在线支付。确认线下收款后在这里改档位，备注会写入权益记录和审计日志。</p>
      <table>
        <thead><tr><th>账号</th><th>档位</th><th>到期</th><th>状态</th><th>调整</th></tr></thead>
        <tbody>
          <tr v-for="user in users" :key="user.id">
            <td>{{ user.account }}<div class="muted" v-if="user.is_admin">管理员</div></td>
            <td>{{ user.tier_label }}</td>
            <td>{{ formatTime(user.tier_expires_at) }}</td>
            <td>{{ user.is_active ? "启用" : "已停用" }}</td>
            <td>
              <div class="stack">
                <select v-model="edits[user.id].tier">
                  <option value="free">免费版</option>
                  <option value="standard">普通版</option>
                  <option value="premium">高级版</option>
                  <option value="lifetime">买断版</option>
                </select>
                <input v-model="edits[user.id].expires" type="datetime-local" />
                <input v-model="edits[user.id].note" placeholder="备注，例如线下收款 30 元" />
                <div class="row">
                  <button class="btn" type="button" @click="saveTier(user)">保存</button>
                  <button v-if="!user.is_self" class="btn-ghost" type="button" @click="setActive(user, !user.is_active)">{{ user.is_active ? "停用" : "启用" }}</button>
                  <button v-if="!user.is_self" class="btn-ghost" type="button" @click="removeUser(user)">删除</button>
                </div>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </section>

    <section v-if="tab === 'models'" class="grid-2">
      <form class="card stack" @submit.prevent="saveProvider">
        <h2>{{ editingId ? "编辑模型服务" : "添加模型服务" }}</h2>
        <label>名称<input v-model="provider.name" required /></label>
        <label>用途
          <select v-model="provider.kind">
            <option value="text">文本写作</option>
            <option value="image">封面生图</option>
          </select>
        </label>
        <label>协议
          <select v-model="provider.protocol">
            <option value="openai">OpenAI 兼容 Chat Completions</option>
            <option value="responses">OpenAI Responses</option>
            <option value="anthropic" :disabled="provider.kind === 'image'">Anthropic Messages</option>
          </select>
        </label>
        <label>Base URL<input v-model="provider.base_url" required placeholder="https://api.openai.com/v1" /></label>
        <label>API Key<input v-model="provider.api_key" type="password" :placeholder="editingId ? '留空则不修改' : ''" /></label>
        <label>模型名<input v-model="provider.model" required /></label>
        <label>额外请求头 JSON<textarea v-model="provider.headersText" placeholder='{"X-Tenant":"demo"}'></textarea></label>
        <label>额外参数 JSON<textarea v-model="provider.paramsText" placeholder='{"temperature":0.7}'></textarea></label>
        <label>推理档位
          <select v-model="provider.reasoning_effort">
            <option value="">不发送</option>
            <option value="low">low</option>
            <option value="medium">medium</option>
            <option value="high">high</option>
          </select>
        </label>
        <p class="muted">推理模型不要把 max_tokens 设得很小。档位会写入 reasoning_effort。流式调用时超时可以放到 600 秒。</p>
        <label>优先级（数字小的先作为默认回退）<input v-model.number="provider.priority" type="number" /></label>
        <label>超时（秒）<input v-model.number="provider.timeout_seconds" type="number" min="5" max="600" /></label>
        <label class="check"><input v-model="provider.enabled" type="checkbox" /> 启用</label>
        <div class="row">
          <button class="btn" type="submit">保存</button>
          <button class="btn-ghost" type="button" @click="resetProvider">清空</button>
        </div>
      </form>
      <div class="stack">
        <article v-for="item in providers" :key="item.id" class="card stack">
          <div class="split">
            <strong>{{ item.name }}</strong>
            <span class="pill" :class="item.enabled ? 'ok' : ''">{{ item.enabled ? "启用" : "停用" }}</span>
          </div>
          <p>{{ providerLabel(item) }} · {{ item.model }}</p>
          <p class="muted">{{ item.base_url }}</p>
          <p class="muted">密钥 {{ item.key_hint || "未设置" }} · 优先级 {{ item.priority }}</p>
          <div class="row">
            <button class="btn-ghost" type="button" @click="editProvider(item)">编辑</button>
            <button class="btn-ghost" type="button" @click="testProvider(item, false)">模拟测试</button>
            <button class="btn-ghost" type="button" @click="testProvider(item, true)">真实连通</button>
            <button class="btn-ghost" type="button" @click="removeProvider(item)">删除</button>
          </div>
        </article>
      </div>
    </section>

    <section v-if="tab === 'routes'" class="card stack">
      <h2>环节回退顺序</h2>
      <p class="muted">每个写作环节按从上到下的顺序调用。上面的失败或返回无法解析时，自动换下一个。选题、大纲、正文、审核未配置时，按优先级使用全部已启用的文本模型。资料检索不配置就不会联网，也不会借用其它环节的模型。封面在「模型」里把用途选成「封面生图」。</p>
      <label>应用到
        <select v-model="routeTier" @change="syncOrders">
          <option v-for="item in tiers" :key="item.key" :value="item.key">{{ item.label }}</option>
        </select>
      </label>
      <div v-for="stage in stages" :key="stage.key" class="choice">
        <strong>{{ stage.label }}</strong>
        <div v-for="(id, index) in orders[stage.key] || []" :key="`${stage.key}-${id}-${index}`" class="split">
          <span>{{ providerName(id) }}</span>
          <div class="row">
            <button class="btn-ghost" type="button" @click="move(stage.key, index, -1)">上移</button>
            <button class="btn-ghost" type="button" @click="move(stage.key, index, 1)">下移</button>
            <button class="btn-ghost" type="button" @click="orders[stage.key].splice(index, 1)">移除</button>
          </div>
        </div>
        <div class="row">
          <select v-model="pick[stage.key]">
            <option value="">添加模型</option>
            <option v-for="item in textProviders" :key="item.id" :value="item.id">{{ item.name }}</option>
          </select>
          <button class="btn-ghost" type="button" @click="addRoute(stage.key)">加入</button>
          <button class="btn" type="button" @click="saveRoute(stage.key)">保存此环节</button>
        </div>
      </div>
    </section>

    <section v-if="tab === 'wechat'" class="card stack" style="max-width: 640px">
      <h2>微信</h2>
      <label>模式
        <select v-model="wechat.mode">
          <option value="mock">模拟（不访问微信）</option>
          <option value="live">正式接口</option>
        </select>
      </label>
      <label>接口根地址<input v-model="wechat.api_base" /></label>
      <label>固定出口 IP<input v-model="wechat.public_egress_ip" placeholder="填给用户做 IP 白名单" /></label>
      <div class="row">
        <button class="btn" type="button" @click="saveWechat">保存</button>
        <button class="btn-ghost" type="button" @click="connectivity">真实连通测试</button>
      </div>
      <p v-if="probeText" class="muted">{{ probeText }}</p>
    </section>

    <section v-if="tab === 'words'" class="card stack" style="max-width: 640px">
      <h2>敏感词</h2>
      <p class="muted">每行一个。内置词会始终生效，这里是额外补充。</p>
      <textarea v-model="words" rows="8"></textarea>
      <button class="btn" type="button" @click="saveWords">保存</button>
      <h2>来源域名</h2>
      <p class="muted">资料检索会丢掉屏蔽名单里的网站。允许名单不为空时，只保留这些域名。每行一个，例如 xilu.com。还没保存过时，屏蔽名单使用内置的聚合站。</p>
      <label>屏蔽<textarea v-model="blockedDomains" rows="6"></textarea></label>
      <label>允许<textarea v-model="allowedDomains" rows="4"></textarea></label>
      <button class="btn" type="button" @click="saveDomains">保存来源域名</button>
    </section>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from "vue";
import { api, formatTime } from "../api";

const tabs = [
  { key: "users", label: "会员" },
  { key: "models", label: "模型" },
  { key: "routes", label: "环节路由" },
  { key: "wechat", label: "微信" },
  { key: "words", label: "敏感词" },
];
const tab = ref("models");
const error = ref("");
const message = ref("");
const users = ref([]);
const edits = reactive({});
const providers = ref([]);
const stages = ref([]);
const tiers = ref([]);
const routeRows = ref([]);
const routeTier = ref("*");
const orders = reactive({});
const pick = reactive({});
const editingId = ref(0);
const words = ref("");
const blockedDomains = ref("");
const allowedDomains = ref("");
const probeText = ref("");
const wechat = reactive({ mode: "mock", api_base: "https://api.weixin.qq.com", public_egress_ip: "" });
const provider = reactive({
  name: "",
  protocol: "openai",
  base_url: "https://api.openai.com/v1",
  api_key: "",
  model: "",
  headersText: "{}",
  paramsText: "{}",
  reasoning_effort: "",
  kind: "text",
  priority: 100,
  enabled: true,
  timeout_seconds: 120,
});

const textProviders = computed(() => providers.value.filter((item) => item.kind !== "image" && item.config_type !== "image_model"));

function providerName(id) {
  return providers.value.find((item) => item.id === id)?.name || `#${id}`;
}

function providerLabel(item) {
  if (item.kind === "image" || item.config_type === "image_model") return "封面生图";
  if (item.protocol === "anthropic") return "Anthropic Messages";
  if (item.protocol === "responses") return "OpenAI Responses";
  return "OpenAI Chat Completions";
}

function resetProvider() {
  editingId.value = 0;
  Object.assign(provider, {
    name: "",
    protocol: "openai",
    base_url: "https://api.openai.com/v1",
    api_key: "",
    model: "",
    headersText: "{}",
    paramsText: "{}",
    reasoning_effort: "",
    kind: "text",
    priority: 100,
    enabled: true,
    timeout_seconds: 120,
  });
}

function editProvider(item) {
  editingId.value = item.id;
  Object.assign(provider, {
    name: item.name,
    protocol: item.protocol,
    base_url: item.base_url,
    api_key: "",
    model: item.model,
    headersText: JSON.stringify(item.extra_headers || {}, null, 2),
    paramsText: JSON.stringify(item.extra_params || {}, null, 2),
    reasoning_effort: item.reasoning_effort || "",
    kind: item.kind || (item.config_type === "image_model" ? "image" : "text"),
    priority: item.priority,
    enabled: item.enabled,
    timeout_seconds: item.timeout_seconds,
  });
}

function parseObject(text, label) {
  let data;
  try {
    data = JSON.parse(text || "{}");
  } catch {
    throw new Error(`${label}不是合法 JSON`);
  }
  if (!data || typeof data !== "object" || Array.isArray(data)) throw new Error(`${label}需要是 JSON 对象`);
  return data;
}

function syncOrders() {
  for (const stage of stages.value) {
    const found = routeRows.value.find((item) => item.stage === stage.key && item.tier === routeTier.value);
    orders[stage.key] = found ? [...found.provider_ids] : [];
  }
}

async function load() {
  users.value = await api("/api/admin/users");
  for (const user of users.value) {
    edits[user.id] = edits[user.id] || { tier: user.tier, expires: "", note: "" };
    edits[user.id].tier = user.tier;
  }
  providers.value = await api("/api/admin/providers");
  const routeData = await api("/api/admin/routes");
  stages.value = routeData.stages;
  tiers.value = routeData.tiers;
  routeRows.value = routeData.routes;
  syncOrders();
  Object.assign(wechat, await api("/api/admin/wechat"));
  const wordData = await api("/api/admin/sensitive-words");
  words.value = (wordData.custom || []).join("\n");
  const domainData = await api("/api/admin/source-domains");
  blockedDomains.value = (domainData.block || []).join("\n");
  allowedDomains.value = (domainData.allow || []).join("\n");
}

async function setActive(user, active) {
  error.value = "";
  message.value = "";
  const verb = active ? "启用" : "停用";
  if (!active && !window.confirm(`停用账号「${user.account}」？停用后无法登录。`)) return;
  try {
    await api(`/api/admin/users/${user.id}/active`, { method: "PUT", body: { active } });
    message.value = `已${verb}`;
    await load();
  } catch (err) {
    error.value = err.message;
  }
}

async function removeUser(user) {
  error.value = "";
  message.value = "";
  if (!window.confirm(`删除账号「${user.account}」及其文章、计划和额度记录？此操作不能恢复。`)) return;
  try {
    await api(`/api/admin/users/${user.id}`, { method: "DELETE" });
    message.value = "账号已删除";
    await load();
  } catch (err) {
    error.value = err.message;
  }
}

async function saveTier(user) {
  error.value = "";
  message.value = "";
  const edit = edits[user.id];
  try {
    await api(`/api/admin/users/${user.id}/tier`, {
      method: "PUT",
      body: {
        tier: edit.tier,
        expires_at: edit.expires ? `${edit.expires}:00+08:00` : null,
        note: edit.note,
      },
    });
    message.value = "档位已更新";
    await load();
  } catch (err) {
    error.value = err.message;
  }
}

async function saveProvider() {
  error.value = "";
  message.value = "";
  try {
    if (provider.kind === "image") provider.protocol = "openai";
    const body = {
      name: provider.name,
      protocol: provider.protocol,
      base_url: provider.base_url,
      api_key: provider.api_key,
      model: provider.model,
      extra_headers: parseObject(provider.headersText, "额外请求头"),
      extra_params: parseObject(provider.paramsText, "额外参数"),
      kind: provider.kind,
      reasoning_effort: provider.reasoning_effort,
      priority: provider.priority,
      enabled: provider.enabled,
      timeout_seconds: provider.timeout_seconds,
    };
    if (editingId.value) {
      await api(`/api/admin/providers/${editingId.value}`, { method: "PUT", body });
    } else {
      await api("/api/admin/providers", { method: "POST", body });
    }
    message.value = "模型服务已保存。密钥只显示掩码。";
    resetProvider();
    await load();
  } catch (err) {
    error.value = err.message;
  }
}

async function testProvider(item, live) {
  error.value = "";
  message.value = "";
  try {
    const result = await api(`/api/admin/providers/${item.id}/test`, { method: "POST", body: { live } });
    message.value = result.mock ? `${item.name}：模拟测试通过（${result.protocol} / ${result.model}）` : `${item.name} 连通成功：${result.text}`;
  } catch (err) {
    error.value = err.message;
  }
}

async function removeProvider(item) {
  if (!window.confirm(`删除模型服务「${item.name}」？`)) return;
  await api(`/api/admin/providers/${item.id}`, { method: "DELETE" });
  await load();
}

function addRoute(stage) {
  const id = Number(pick[stage]);
  if (!id) return;
  orders[stage] = orders[stage] || [];
  orders[stage].push(id);
  pick[stage] = "";
}

function move(stage, index, delta) {
  const list = orders[stage];
  const next = index + delta;
  if (next < 0 || next >= list.length) return;
  const [item] = list.splice(index, 1);
  list.splice(next, 0, item);
}

async function saveRoute(stage) {
  error.value = "";
  try {
    await api("/api/admin/routes", {
      method: "PUT",
      body: { stage, tier: routeTier.value, provider_ids: orders[stage] || [] },
    });
    message.value = "回退顺序已保存";
    routeRows.value = (await api("/api/admin/routes")).routes;
  } catch (err) {
    error.value = err.message;
  }
}

async function connectivity() {
  error.value = "";
  message.value = "";
  probeText.value = "正在测试，生成和写入草稿可能需要几分钟。";
  try {
    const result = await api("/api/admin/connectivity-test", { method: "POST", body: {} });
    const text = (result.steps || []).map((step) => `${step.name}：${step.ok ? "成功" : "失败"} ${step.detail || ""}`).join("；");
    probeText.value = text;
    message.value = "真实连通测试完成，草稿已留在公众号草稿箱。";
  } catch (err) {
    probeText.value = err.message;
    error.value = err.message;
  }
}

async function saveWechat() {
  error.value = "";
  try {
    await api("/api/admin/wechat", { method: "PUT", body: { ...wechat } });
    message.value = "微信设置已保存";
  } catch (err) {
    error.value = err.message;
  }
}

async function saveWords() {
  error.value = "";
  try {
    const list = words.value.split(/\n+/).map((item) => item.trim()).filter(Boolean);
    await api("/api/admin/sensitive-words", { method: "PUT", body: { words: list } });
    message.value = "敏感词已保存";
  } catch (err) {
    error.value = err.message;
  }
}

function domainLines(text) {
  return text.split(/\n+/).map((item) => item.trim()).filter(Boolean);
}

async function saveDomains() {
  error.value = "";
  try {
    const saved = await api("/api/admin/source-domains", {
      method: "PUT",
      body: { block: domainLines(blockedDomains.value), allow: domainLines(allowedDomains.value) },
    });
    blockedDomains.value = (saved.block || []).join("\n");
    allowedDomains.value = (saved.allow || []).join("\n");
    message.value = "来源域名已保存";
  } catch (err) {
    error.value = err.message;
  }
}

onMounted(load);
</script>
