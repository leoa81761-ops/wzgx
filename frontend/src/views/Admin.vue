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

    <section v-if="tab === 'services'" class="stack">
      <section class="card stack">
        <h2>注册需验证码</h2>
        <p class="muted">默认关闭。打开后，手机号注册要短信验证码，邮箱注册要邮件验证码。验证码 5 分钟有效，提交时先占用一次机会。</p>
        <p v-if="registerCfg.reason" class="banner">{{ registerCfg.reason }}</p>
        <p v-else-if="registerCfg.note" class="muted">{{ registerCfg.note }}</p>
        <label class="check">
          <input v-model="registerCfg.verify" type="checkbox" :disabled="!!registerCfg.reason" />
          注册需验证码
        </label>
        <button class="btn" type="button" :disabled="!!registerCfg.reason" @click="saveRegisterVerify">保存开关</button>
      </section>
      <section v-for="section in integrations" :key="section.key" class="card stack">
        <div class="split">
          <h2>{{ section.title }}</h2>
          <span class="muted">{{ section.ready ? "已配齐" : "未配齐" }}</span>
        </div>
        <p v-if="section.error" class="banner">{{ section.error }}</p>
        <label v-for="field in section.fields" :key="field.key">
          {{ field.label }}
          <span v-if="field.hint && !field.secret" class="muted">{{ field.hint }}</span>
          <span v-else-if="field.secret && field.set" class="muted">已保存 {{ field.hint }}<template v-if="field.source === 'env'">（来自环境变量，留空不修改）</template><template v-else>（留空不修改）</template></span>
          <textarea v-if="field.multiline" v-model="integrationForm[section.key][field.key]" rows="4" :placeholder="field.placeholder || (field.secret ? '留空则不修改' : '')"></textarea>
          <input v-else v-model="integrationForm[section.key][field.key]" :type="field.secret ? 'password' : 'text'" :placeholder="field.placeholder || (field.secret ? '留空则不修改' : '')" />
        </label>
        <div class="row">
          <button class="btn" type="button" @click="saveIntegration(section)">保存</button>
          <button v-if="section.key !== 'public'" class="btn-ghost" type="button" @click="testIntegration(section)">测试连通</button>
        </div>
        <label v-if="section.key === 'sms'">测试手机号<input v-model="probePhone" placeholder="13800138000" /></label>
      </section>
    </section>

    <section v-if="tab === 'users'" class="card stack">
      <h2>套餐额度</h2>
      <p class="muted">每天篇数按用户合计，立即生成和定时生成都从这里扣。免费和普通默认 1，高级和买断默认 5。改完之后，新的写作方向不能把每天篇数加起来超过这个上限。</p>
      <div v-for="item in planConfigs" :key="item.tier" class="row">
        <strong style="min-width: 5em">{{ item.name }}</strong>
        <label>每天<input v-model.number="item.daily_quota" type="number" min="1" max="100" /></label>
        <label>{{ item.period_unit === "year" ? "每年" : "每月" }}<input v-model.number="item.period_quota" type="number" min="1" max="10000" /></label>
        <button class="btn-ghost" type="button" @click="savePlanConfig(item)">保存</button>
      </div>
    </section>
    <section v-if="tab === 'users'" class="card stack">
      <h2>手工开通会员</h2>
      <p class="muted">没有在线支付。确认线下收款后在这里改档位，备注会写入权益记录和审计日志。</p>
      <table>
        <thead><tr><th>账号</th><th>档位</th><th>到期</th><th>状态</th><th>调整</th></tr></thead>
        <tbody>
          <tr v-for="user in users" :key="user.id">
            <td>{{ user.account }}<div class="muted" v-if="user.is_admin">管理员</div></td>
            <td>{{ user.tier_label }}<div v-if="user.pending_tier" class="muted">到期后改为{{ user.pending_label }}</div></td>
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
                <input v-model="edits[user.id].company" placeholder="定制合同公司名称" />
                <input v-model="edits[user.id].contract_no" placeholder="合同编号" />
                <input v-model="edits[user.id].contract_note" placeholder="合同备注" />
                <label class="check"><input type="checkbox" v-model="edits[user.id].later" /> 降级等到期再生效</label>
                <label class="check"><input type="checkbox" :checked="user.quota_exempt" @change="setExempt(user, $event.target.checked)" /> 测试账号不限额</label>
                <div class="row">
                  <button class="btn" type="button" @click="saveTier(user)">保存</button>
                  <button class="btn-ghost" type="button" @click="saveContract(user)">保存合同</button>
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
            <option value="http" :disabled="provider.kind === 'image'">自定义 HTTP 字段映射</option>
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

    <section v-if="tab === 'tasks'" class="card stack">
      <h2>最近任务</h2>
      <table>
        <thead><tr><th>ID</th><th>用户</th><th>状态</th><th>触发</th><th>耗时</th><th>原因</th></tr></thead>
        <tbody>
          <tr v-for="task in tasks" :key="task.id">
            <td>{{ task.id }}</td>
            <td>{{ task.user_id }}</td>
            <td>{{ task.status }}</td>
            <td>{{ task.trigger_type }}</td>
            <td>{{ task.duration_ms }} ms</td>
            <td>{{ task.failure_reason }}</td>
          </tr>
        </tbody>
      </table>
    </section>

    <section v-if="tab === 'complaints'" class="stack">
      <article v-for="item in complaints" :key="item.id" class="card stack">
        <div class="split">
          <strong>#{{ item.id }} · {{ item.kind }}</strong>
          <span class="pill" :class="item.status === 'closed' ? 'ok' : 'warn'">{{ item.status === "closed" ? "已关闭" : "处理中" }}</span>
        </div>
        <p>{{ item.content }}</p>
        <p class="muted">{{ item.contact }} · {{ formatTime(item.created_at) }}</p>
        <p v-if="item.reply">回复：{{ item.reply }}</p>
        <label>回复<textarea v-model="replies[item.id]" rows="3"></textarea></label>
        <button class="btn" type="button" @click="replyComplaint(item)">保存回复并关闭</button>
      </article>
      <p v-if="!complaints.length" class="muted">暂无投诉。</p>
    </section>

    <section v-if="tab === 'orders'" class="stack">
      <p class="muted">退款只改订单标记，不改会员档位。真正退款请到微信商户平台或支付宝后台操作。</p>
      <article v-for="item in payOrders" :key="item.id" class="card stack">
        <div class="split">
          <strong>{{ item.account || item.user_id }} · {{ item.tier }} / {{ item.period }}</strong>
          <span class="pill">{{ item.status }}</span>
        </div>
        <p class="muted">{{ item.provider }} · {{ item.out_trade_no }} · {{ (item.amount_cents / 100).toFixed(2) }} 元</p>
        <button v-if="item.status === 'paid'" class="btn-ghost" type="button" @click="refundOrder(item)">标记已退款</button>
      </article>
      <p v-if="!payOrders.length" class="muted">暂无订单。</p>
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

    <section v-if="tab === 'library'" class="stack">
      <article class="card stack">
        <h2>风格</h2>
        <p class="muted">免费版只能选最低档为免费的风格。更高档位的风格在写作计划里按会员过滤。</p>
        <div v-for="item in styles" :key="item.key" class="stack">
          <div class="row">
            <strong>{{ item.key }}</strong>
            <input v-model="item.name" />
            <select v-model="item.min_tier">
              <option value="free">免费版</option>
              <option value="standard">普通版</option>
              <option value="premium">高级版</option>
              <option value="lifetime">买断版</option>
            </select>
            <label class="check"><input type="checkbox" v-model="item.enabled" /> 启用</label>
            <button class="btn-ghost" type="button" @click="saveStyle(item)">保存</button>
          </div>
          <input v-model="item.description" placeholder="说明" />
          <input v-model="item.tone" placeholder="语气，会写进提示词" />
        </div>
        <div class="row">
          <input v-model="newStyle.key" placeholder="标识，如 story" />
          <input v-model="newStyle.name" placeholder="名称" />
          <button class="btn" type="button" @click="saveStyle(newStyle)">新增风格</button>
        </div>
      </article>
      <article class="card stack">
        <h2>排版模板</h2>
        <div v-for="item in templates" :key="item.key" class="row">
          <strong>{{ item.key }}</strong>
          <input v-model="item.name" />
          <input v-model="item.description" placeholder="说明" />
          <select v-model="item.min_tier">
            <option value="free">免费版</option>
            <option value="standard">普通版</option>
            <option value="premium">高级版</option>
            <option value="lifetime">买断版</option>
          </select>
          <label class="check"><input type="checkbox" v-model="item.enabled" /> 启用</label>
          <button class="btn-ghost" type="button" @click="saveTemplate(item)">保存</button>
        </div>
        <div class="row">
          <input v-model="newTemplate.key" placeholder="标识，如 card" />
          <input v-model="newTemplate.name" placeholder="名称" />
          <button class="btn" type="button" @click="saveTemplate(newTemplate)">新增模板</button>
        </div>
      </article>
      <article class="card stack">
        <h2>垂类规则包</h2>
        <p class="muted">方向名称里包含关键词时使用对应规则。关键词留空的「通用」是兜底。子话题每行一个。</p>
        <div v-for="item in verticals" :key="item.key" class="stack">
          <div class="row">
            <strong>{{ item.key }}</strong>
            <input v-model="item.name" />
            <input v-model="item.keywords" placeholder="关键词，逗号分隔" />
            <label class="check"><input type="checkbox" v-model="item.enabled" /> 启用</label>
            <button class="btn-ghost" type="button" @click="saveVertical(item)">保存</button>
          </div>
          <textarea v-model="item.subtopics" rows="3" placeholder="子话题，每行一个"></textarea>
          <textarea v-model="item.red_lines" rows="2" placeholder="红线"></textarea>
          <input v-model="item.disclaimer" placeholder="文末声明，可留空" />
        </div>
        <div class="row">
          <input v-model="newVertical.key" placeholder="标识，如 travel" />
          <input v-model="newVertical.name" placeholder="名称" />
          <button class="btn" type="button" @click="saveVertical(newVertical)">新增垂类</button>
        </div>
      </article>
    </section>

    <section v-if="tab === 'board'" class="stack">
      <article class="card stack" v-if="metrics">
        <h2>数据看板</h2>
        <p>注册 {{ metrics.users_total }}（7 天 {{ metrics.users_7d }}，30 天 {{ metrics.users_30d }}）。付费用户 {{ metrics.paid_users }}，转化率 {{ Math.round((metrics.conversion || 0) * 1000) / 10 }}%。</p>
        <p>近 30 天支付成功 {{ metrics.orders_paid_30d }} 笔，其中续费 {{ metrics.renewals_30d }} 笔。成功稿件 {{ metrics.articles_30d }} 篇，成本 {{ metrics.cost_cents_30d }} 分，单篇约 {{ metrics.cost_per_article_cents }} 分。</p>
        <p>邀请 {{ metrics.referrals || 0 }} 条，已生效 {{ metrics.referrals_valid || 0 }}，待审核 {{ metrics.referrals_frozen || 0 }}。</p>
        <p class="muted">成本按后台单价和实际 token、检索次数、生图张数估算。单价为 0 或还没有用量时，单篇成本是 0。</p>
      </article>
      <article class="card stack" style="max-width: 640px">
        <h2>成本与熔断</h2>
        <label>文本，每千 token（分） <input v-model.number="costForm.chat_cents_per_1k" type="number" min="0" /></label>
        <label>每次检索（分） <input v-model.number="costForm.search_cents" type="number" min="0" /></label>
        <label>每张 AI 配图（分） <input v-model.number="costForm.image_cents" type="number" min="0" /></label>
        <label>用户每月上限（分，0 表示不熔断） <input v-model.number="costForm.month_cap_cents" type="number" min="0" /></label>
        <label>达到上限
          <select v-model="costForm.action">
            <option value="degrade">降级：停检索和 AI 配图，文章继续写</option>
            <option value="pause">暂停：这篇仍降级写出，然后暂停计划</option>
          </select>
        </label>
        <label><input v-model="costForm.ai_label" type="checkbox" /> 文末添加「本文由人工智能辅助生成」（默认关闭）</label>
        <button class="btn" type="button" @click="saveCost">保存</button>
      </article>
    </section>

    <section v-if="tab === 'audit'" class="card stack">
      <h2>操作审计</h2>
      <table>
        <thead><tr><th>时间</th><th>管理员</th><th>动作</th><th>对象</th><th>说明</th></tr></thead>
        <tbody>
          <tr v-for="row in auditLogs" :key="row.id">
            <td>{{ formatTime(row.created_at) }}</td>
            <td>{{ row.actor_id ?? "—" }}</td>
            <td>{{ row.action }}</td>
            <td>{{ row.target }}</td>
            <td>{{ row.detail }}</td>
          </tr>
        </tbody>
      </table>
      <h2>同意记录</h2>
      <div class="row">
        <input v-model="consentUserID" placeholder="用户 ID" />
        <button class="btn" type="button" @click="loadConsents">查询</button>
      </div>
      <table v-if="consentRows.length">
        <thead><tr><th>时间</th><th>文档</th><th>版本</th><th>事项</th><th>IP</th></tr></thead>
        <tbody>
          <tr v-for="row in consentRows" :key="row.id">
            <td>{{ formatTime(row.created_at) }}</td>
            <td>{{ row.doc_type }}</td>
            <td>{{ row.version }}</td>
            <td>{{ row.consent_item }}</td>
            <td>{{ row.ip }}</td>
          </tr>
        </tbody>
      </table>
    </section>

    <section v-if="tab === 'legal'" class="stack">
      <article class="card stack" style="max-width: 720px">
        <h2>发布新版本</h2>
        <p class="muted">用户协议和隐私政策勾选「要求重新同意」后，已登录用户要重新勾选才能继续用。旧版本仍可在协议页打开。</p>
        <select v-model="legalForm.doc_type">
          <option value="user_agreement">用户协议</option>
          <option value="privacy">隐私政策</option>
          <option value="disclaimer">免责声明</option>
          <option value="membership">会员与退款</option>
          <option value="content_rules">内容规范</option>
          <option value="invite_rules">邀请规则</option>
          <option value="page_home">页面 · 首页补充</option>
          <option value="page_help">页面 · 帮助补充</option>
          <option value="page_about">页面 · 关于</option>
        </select>
        <input v-model="legalForm.version" placeholder="版本号，如 0.2" />
        <input v-model="legalForm.title" placeholder="标题" />
        <input v-model="legalForm.effective_date" placeholder="生效日期 YYYY-MM-DD，可空" />
        <textarea v-model="legalForm.content" rows="8" placeholder="正文"></textarea>
        <label><input v-model="legalForm.require_reconsent" type="checkbox" /> 重大变更，要求重新同意</label>
        <button class="btn" type="button" @click="publishLegal">发布</button>
      </article>
      <article class="card stack">
        <h2>已有版本</h2>
        <table>
          <thead><tr><th>类型</th><th>版本</th><th>状态</th><th>标题</th></tr></thead>
          <tbody>
            <tr v-for="row in legalDocs" :key="row.doc_type + row.version">
              <td>{{ row.doc_type }}</td>
              <td><router-link :to="`/legal/${row.doc_type}?version=${encodeURIComponent(row.version)}`">{{ row.version }}</router-link></td>
              <td>{{ row.status }}</td>
              <td>{{ row.title }}</td>
            </tr>
          </tbody>
        </table>
      </article>
    </section>

    <section v-if="tab === 'invite'" class="stack">
      <article class="card stack" style="max-width: 720px">
        <h2>邀请规则</h2>
        <p class="muted">免费用户才加篇。付费期间关系保留，降回免费后恢复。虚拟号、同设备、同一 IP 段集中注册不会变成有效邀请。</p>
        <label>每个有效邀请加篇 <input v-model.number="inviteRules.reward_per" type="number" min="0" /></label>
        <label>加篇上限 <input v-model.number="inviteRules.reward_cap" type="number" min="0" /></label>
        <label>免费月额度上限 <input v-model.number="inviteRules.month_max" type="number" min="1" /></label>
        <label>每人每天有效邀请上限 <input v-model.number="inviteRules.daily_cap" type="number" min="1" /></label>
        <label>观察天数 <input v-model.number="inviteRules.observe_days" type="number" min="0" /></label>
        <label>成功草稿篇数 <input v-model.number="inviteRules.drafts_need" type="number" min="1" /></label>
        <button class="btn" type="button" @click="saveInviteRules">保存规则</button>
      </article>
      <article class="card stack">
        <h2>审核队列</h2>
        <table>
          <thead><tr><th>邀请人</th><th>被邀请人</th><th>状态</th><th>原因</th><th></th></tr></thead>
          <tbody>
            <tr v-for="row in referrals" :key="row.id">
              <td>{{ row.inviter }}</td>
              <td>{{ row.invitee }}</td>
              <td>{{ row.status }}</td>
              <td>{{ row.reason }}</td>
              <td>
                <button class="text-btn" type="button" @click="reviewReferral(row, 'approve')">通过</button>
                <button class="text-btn" type="button" @click="reviewReferral(row, 'revoke')">撤销</button>
              </td>
            </tr>
          </tbody>
        </table>
      </article>
    </section>

    <section v-if="tab === 'hot'" class="stack">
      <article class="card stack" style="max-width: 720px">
        <h2>热点源</h2>
        <p class="muted">支持 HTTP JSON 和远程 MCP。没添加启用的来源时，写作仍用常青选题。用户任务只读缓存。</p>
        <label>名称 <input v-model="hotForm.name" /></label>
        <label>协议
          <select v-model="hotForm.protocol">
            <option value="http">HTTP JSON</option>
            <option value="mcp">MCP tools/call</option>
          </select>
        </label>
        <label>地址 <input v-model="hotForm.base_url" placeholder="https://example.com/hot" /></label>
        <label>密钥（留空表示不修改） <input v-model="hotForm.api_key" type="password" /></label>
        <label>垂类标识（可空，空表示各方向都能用） <input v-model="hotForm.vertical" /></label>
        <label>缓存小时 <input v-model.number="hotForm.cache_hours" type="number" min="1" /></label>
        <label class="check"><input v-model="hotForm.enabled" type="checkbox" /> 启用</label>
        <button class="btn" type="button" @click="saveHot">保存热点源</button>
      </article>
      <article class="card stack">
        <table>
          <thead><tr><th>名称</th><th>协议</th><th>地址</th><th>状态</th><th></th></tr></thead>
          <tbody>
            <tr v-for="row in hotSources" :key="row.id">
              <td>{{ row.name }}</td>
              <td>{{ row.protocol }}</td>
              <td>{{ row.base_url }}</td>
              <td>{{ row.enabled ? "启用" : "停用" }}</td>
              <td>
                <button class="text-btn" type="button" @click="testHot(row)">测试</button>
                <button class="text-btn" type="button" @click="removeHot(row)">删除</button>
              </td>
            </tr>
          </tbody>
        </table>
        <p v-if="hotPreview" class="muted">{{ hotPreview }}</p>
      </article>
    </section>

    <section v-if="tab === 'checks'" class="card stack">
      <h2>模板实测记录</h2>
      <p class="muted">每个模板在浅色和暗色都留下一条通过记录后，才显示为已实测。这里只保存记录，不代替你在微信里打开稿件看过。</p>
      <label>模板标识<input v-model="checkForm.layout_key" /></label>
      <label>机型<input v-model="checkForm.device" placeholder="例如 iPhone" /></label>
      <label>主题
        <select v-model="checkForm.theme">
          <option value="light">浅色</option>
          <option value="dark">暗色</option>
        </select>
      </label>
      <label>结果
        <select v-model="checkForm.result">
          <option value="pass">通过</option>
          <option value="fail">未通过</option>
        </select>
      </label>
      <label>备注<input v-model="checkForm.note" /></label>
      <div class="row">
        <button class="btn" type="button" @click="saveCheck">保存记录</button>
        <button class="btn-ghost" type="button" @click="loadChecks">刷新</button>
      </div>
      <p v-for="row in checks" :key="row.id" class="muted">{{ row.layout_key }} · {{ row.device }} · {{ row.theme === 'dark' ? '暗色' : '浅色' }} · {{ row.result === 'pass' ? '通过' : '未通过' }} · {{ row.note }}</p>
    </section>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from "vue";
import { api, formatTime } from "../api";

const tabs = [
  { key: "services", label: "服务" },
  { key: "users", label: "会员" },
  { key: "models", label: "模型" },
  { key: "routes", label: "环节路由" },
  { key: "wechat", label: "微信" },
  { key: "words", label: "敏感词" },
  { key: "tasks", label: "任务" },
  { key: "complaints", label: "投诉" },
  { key: "orders", label: "订单" },
  { key: "library", label: "内容库" },
  { key: "board", label: "看板" },
  { key: "audit", label: "审计" },
  { key: "legal", label: "协议" },
  { key: "invite", label: "邀请" },
  { key: "hot", label: "热点" },
  { key: "checks", label: "实测" },
];
const tab = ref("models");
const checks = ref([]);
const checkForm = reactive({ layout_key: "simple", device: "", theme: "light", result: "pass", note: "" });
const error = ref("");
const message = ref("");
const integrations = ref([]);
const integrationForm = reactive({});
const registerCfg = reactive({ verify: false, reason: "", note: "" });
const probePhone = ref("");
const users = ref([]);
const planConfigs = ref([]);
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
const tasks = ref([]);
const complaints = ref([]);
const payOrders = ref([]);
const styles = ref([]);
const templates = ref([]);
const verticals = ref([]);
const newStyle = reactive({ key: "", name: "", description: "", tone: "", min_tier: "free", enabled: true });
const newTemplate = reactive({ key: "", name: "", description: "", min_tier: "free", enabled: true });
const newVertical = reactive({ key: "", name: "", keywords: "", subtopics: "", red_lines: "", source_allow: "", image_rules: "", disclaimer: "", enabled: true });
const replies = reactive({});
const metrics = ref(null);
const auditLogs = ref([]);
const legalDocs = ref([]);
const referrals = ref([]);
const inviteRules = reactive({ reward_per: 2, reward_cap: 20, month_max: 30, daily_cap: 3, observe_days: 7, drafts_need: 3 });
const hotSources = ref([]);
const hotPreview = ref("");
const hotForm = reactive({ name: "", protocol: "http", base_url: "", api_key: "", vertical: "", cache_hours: 12, enabled: true });
const consentUserID = ref("");
const consentRows = ref([]);
const legalForm = reactive({
  doc_type: "user_agreement",
  version: "",
  title: "",
  content: "",
  effective_date: "",
  require_reconsent: true,
});
const costForm = reactive({
  chat_cents_per_1k: 2,
  image_cents: 10,
  search_cents: 5,
  month_cap_cents: 0,
  action: "degrade",
  ai_label: false,
});
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

async function saveCost() {
  error.value = "";
  message.value = "";
  try {
    const saved = await api("/api/admin/cost", { method: "PUT", body: costForm });
    Object.assign(costForm, saved);
    message.value = "已保存成本和标识开关";
    metrics.value = await api("/api/admin/metrics");
    auditLogs.value = await api("/api/admin/audit-logs");
  } catch (err) {
    error.value = err.message;
  }
}

async function publishLegal() {
  error.value = "";
  message.value = "";
  try {
    await api("/api/admin/legal", { method: "POST", body: legalForm });
    message.value = "已发布 " + legalForm.version;
    legalDocs.value = await api("/api/admin/legal");
    auditLogs.value = await api("/api/admin/audit-logs");
  } catch (err) {
    error.value = err.message;
  }
}

async function loadChecks() {
  error.value = "";
  checks.value = await api("/api/admin/layout-checks");
}
async function saveCheck() {
  error.value = "";
  message.value = "";
  try {
    const saved = await api("/api/admin/layout-checks", { method: "POST", body: checkForm });
    message.value = saved.measured ? "浅色和暗色都已有通过记录" : "已记下。浅色和暗色都通过后才算实测完成";
    await loadChecks();
  } catch (err) {
    error.value = err.message;
  }
}
async function loadConsents() {
  error.value = "";
  try {
    consentRows.value = await api("/api/admin/consents?user_id=" + encodeURIComponent(consentUserID.value));
  } catch (err) {
    error.value = err.message;
  }
}

function fillIntegrations(data) {
  integrations.value = data.sections || [];
  Object.assign(registerCfg, data.register || { verify: false, reason: "", note: "" });
  for (const section of integrations.value) {
    if (!integrationForm[section.key]) integrationForm[section.key] = {};
    for (const field of section.fields) {
      integrationForm[section.key][field.key] = field.secret ? "" : (field.value || "");
    }
  }
}
async function saveIntegration(section) {
  error.value = "";
  message.value = "";
  try {
    const data = await api(`/api/admin/integrations/${section.key}`, { method: "PUT", body: { fields: integrationForm[section.key] } });
    fillIntegrations(data);
    message.value = section.title + "已保存";
  } catch (err) {
    error.value = err.message;
  }
}
async function saveRegisterVerify() {
  error.value = "";
  message.value = "";
  try {
    const data = await api("/api/admin/integrations/register", { method: "PUT", body: { fields: { verify: registerCfg.verify ? "1" : "0" } } });
    fillIntegrations(data);
    message.value = "注册验证码开关已保存";
  } catch (err) {
    error.value = err.message;
  }
}
async function testIntegration(section) {
  error.value = "";
  message.value = "";
  try {
    const data = await api(`/api/admin/integrations/${section.key}/test`, { method: "POST", body: { phone: probePhone.value } });
    message.value = data.message || "连通成功";
  } catch (err) {
    error.value = err.message;
  }
}
async function load() {
  planConfigs.value = await api("/api/admin/plan-configs");
  users.value = await api("/api/admin/users");
  for (const user of users.value) {
    edits[user.id] = edits[user.id] || { tier: user.tier, expires: "", note: "", company: "", contract_no: "", contract_note: "" };
    edits[user.id].tier = user.tier;
    edits[user.id].company = user.company || "";
    edits[user.id].contract_no = user.contract_no || "";
    edits[user.id].contract_note = user.contract_note || "";
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
  tasks.value = await api("/api/admin/tasks");
  complaints.value = await api("/api/admin/complaints");
  payOrders.value = await api("/api/admin/orders");
  styles.value = await api("/api/admin/styles");
  templates.value = await api("/api/admin/templates");
  verticals.value = await api("/api/admin/verticals");
  metrics.value = await api("/api/admin/metrics");
  if (metrics.value?.cost) Object.assign(costForm, metrics.value.cost);
  auditLogs.value = await api("/api/admin/audit-logs");
  legalDocs.value = await api("/api/admin/legal");
  referrals.value = await api("/api/admin/referrals");
  Object.assign(inviteRules, await api("/api/admin/invite-rules"));
  hotSources.value = await api("/api/admin/hot-sources");
  fillIntegrations(await api("/api/admin/integrations"));
}

async function saveInviteRules() {
  error.value = "";
  message.value = "";
  try {
    Object.assign(inviteRules, await api("/api/admin/invite-rules", { method: "PUT", body: inviteRules }));
    message.value = "邀请规则已保存";
  } catch (err) {
    error.value = err.message;
  }
}

async function reviewReferral(row, action) {
  error.value = "";
  try {
    await api(`/api/admin/referrals/${row.id}/${action}`, { method: "POST", body: {} });
    referrals.value = await api("/api/admin/referrals");
    message.value = action === "approve" ? "已通过" : "已撤销";
  } catch (err) {
    error.value = err.message;
  }
}

async function saveHot() {
  error.value = "";
  message.value = "";
  try {
    await api("/api/admin/hot-sources", {
      method: "POST",
      body: {
        name: hotForm.name,
        protocol: hotForm.protocol,
        base_url: hotForm.base_url,
        api_key: hotForm.api_key,
        enabled: hotForm.enabled,
        extra_params: { vertical: hotForm.vertical, cache_hours: Number(hotForm.cache_hours || 12) },
      },
    });
    hotForm.api_key = "";
    hotSources.value = await api("/api/admin/hot-sources");
    message.value = "热点源已保存";
  } catch (err) {
    error.value = err.message;
  }
}

async function testHot(row) {
  error.value = "";
  hotPreview.value = "";
  try {
    const data = await api(`/api/admin/hot-sources/${row.id}/test`, { method: "POST", body: {} });
    hotPreview.value = `返回 ${data.count} 条`;
  } catch (err) {
    error.value = err.message;
  }
}

async function removeHot(row) {
  error.value = "";
  try {
    await api(`/api/admin/hot-sources/${row.id}`, { method: "DELETE" });
    hotSources.value = await api("/api/admin/hot-sources");
  } catch (err) {
    error.value = err.message;
  }
}

async function savePlanConfig(item) {
  error.value = "";
  message.value = "";
  try {
    await api(`/api/admin/plan-configs/${item.tier}`, {
      method: "PUT",
      body: { daily_quota: Number(item.daily_quota), period_quota: Number(item.period_quota) },
    });
    message.value = "套餐额度已保存";
    await load();
  } catch (err) {
    error.value = err.message;
  }
}

async function refundOrder(item) {
  error.value = "";
  message.value = "";
  try {
    const saved = await api(`/api/admin/orders/${item.id}/refund`, { method: "POST" });
    message.value = saved.message || "已标记退款";
    await load();
  } catch (err) {
    error.value = err.message;
  }
}

async function replyComplaint(item) {
  error.value = "";
  message.value = "";
  try {
    await api(`/api/admin/complaints/${item.id}`, {
      method: "PUT",
      body: { status: "closed", reply: replies[item.id] || item.reply || "" },
    });
    message.value = "已回复";
    await load();
  } catch (err) {
    error.value = err.message;
  }
}

async function setExempt(user, quotaExempt) {
  error.value = "";
  message.value = "";
  try {
    await api(`/api/admin/users/${user.id}/quota-exempt`, { method: "PUT", body: { quota_exempt: quotaExempt } });
    message.value = quotaExempt ? "已设为测试账号，不限额" : "已取消测试账号不限额";
    await load();
  } catch (err) {
    error.value = err.message;
  }
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

async function saveContract(user) {
  error.value = "";
  message.value = "";
  const edit = edits[user.id];
  try {
    await api(`/api/admin/users/${user.id}/contract`, {
      method: "PUT",
      body: { company: edit.company || "", contract_no: edit.contract_no || "", note: edit.contract_note || "" },
    });
    message.value = "合同字段已保存";
  } catch (err) {
    error.value = err.message;
  }
}

async function saveTier(user) {
  error.value = "";
  message.value = "";
  const edit = edits[user.id];
  try {
    const saved = await api(`/api/admin/users/${user.id}/tier`, {
      method: "PUT",
      body: {
        tier: edit.tier,
        expires_at: edit.expires ? `${edit.expires}:00+08:00` : null,
        note: edit.note,
        when: edit.later ? "period_end" : "now",
      },
    });
    message.value = saved.pending_tier ? "已安排到期后生效" : "档位已更新";
    await load();
  } catch (err) {
    error.value = err.message;
  }
}

async function saveStyle(item) {
  error.value = "";
  message.value = "";
  try {
    styles.value = await api("/api/admin/styles", { method: "PUT", body: item });
    newStyle.key = "";
    newStyle.name = "";
    message.value = "风格已保存";
  } catch (err) {
    error.value = err.message;
  }
}

async function saveTemplate(item) {
  error.value = "";
  message.value = "";
  try {
    templates.value = await api("/api/admin/templates", { method: "PUT", body: item });
    newTemplate.key = "";
    newTemplate.name = "";
    message.value = "模板已保存";
  } catch (err) {
    error.value = err.message;
  }
}

async function saveVertical(item) {
  error.value = "";
  message.value = "";
  try {
    verticals.value = await api("/api/admin/verticals", { method: "PUT", body: item });
    newVertical.key = "";
    newVertical.name = "";
    message.value = "垂类已保存";
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
