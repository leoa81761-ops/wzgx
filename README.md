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
- 本地敏感词审核。不通过会再请模型改写一次，仍然命中则任务失败并退回额度。
- 草稿推送：`/cgi-bin/stable_token`、永久封面素材 `/cgi-bin/material/add_material?type=image`、正文图 `/cgi-bin/media/uploadimg`、`/cgi-bin/draft/add`。重写时走 `/cgi-bin/draft/update`，沿用原来的 `media_id`。没有配图服务时用程序生成的默认封面（草稿接口要求 `thumb_media_id`）。推送失败则保留 HTML，状态为「已导出」。
- 额度：任务创建时预占，成功确认，失败退回。免费版每天 1 篇、每月 10 篇。普通 / 高级 / 买断的额度按开发书第 4 章写入套餐表。每篇第一次重写不占额度。到期后有 7 天宽限期，宽限期内不能新增公众号和方向；宽限期结束按免费版保留较早的计划。
- 文章列表：状态、预览、失败原因、重新生成、复制 / 下载 HTML。浅色稿是实际推送的 HTML；暗色只供站内预览。
- 管理配置中心：多个模型供应商、环节回退顺序、微信模拟/正式开关、出口 IP、补充敏感词、手工开通会员。没有在线支付。
- 真实连通测试：命令行 `gonghao probe`，以及管理后台微信页的「真实连通测试」。用真实 AppID / AppSecret 和真实模型走一遍写作，并把一篇草稿留在公众号草稿箱。

## 没有实现（相对开发书）

这些留在 P1 / P2，代码里没有假装已经可用：

- 在线支付、订单、邀请奖励、加量包
- 微信开放平台扫码授权、微信扫码登录
- 企业认证号自动发布、草稿回读、修改检测
- 联网检索、热点、配图 / AI 生图、表格、垂类规则包后台、内容日历
- 风格库和模板的可视化编辑、站内手机预览与推送稿完全同一套暗色实测
- 账号注销、个人数据导出、投诉工单
- 正文 30 天自动清理
- 宣传页里的绝对化承诺。落地页只说明已实现的能力

P0 里每个公众号每天仍然只有 1 批。高级版「一个号一天多篇」需要和多图文合并一起做，没有提前放开。

## 模型网关

业务只调用统一的补全接口。管理员可以添加多个文本模型，每个模型单独配置：

| 字段 | 说明 |
| --- | --- |
| 协议 | `openai`：OpenAI 兼容 Chat Completions。`anthropic`：Anthropic Messages API |
| Base URL | 自定义地址。`https://host/v1` 会分别补成 `/chat/completions` 或 `/v1/messages`；如果已经写到完整路径则原样使用 |
| API Key | 加密后存进 `service_config`。界面只显示末四位。留空表示修改其他字段时不更换密钥 |
| 模型名 | 写入请求体的 `model`。即使额外参数里也带了 `model`，也以这个字段为准 |
| 额外请求头 | JSON 对象，合并进请求头，可以覆盖适配器默认的 `Authorization` 或 `x-api-key` |
| 额外参数 | JSON 对象，合并进请求体，例如 `{"temperature": 0.7}`。以 `_` 开头的键是内部保留字段，不会发给上游 |

环节路由在「配置中心 → 环节路由」。选题、大纲、正文、审核可以各自指定一串模型，**顺序就是回退顺序**：前一个超时、HTTP 错误或返回的不是合法 JSON，就换下一个。路由可以按会员档位区分；某一档没有专属路由时，使用「全部档位」。

如果一个环节既没有路由，也没有启用的模型：

- `LLM_MODE=mock`（默认）：使用内置模拟写手，不访问网络，本地和测试能走完「立即生成」。
- `LLM_MODE=live`：任务失败并退回额度，提示管理员先配置模型。

真实调用使用流式响应（`stream: true`），带超时和指数退避。429 / 5xx / 网络错误会重试；模型把内容返回成无法解析的 JSON 时不重试，直接换下一个供应商。`access_token` 和后台配置都有进程内缓存。

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

浏览器打开 `http://127.0.0.1:8000`。数据库数据在卷 `pgdata`。镜像是多阶段构建：Node 编译前端，Go 编译静态二进制，最终运行在 distroless 里。健康检查调用 `gonghao healthcheck`。

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

没有配图服务时，封面是程序生成的纯色 PNG，经永久素材接口上传。

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
| `STALE_TASK_SECONDS` | 卡在「生成中」超过这个秒数就重新排队，默认 900 |
| `WX_APP_ID` / `WX_APP_SECRET` | 只给 `gonghao probe` 和后台真实连通测试用 |
| `LLM_PROTOCOL` / `LLM_BASE_URL` / `LLM_API_KEY` / `LLM_MODEL` | 连通测试使用的模型。协议是 `openai` 或 `anthropic` |
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
