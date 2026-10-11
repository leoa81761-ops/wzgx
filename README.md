# 公号智写

按《项目开发书：公众号自动写稿智能体》做的 **P0 内测版**。后端是单个 Go 二进制，前端是 Vue，构建后嵌进同一个进程。用户设定写作方向和周期后，系统定时生成文章，并写入公众号草稿箱；写不进去时改为导出可复制的排版 HTML。

模型密钥和公众号密钥都由管理员配置，用户不接触 API Key。

## 已实现（P0）

- 手机号或邮箱 + 密码注册登录。注册必须主动勾选同意《用户协议》《隐私政策》，同意写入 `consent_log`（条款版本、时间、IP、浏览器标识）。微信扫码登录只有禁用按钮，没有实现。
- 协议页：用户协议、隐私政策、免责声明、会员与退款、内容规范、邀请规则。文首都标明 **草案 · 待法务审核**。
- 公众号绑定：选择个人/企业、订阅号/服务号、是否认证；加密保存 AppSecret；检测 `stable_token`、草稿数量、封面素材、草稿写入与读取，并删除检测草稿。检测失败时改为手动导出。同一 AppID 不能同时绑在两个账号上，解绑后 30 天内不能被其他账号绑定。
- 全托管写作计划：只选方向、基础风格（3 种）、基础排版（3 套）、每天或每周、时段窗口。每个公众号每天最多 1 批；同一账号有多个方向时按日期轮换。免费版固定夜间低峰（0:00–6:00，北京时间）。
- 定时扫描：默认每分钟查找到期计划。PostgreSQL 使用 `FOR UPDATE SKIP LOCKED` 领取任务。全局并发是可配置的 goroutine 池（`MAX_CONCURRENCY`），生成过程大部分时间在等模型和微信，不占着数据库连接。
- 分阶段写作：选题 → 大纲 → 正文 → 审核 → 结构化 JSON → 内联样式 HTML。主题按「子方向 + 栏目 + 角度」去重，90 天内尽量不重复。
- 本地敏感词审核。不通过会再请模型改写一次，仍然命中则任务失败并退回额度。正文之后还有一次事实核对：对照检索来源和领域常识改掉错误的定义和数字，来源没有支撑的阈值、电压和标准编号不写进正文。
- 草稿推送：`/cgi-bin/stable_token`、永久封面素材 `/cgi-bin/material/add_material?type=image`、正文图 `/cgi-bin/media/uploadimg`、`/cgi-bin/draft/add`。重写会重新生成并上传封面。旧草稿还在时走 `/cgi-bin/draft/update`；如果微信返回 40007 或其它 media_id / thumb_media_id 无效，就重新上传封面并用 `/cgi-bin/draft/add` 新建一篇，记下新的 `media_id`。新增草稿时封面无效也会重新上传一次再提交。没有配图服务时用程序生成的默认封面（草稿接口要求 `thumb_media_id`）。推送失败则保留 HTML，状态为「已导出」。
- 额度：任务创建时预占，成功确认，失败退回。免费版每天 1 篇、每月 10 篇。普通 / 高级 / 买断的额度按开发书第 4 章写入套餐表。每篇第一次重写不占额度。到期后有 7 天宽限期，宽限期内不能新增公众号和方向；宽限期结束按免费版保留较早的计划。
- 文章列表：状态、预览、失败原因、重新生成、复制 / 下载 HTML。浅色稿是实际推送的 HTML；暗色只供站内预览。
- 管理配置中心：多个模型供应商、环节回退顺序、微信模拟/正式开关、出口 IP、补充敏感词、手工开通会员。没有在线支付。
- 真实连通测试：命令行 `gonghao probe`，以及管理后台微信页的「真实连通测试」。用真实 AppID / AppSecret 和真实模型走一遍写作，并把一篇草稿留在公众号草稿箱。

## 第一批 P1（不依赖商户号）

- 失败通知：任务最终失败，或高风险稿没有写入草稿时，站内「通知」记一条。`NOTIFY_WEBHOOK_URL` 若配置了，会再 POST 一份 JSON（`internal/notify.Sender`）。没配地址就只打日志。邮件服务以后实现同一个接口即可。
- 反馈：文章页可以记「已发布 / 修改后发布 / 弃用」。
- 草稿回读：`POST /api/articles/{id}/reread` 调微信 `draft/get`，比较标题和正文长度。结果是未改、已改或读不到。读不到不等于已发布。
- 运营后台：配置中心增加最近任务和投诉回复。
- 内容日历：按现有轮换规则预告接下来几次选题，不额外调用模型。
- 风险分级：剂量、诊断、偏方、处方、治疗方案、包治为高风险，不写入草稿。母婴方向里出现喂养、睡眠、疫苗、健康，或正文出现投资建议、收益率为中风险。免费版只推送低风险；付费档的中风险需要带来源。
- 手动干预：写作计划可以指定下一篇题目（成功后清除），也可以否决「子方向|栏目|角度」。
- 帮助中心、投诉入口、定价页。定价展示套餐，购买按钮禁用并写「即将开通」。
- 账号注销会立刻删除公众号密钥和账号数据。`GET /api/me/export` 导出 JSON，不含 AppSecret。
- 支付只留接口：`internal/pay.Provider`。当前 `manual` 的 `Create` 返回「即将开通」，`POST /api/billing/checkout` 是 501。微信支付和支付宝以后实现这个接口再注册，不要把商户密钥写进仓库。认证号自动发布仍未做。

## 没有实现（相对开发书）

这些留在后面，代码里没有假装已经可用：

- 在线支付扣款、订单、邀请奖励、加量包（只有上面的支付接口）
- 微信开放平台扫码授权、微信扫码登录、手机短信验证码
- 企业认证号自动发布
- 热点、表格、垂类规则包后台
- 风格库和模板的可视化编辑、站内手机预览与推送稿完全同一套暗色实测
- 正文 30 天自动清理
- 宣传页里的绝对化承诺。落地页和定价页只说明已实现的能力

P0 里每个公众号每天仍然只有 1 批。高级版「一个号一天多篇」需要和多图文合并一起做，没有提前放开。

## 模型网关

业务只调用统一的补全接口。管理员可以添加多个文本模型，每个模型单独配置：

| 字段 | 说明 |
| --- | --- |
| 用途 | 文本写作，或封面生图 |
| 协议 | `openai`：Chat Completions。`responses`：OpenAI Responses API（`/v1/responses`）。`anthropic`：Messages API。封面生图固定走 OpenAI 兼容的 `/v1/images/generations` |
| Base URL | 自定义地址。`https://host/v1` 会补成 `/chat/completions`、`/responses` 或 `/images/generations`；`anthropic` 在没有 `/v1` 时补 `/v1/messages`。已经写到完整路径则原样使用 |
| API Key | 加密后存进 `service_config`。界面只显示末四位。留空表示修改其他字段时不更换密钥 |
| 模型名 | 写入请求体的 `model`。即使额外参数里也带了 `model`，也以这个字段为准 |
| 推理档位 | 不发送 / `low` / `medium` / `high`。会写成请求里的 `reasoning_effort`。没选时，大纲、正文、审核、扩写、缩写默认 `low`；选题和资料检索不默认加档位。`grok-chat-fast` 这类名字里带 `chat-fast` 或 `non-reasoning` 的模型不发送这个字段。推理模型不要再在额外参数里放很小的 `max_tokens` |
| 超时 | 5 到 600 秒。流式响应用来避开前置代理大约 120 秒的空闲切断。长环节默认 `low`，尽量在窗口内吐出第一个字 |
| 额外请求头 | JSON 对象，合并进请求头，可以覆盖适配器默认的 `Authorization` 或 `x-api-key` |
| 额外参数 | JSON 对象，合并进请求体，例如 `{"temperature": 0.7}`。以 `_` 开头的键是内部保留字段，不会发给上游 |

选题、大纲、正文、审核的提示词写明了要输出的 JSON Schema。`openai` 和 `responses` 会自动带上 `response_format` / `text.format` 的 `json_schema`（`strict: true`）。模型如果忽略它、返回 Markdown，程序仍会从文本里抽出 JSON。

资料检索用单独的提示词：要求联网搜索，返回 3 到 5 条最近、相关的来源，每条带网址和简要说明，并优先政府网站（`.gov.cn`）、标准组织和主流媒体，不要聚合站或内容农场。这一步不强制 `json_schema`（有的检索模型会忽略 strict schema，并交出空的 sources）。请求使用非流式（`stream: false`），单次超时 90 秒，网关内部不再重试。没有可用网址或请求失败时，最多再检索一次，然后继续写稿，不带来源。同一环节里，模型名包含 `chat-fast` 的会排到最前面再按原回退顺序调用，避免推理模型先空等满 90 秒。JSON、Markdown 链接和裸网址都会提取。来源交给大纲和正文，并写入文末「参考来源」；网址里的「.」写成「点」。默认丢掉 `xilu.com`、`fyun.com`、`huanancj.com`。配置中心「敏感词」页可以改屏蔽名单和允许名单；允许名单不为空时只保留这些域名。还没保存过时用默认屏蔽名单，保存成空名单则不再屏蔽。

正文字数只统计各小节的段落、列表和结尾，不含标题和摘要。目标区间是 `word_target` 的 85% 到 130%。偏短时用同一写作模型扩写一次，偏长时缩写一次。模型缩写后仍然超过 130%，就从文末裁到上限。审核之后如果又写超，同样裁到 130%，不再次调用模型。

正文、扩写、缩写和审核都要求：来源里没有的阈值、电压、标准编号和统计数字不要编造。正文定稿前还有一步「事实核对」，用审核环节的模型对照 `research_notes`、`research_sources` 和领域常识改写定义与数字。例如建筑智能化里的「弱电」是通信、安防、楼宇自控、火灾报警、音视频等电子系统（常见 36V 及以下的安全特低电压或信号电平），不是低压配电的 1000V。核对失败不会让整篇文章失败，会留下追踪并继续用当前正文做敏感词审核。

环节路由在「配置中心 → 环节路由」。选题、资料检索、大纲、正文、审核可以各自指定一串模型，**顺序就是回退顺序**：前一个超时、HTTP 错误或返回的不是合法 JSON，就换下一个。路由可以按会员档位区分；某一档没有专属路由时，使用「全部档位」。资料检索是例外：没有单独配置就不会联网，也不会借用其它环节的模型。免费档不检索。

如果一个环节既没有路由，也没有启用的模型：

- `LLM_MODE=mock`（默认）：使用内置模拟写手，不访问网络，本地和测试能走完「立即生成」。
- `LLM_MODE=live`：任务失败并退回额度，提示管理员先配置模型。

选题、大纲、正文、审核使用流式响应（`stream: true`），带超时和指数退避。资料检索固定非流式，并且只有上面说的那一次额外尝试。429 / 5xx / 网络错误会重试；模型把内容返回成无法解析的 JSON 时不重试，直接换下一个供应商。Chat Completions 不会擅自加上很小的 `max_tokens`。Anthropic 在管理员没写 `max_tokens` 时用 16384，避免推理把额度花完后没有正文。`access_token` 和后台配置都有进程内缓存。

`https://ai.1so.org` 的文档说明：Cloudflare 大约 120 秒没有字节就会返回 524，流式响应每收到一块数据就刷新这个窗口，并建议能选的话优先用 Responses。grok-4.7 在 `medium` 推理时，短提示也要大约 87 秒才吐出第一个字，真实提示会越过这个窗口，表现为 HTTP 524 或 HTTP/2 `stream error INTERNAL_ERROR`。长环节因此默认 `low`。如果调用仍然被这种空闲切断打断，会立刻再试一次（不再按指数退避空等）：推理档位降到 `low`；原来走 Chat Completions 的推理模型（名字里有 `grok-4`、`grok-composer` 或 `grok-build`，且不是生图或 `chat-fast`）改走 Responses。Responses 流里的 `response.reasoning_summary_text.delta` 和 `response.reasoning_text.delta` 会被读走以保持连接，但不会写进正文。该站文档没有列出这两个事件名；只要上游按 OpenAI Responses 发送，就会被接受。已经降过档、或无法再降时，马上换下一个供应商。每一次失败尝试都写入 `model_trace`（供应商、模型、实际协议、推理档位、错误）。任务最终失败时这些记录也会留下。

「立即生成」和「重新生成」只负责入队，接口马上返回「生成中」。文章页和文章列表会轮询，完成后刷新。任务的 `duration_ms` 从开始写到结束，而不是只记入队那一瞬间。

## 封面和资料检索

封面：在模型里把用途选成「封面生图」，协议用 `openai`，Base URL 写到 `/v1`，模型名填生图模型。请求会带 `aspect_ratio: 16:9` 和 `response_format: b64_json`（上游图片 URL 经 Cloudflare 下载时常 502）。返回 base64 时直接使用；如果上游仍给了 URL，再尝试下载。程序按文件头区分 JPEG 和 PNG，上传文件名为 `cover.jpg` 或 `cover.png`。大约超过 2MB 会重新编码，超过微信 10MB 上限则放弃这张图。任务追踪会记下封面是模型生成还是内置图、字节数、文件名，以及失败原因。失败时改用内置纯色封面，文章仍然可以进草稿箱。

资料检索：给「资料检索」单独配一个会联网的文本模型，例如 `grok-chat-fast`。如果路由里同时有推理模型和 `chat-fast`，会先调用 `chat-fast`。付费档会在选题之后先检索。单次最多等 90 秒，失败或没有可用网址时再试一次，然后继续写稿。没配这个环节就跳过，不因此失败。检索追踪会写下模型名和供应商名称，失败的那一次也会留下。屏蔽名单和允许名单在「敏感词」页。

重写：每次都重新做封面并上传。旧的 `draft_media_id` 还有效时更新那一篇；微信返回 40007（素材 media_id 无效）或其它 media_id / thumb_media_id 无效时，重新上传封面并新建草稿，文章改记新的 `media_id`。这样草稿被删掉或已经发布后，重写仍然能进草稿箱，而旧稿还在时也不会多留一份过期副本。

配置中心的用户列表可以停用或删除测试账号。停用后无法登录。删除会清掉该用户的公众号、计划、文章和额度记录。不能停用或删除当前登录的管理员，也不能动最后一位管理员。

以 `https://ai.1so.org` 为例（密钥只放后台或环境变量）：

| 用途 | 协议 | Base URL | 模型 |
| --- | --- | --- | --- |
| 选题 / 大纲 / 正文 / 审核 | `openai` 或 `responses` | `https://ai.1so.org/v1` | `grok-4.7`。大纲、正文、审核在管理员没指定更高档位时发送 `reasoning_effort=low`。被 524 或流中断切断后，推理模型会改用 Responses 再试一次 |
| 资料检索 | `openai` | `https://ai.1so.org/v1` | `grok-chat-fast`。路由里如果还有别的模型，名字里带 `chat-fast` 的会先调用 |
| 封面生图 | `openai`（用途选封面） | `https://ai.1so.org/v1` | `grok-imagine-image-2.0` |

## 本地运行

需要 Go 1.26 和 Node 22（只在构建前端时需要）。

```bash
cd frontend && npm ci && npm run build && cd ..
mkdir -p data
DATABASE_URL=sqlite:///./data/dev.db \
MASTER_KEY=dev-master-key-change-me \
JWT_SECRET=dev-jwt-secret-change-me \
SCHEDULER_ENABLED=1 \
LLM_MODE=mock \
WECHAT_MODE=mock \
go run ./cmd/gonghao serve
```

浏览器打开 `http://127.0.0.1:8000`。前端开发也可以另开一个终端，在 `frontend/` 里运行 `npm run dev`（Vite 会把 `/api` 代理到 8000）。没执行 `npm run build` 时，二进制里只有占位页，接口仍然可用。

默认管理员：`admin@example.com` / `admin123456`。只在第一次启动、该邮箱还不存在时创建。之后改环境变量不会重置密码。

## Docker Compose

在仓库根目录：

```bash
cp .env.example .env
docker compose up --build
```

浏览器打开 `http://127.0.0.1:8000`。数据库数据在卷 `pgdata`。`db` 和 `app` 都设置了 `restart: unless-stopped`，服务器重启后两个服务会自己拉起来。镜像是多阶段构建：Node 编译前端，Go 编译静态二进制，最终运行在 distroless 里。健康检查调用 `gonghao healthcheck`。

默认 `LLM_MODE=mock`、`WECHAT_MODE=mock`，不需要真实密钥就能：注册 → 绑定任意 AppID → 设定方向 → 立即生成 → 看到「已入草稿箱」。

## 用个人订阅号做一次真实草稿

下面这件事会在你的草稿箱里留下一篇真实文章。请使用个人订阅号，不要用还在给读者发文的号做第一次试验。

1. 登录 [微信公众平台](https://mp.weixin.qq.com/) → 设置与开发 → 基本配置，拿到 **AppID** 和 **AppSecret**。
2. 在同一页的 **IP 白名单** 里加入服务器的公网出口 IP。漏掉这一步时，接口会返回 `40164`，页面上会看到「服务器出口 IP 未加入公众号 IP 白名单」。本机试用时，白名单填的是运行 `gonghao` 的那台机器的公网 IP，不是家里路由器的内网地址。
3. 准备一个 OpenAI 兼容或 Anthropic 的模型。密钥只放在环境变量或管理后台，不要写进仓库。
4. 设置环境变量后执行连通测试。命令行只读环境变量，缺任何一项都会直接退出，不会去猜后台里的号：

```bash
export WX_APP_ID=你的AppID
export WX_APP_SECRET=你的AppSecret
export WECHAT_API_BASE=https://api.weixin.qq.com
export LLM_PROTOCOL=openai
export LLM_BASE_URL=https://你的网关/v1
export LLM_API_KEY=你的模型密钥
export LLM_MODEL=你的模型名
export PROBE_DIRECTION=职场成长
gonghao probe
```

`LLM_PROTOCOL=anthropic` 时，`LLM_BASE_URL` 写成到 `/v1` 或完整的 `/v1/messages` 都可以。`LLM_EXTRA_HEADERS` 是可选的 JSON 对象。

成功时会逐行打印：获取 access_token、选题、大纲 / 正文 / 审核、上传封面 `add_material`、上传正文图片 `uploadimg`、新增草稿 `draft/add`、读取草稿 `draft/get`。最后一句是「真实连通测试完成，草稿已留在公众号草稿箱。」请到公众平台草稿箱核对标题和封面。这篇草稿不会被自动删除。

也可以在已经 `WECHAT_MODE=live`、`LLM_MODE=live` 的服务里，用管理员打开配置中心 → 微信，点 **真实连通测试**。后台按钮在环境变量不全时，会用最近一个已绑定账号的 AppSecret、微信接口根地址，以及环节路由（或第一个启用的文本模型）补齐。失败原因显示在按钮下方。

常见错误会变成中文说明，而不是只留一个 errcode：

| errcode | 用户看到的原因 |
| --- | --- |
| 40164 | 服务器出口 IP 未加入公众号 IP 白名单 |
| 40001 / 42001 / 40014 | 凭证无效或 access_token 过期。程序会强制刷新一次 token 再重试 |
| 40125 / 40013 | AppSecret 或 AppID 无效 |
| 45009 | 微信接口调用次数已达上限 |
| 48001 | 该公众号没有此接口权限。绑定检测会改成手动导出，而不是把账号标成授权失效 |

没有配图服务时，封面是程序生成的纯色 PNG，经永久素材接口以 `cover.png` 上传。模型返回的 JPEG 会以 `cover.jpg` 上传。

没有真实密钥时，集成测试会跳过。本地用假的微信和模型服务核对请求格式：

```bash
go test ./...
```

带真实密钥时，同一条命令会额外跑 `TestLiveDraftWhenCredentialsPresent`（需要 `WX_APP_ID`、`WX_APP_SECRET`、`LLM_BASE_URL`、`LLM_API_KEY`、`LLM_MODEL`）。这条测试也会在草稿箱留下一篇稿。

## 4 核 4 GB 上能撑多少

生成大约九十秒，绝大部分时间在等外部接口，所以并发可以远高于 CPU 核数。默认 `MAX_CONCURRENCY=128`，允许 1 到 4096。下面是在 **4 CPU、4 GiB、关闭 swap** 的限制里测到的数字。

这台环境没有 Docker，限制是用 cgroup v2 做的，和 `docker run --cpus=4 --memory=4g --memory-swap=4g` 同一类约束：

- `cpu.max = 400000 100000`
- `memory.max = 4294967296`
- `memory.swap.max = 0`

被测进程是 `gonghao serve`，数据库是 SQLite（WAL，写事务 `BEGIN IMMEDIATE`，最多 8 条连接），`GOMAXPROCS=4`，`GOMEMLIMIT=3GiB`。模型和微信是进程外的假服务，不占这 4 GiB。每个写作阶段等待 800ms（四段合计 3.2 秒），每次微信调用等待 20ms。内存取的是生成期间 cgroup `memory.current` 的采样峰值。

| 项目 | 每阶段 800ms（四段合计 3.2 秒） | 每阶段 5 秒（四段合计 20 秒，任务叠在一起等模型） |
| --- | --- | --- |
| `/api/health` 与 `/api/meta` 混合，64 并发，8 秒 | **29183 次/秒**，内存峰值 12.5 MB | **30347 次/秒**，内存峰值 11.6 MB |
| 同时发起的生成任务 | **4096 个全部成功** | **4096 个全部成功** |
| 墙钟时间 | 17.79 秒 | 33.40 秒 |
| 生成期间内存峰值 | **358 MiB**（375390208 字节） | **520 MiB**（544694272 字节） |
| 生成占用的 CPU | 四核能力的 22.6% | 四核能力的 10.1% |

第二列把模型等待拉长到 20 秒，4096 个任务会叠在「等外部接口」上。这时内存峰值是 520 MiB，CPU 更低。距离 4 GiB 仍有数倍余量。SQLite 同一时刻只能有一个写事务，所以墙钟长于模型时间，写队列是这轮压测的主要限制，不是内存，也不是 CPU。生产环境用 PostgreSQL，写入可以并行，等模型的并发方式相同。

有 Docker 时可以按同样的资源上限复现。压测程序在 `cmd/loadtest`：

```bash
go test ./...
go build -o /tmp/gonghao ./cmd/gonghao
go build -o /tmp/loadtest ./cmd/loadtest
# 先把 cgroup 限制成 4 CPU / 4 GiB / 无 swap，再：
/tmp/loadtest -server /tmp/gonghao -gen 4096 -api-seconds 8 -llm-delay 800ms -wx-delay 20ms
```

### 建议配置（一台 4 核 4 GB）

| 变量 | 建议 | 原因 |
| --- | --- | --- |
| `MAX_CONCURRENCY` | **1024** | 4096 路同时等模型时峰值约 520 MiB。1024 留给更长的真实正文。要再提高，直接改到 4096 即可 |
| `DB_MAX_OPEN_CONNS` | **16**（PostgreSQL） | 生成不占着连接等模型，16 条够用。SQLite 开发环境用 8 |
| `DB_MAX_IDLE_CONNS` | **8** | 大约是打开数的一半 |
| `GOMAXPROCS` | **4** | 跟机器核数一致。镜像里已经设了 |
| `GOMEMLIMIT` | **3GiB** | 给系统和 PostgreSQL 留出大约 1 GB。镜像里已经设了 |

Compose 和 `.env.example` 里的 `MAX_CONCURRENCY` 就是 1024。单机 `go run` 没设这个变量时，程序默认 128，避免在小机器上一次拉起太多任务。

## 配置项

| 变量 | 作用 |
| --- | --- |
| `DATABASE_URL` | `postgres://...` 或 `sqlite:///./data/dev.db`。也接受以前的 `postgresql+asyncpg://`、`sqlite+aiosqlite://` |
| `ADDR` | 监听地址，默认 `:8000`。只写 `PORT` 也可以 |
| `MASTER_KEY` | 加密公众号凭证和模型密钥。生产环境必须换成足够长的随机字符串。换了之后，已经保存的密钥解不开 |
| `JWT_SECRET` | 登录令牌，HS256，7 天 |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` | 首启管理员 |
| `WECHAT_MODE` | `mock` 或 `live`。配置中心可以覆盖 |
| `WECHAT_API_BASE` | 微信接口根地址，默认 `https://api.weixin.qq.com` |
| `LLM_MODE` | `mock` 或 `live` |
| `MAX_CONCURRENCY` | 同时进行的生成任务上限，1–4096 |
| `DB_MAX_OPEN_CONNS` / `DB_MAX_IDLE_CONNS` | 数据库连接池。0 表示按上面的默认 |
| `PUBLIC_EGRESS_IP` | 展示在绑定教程中，方便用户去填 IP 白名单 |
| `SCHEDULER_ENABLED` | 是否在进程内跑定时扫描 |
| `SCHEDULER_INTERVAL_SECONDS` | 扫描间隔，默认 60，最小 5 |
| `STALE_TASK_SECONDS` | 卡在「生成中」超过这个秒数就重新排队，默认 3600。推理模型单篇可能要好几分钟，不要改回几十秒 |
| `WX_APP_ID` / `WX_APP_SECRET` | 只给 `gonghao probe` 和后台真实连通测试用 |
| `LLM_PROTOCOL` / `LLM_BASE_URL` / `LLM_API_KEY` / `LLM_MODEL` | 连通测试使用的模型。协议是 `openai`、`responses` 或 `anthropic` |
| `LLM_TIMEOUT_SECONDS` | 连通测试的模型超时，默认 120 |
| `LLM_EXTRA_HEADERS` | 可选，JSON 对象 |
| `PROBE_DIRECTION` | 连通测试的写作方向，默认「职场成长」 |

切到正式微信和真实模型时：

1. 服务器要有固定公网出口 IP，填进 `PUBLIC_EGRESS_IP`，并加到公众号 IP 白名单。
2. 把 `MASTER_KEY`、`JWT_SECRET`、数据库密码换成随机值。
3. 在配置中心把微信模式改为「正式接口」，添加模型供应商，并把 `LLM_MODE=live`。
4. 对外访问请在前面加 HTTPS 反向代理。上线前还需要 ICP 备案和法务审过的协议，这些不在本镜像里。

## 测试

```bash
go test ./...
```

覆盖：写作流水线与主题去重、调度幂等和额度暂停、每日/每月额度与失败退回、微信客户端（模拟，以及用假 HTTP 核对 `stable_token`、永久素材、`uploadimg`、草稿新增/更新/读取的请求形状）、两种模型协议的流式请求和回退顺序、注册同意到「立即生成」进入草稿箱的接口流程、IP 白名单错误不会把账号标成授权失效。

## 建议的下一步（P1）

1. 用上面的真实连通测试，在个人订阅号草稿箱里核对一篇稿。
2. 管理员登记线下订单的完整页面，加上到期前提醒；再考虑在线支付。
3. 认证号自动发布（审阅窗口、发布前读草稿、禁止重复发布）。
4. 草稿回读和用户反馈，用来计算修改率。
5. 垂类规则包、内容日历、风格/模板后台。
6. 邀请奖励和防刷。
7. 法务替换协议文本；确定模型供应商是否出境，并补单独同意。
