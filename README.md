# 公号智写

按《项目开发书：公众号自动写稿智能体》做的 **P0 内测版**。后端是单个 Go 二进制，前端是 Vue，构建后嵌进同一个进程。用户设定写作方向和周期后，系统定时生成文章，并写入公众号草稿箱；写不进去时改为导出可复制的排版 HTML。

模型密钥和公众号密钥都由管理员配置，用户不接触 API Key。

## 已实现（P0）

- 手机号或邮箱 + 密码注册登录。注册必须主动勾选同意《用户协议》《隐私政策》，同意写入 `consent_log`（条款类型、条款版本、时间、IP、浏览器标识）。条款版本（`catalog.LegalVersion`）变更后，已登录用户进入工作台前要重新勾选同意，新的同意再写一条记录。短信验证码和微信扫码登录配齐环境变量后才出现；没配时页面上没有这两个入口。公众号仍可用 AppID / AppSecret 绑定。
- 协议页：用户协议、隐私政策、免责声明、会员与退款、内容规范、邀请规则。文首都标明 **草案 · 待法务审核**。
- 公众号绑定：选择个人/企业、订阅号/服务号、是否认证；加密保存 AppSecret；检测 `stable_token`、草稿数量、封面素材、草稿写入与读取，并删除检测草稿。检测失败时改为手动导出。同一 AppID 不能同时绑在两个账号上，解绑后 30 天内不能被其他账号绑定。
- 全托管写作计划：只选方向、风格、排版、每天或每周、时段窗口，以及这个方向每天几篇。每天篇数按用户合计（免费和普通默认 1，高级和买断默认 5，配置中心可以改），各方向加起来不能超过这个上限。立即生成和定时生成都扣同一笔额度。同一个方向不能并发生成。免费版固定夜间低峰（0:00–6:00，北京时间），并且只能选免费档的风格和模板。
- 每个公众号各自拥有写作计划。方向、关键词、读者、风格、排版、篇幅、周期、禁用词、固定开头和结尾、垂类资料包、选题方式和配图都按计划分开保存，改一个号不会改另一个号。工作台和计划页对已暂停的计划显示「启动」，计划详情可以编辑。立即生成一篇在暂停时仍可写一次，不会自动恢复定时。额度仍按用户计算。
- 工作台按已绑定的公众号各放一张卡片，上面有状态、下次生成、今天全账号还剩几篇、最近一篇和启动、暂停、立即生成。还没开始的号走「给一个号做设置」：绑定、按号的类型和垂类给出方向、看一段 200 字试读，再启动。试读不占用每天的风格试写次数。写作计划页先显示方向和节奏，其余收在「更多设置」里。
- 定时扫描：默认每分钟查找到期计划。PostgreSQL 使用 `FOR UPDATE SKIP LOCKED` 领取任务。全局并发是可配置的 goroutine 池（`MAX_CONCURRENCY`），生成过程大部分时间在等模型和微信，不占着数据库连接。
- 分阶段写作：选题 → 大纲 → 正文 → 审核 → 结构化 JSON → 内联样式 HTML。主题按「子方向 + 栏目 + 角度」去重，90 天内尽量不重复。
- 本地敏感词审核。不通过会再请模型改写一次，仍然命中则任务失败并退回额度。正文之后还有一次事实核对：对照检索来源和领域常识改掉错误的定义和数字，来源没有支撑的阈值、电压和标准编号不写进正文。
- 草稿推送：`/cgi-bin/stable_token`、永久封面素材 `/cgi-bin/material/add_material?type=image`、正文图 `/cgi-bin/media/uploadimg`、`/cgi-bin/draft/add`。重写会重新生成并上传封面。旧草稿还在时走 `/cgi-bin/draft/update`；如果微信返回 40007 或其它 media_id / thumb_media_id 无效，就重新上传封面并用 `/cgi-bin/draft/add` 新建一篇，记下新的 `media_id`。新增草稿时封面无效也会重新上传一次再提交。没有配图服务时用程序生成的默认封面（草稿接口要求 `thumb_media_id`）。推送失败则保留 HTML，状态为「已导出」。
- 额度：任务创建时预占，成功确认，失败退回。按北京时间的自然日统计进行中和已成功的篇数，一个用户名下所有公众号合在一起。免费版每天 1 篇、每月 10 篇；普通每天 1 篇、每月 30 篇；高级每天最多 5 篇、每月 150 篇；买断每天最多 5 篇、每周年 365 篇。配置中心「会员」页可以改每天和周期上限。每篇第一次重写不占额度。到期后有 7 天宽限期，宽限期内不能新增公众号和方向；宽限期结束按免费版保留较早的计划。
- 文章列表：状态、预览、失败原因、重新生成、复制 / 下载 HTML。浅色稿是实际推送的 HTML；暗色只供站内预览。
- 管理配置中心：多个模型供应商、环节回退顺序、微信模拟/正式开关、出口 IP、补充敏感词、手工开通会员。没有在线支付。
- 真实连通测试：命令行 `gonghao probe`，以及管理后台微信页的「真实连通测试」。用真实 AppID / AppSecret 和真实模型走一遍写作，并把一篇草稿留在公众号草稿箱。

## 第一批 P1（不依赖商户号）

- 失败通知：任务最终失败，或高风险稿没有写入草稿时，站内「通知」记一条。`NOTIFY_WEBHOOK_URL` 若配置了，会再 POST 一份 JSON。`SMTP_HOST` 配了并且账号是邮箱时，同一条通知再发一封邮件。两个都走 `internal/notify.Sender`，失败不会让写作任务失败。没配就只记站内通知。
- 反馈：文章页可以记「已发布 / 修改后发布 / 弃用」。
- 草稿回读：`POST /api/articles/{id}/reread` 调微信 `draft/get`，比较标题和正文长度。结果是未改、已改或读不到。读不到不等于已发布。
- 运营后台：配置中心增加最近任务和投诉回复。
- 内容日历：按现有轮换规则预告接下来几次选题，不额外调用模型。
- 风险分级：剂量、诊断、偏方、处方、治疗方案、包治为高风险，不写入草稿。母婴方向里出现喂养、睡眠、疫苗、健康，或正文出现投资建议、收益率为中风险。免费版只推送低风险；付费档的中风险需要带来源。
- 手动干预：写作计划可以指定下一篇题目（成功后清除），也可以否决「子方向|栏目|角度」。
- 帮助中心、投诉入口、定价页。没配支付时购买按钮禁用并写「即将开通」。未登录投诉按 IP 每小时最多 5 次。
- 账号注销会立刻删除公众号密钥和账号数据。`GET /api/me/export` 导出 JSON，不含 AppSecret。

## 垂类、风格、预览和套餐变更

- 垂类规则包存在 `vertical_pack`。方向名称里包含关键词时套用对应红线、子话题、来源偏好、配图要求和文末声明；关键词留空的「通用」是兜底。内置弱电智能化、家庭生活、科技。配置中心「内容库」可以改，也可以新增。写作和内容日历都用这套子话题。
- 风格 7 种、排版 5 套。免费档仍是原来的 3 种风格和 3 套模板；故事化、干货清单、观点犀利、科普通俗，以及卡片风、杂志风，最低是普通版。管理员可以改名称、说明、语气、最低档位和是否启用。停用或档位不够时，不能新建计划。
- 文章页可以换排版预览。预览用同一份正文 JSON 重新排版，和写入草稿用的是同一套浅色 HTML。换了模板但还没重写时，页面会说明这份预览没有写回草稿。暗色仍然只在站内看。
- 表格由程序画，模型只给表头和单元格，最多 4 列。更宽的表改成「表头：内容」的段落。没有行的表不画。正文配图走封面同一套生图接口（`grok-imagine-image-2.0` 一类），用微信 `uploadimg` 的地址插进小节。免费版不用 AI 封面，也不插 AI 配图，仍用程序封面和原来的一张内置正文图。普通版 AI 封面加 1 张配图，高级版和买断版 AI 封面加最多 3 张。生图或上传失败就跳过，文章仍然继续。
- 套餐升降级不依赖支付。升级立刻生效。降级时如果当前到期日还在后面，默认记到 `tier_change`，到期那一刻再改档位并按免费版收紧计划；管理员勾选「降级等到期再生效」，或请求里 `when` 为 `period_end`，才走这条。`when` 为 `now` 则马上改。买断版没有到期日，降级立刻生效。到期后的 7 天宽限期不变。
- 邮件：`SMTP_HOST`、`SMTP_PORT`（默认 587）、`SMTP_USER`、`SMTP_PASSWORD`、`SMTP_FROM`。不要把真实密码写进仓库。

## 认证号自动发布

默认关闭。只有企业主体且已认证、档位是普通版 / 高级版 / 买断版、并且写作计划里勾过内容责任的号，才会把**低风险**稿提交发布。个人号、未认证号、免费版、中风险和高风险都留在草稿箱，页面上写明原因。

审阅窗口默认 30 分钟，可以改成 0 到 180。0 表示生成后马上提交。到点先读草稿：草稿不在就取消，这篇仍留在草稿箱。草稿还在就按现状调用 `freepublish/submit`，再用 `freepublish/get` 查结果。没有 `publish_id` 不会再提交第二次。发布中会继续查，大约两小时仍未知就停下来并通知，不再重试。

每个公众号每天最多 `PUBLISH_DAILY_CAP` 次（默认 20，最大 100）。超过后这篇仍留在草稿箱。写作计划里可以随时关掉。

## 在线支付

没配齐一组商户参数时，`POST /api/billing/checkout` 仍是 501，页面按钮仍是「即将开通」。配齐微信支付或支付宝之后，定价页可以下单。支付成功的回调验签通过、金额一致，才会把档位改掉；同一订单再通知一次不会把到期日再延一次。同档续费从当前到期日和现在里较晚的那个往后加。买更高档位马上生效。当前档位更高时不自动降级。

退款没有接退款接口。管理员在「订单」里标记已退款，并到会员页自己改档位。微信商户平台或支付宝后台的退款要手工做。

`PAY_SANDBOX=1` 只用于测试：下单返回 `sandbox:` 开头的链接，再调完成接口就入账，不会访问微信或支付宝。生产环境不要开。

要打开微信支付，下面每一项都要有。私钥和平台证书可以写 PEM 文本（换行写成 `\n`），也可以改用文件路径。文件读不到就当作没配，进程不会因此起不来。

- `WXPAY_MCH_ID`
- `WXPAY_APP_ID`
- `WXPAY_MCH_SERIAL`
- `WXPAY_API_V3_KEY`（正好 32 字节）
- `WXPAY_PRIVATE_KEY` 或 `WXPAY_PRIVATE_KEY_FILE`
- `WXPAY_PLATFORM_CERT` 或 `WXPAY_PLATFORM_CERT_FILE`
- `WXPAY_NOTIFY_URL`（公网 HTTPS，指向 `/api/billing/wechat/notify`）。留空时用 `PUBLIC_BASE_URL` 拼出这个地址

可选：`WXPAY_API_BASE`，默认 `https://api.mch.weixin.qq.com`。

要打开支付宝当面付（扫码），下面每一项都要有：

- `ALIPAY_APP_ID`
- `ALIPAY_PRIVATE_KEY` 或 `ALIPAY_PRIVATE_KEY_FILE`
- `ALIPAY_PUBLIC_KEY` 或 `ALIPAY_PUBLIC_KEY_FILE`
- `ALIPAY_NOTIFY_URL`（公网地址，指向 `/api/billing/alipay/notify`）。留空时用 `PUBLIC_BASE_URL` 拼出这个地址

电脑网站支付的回跳地址是 `ALIPAY_RETURN_URL`。留空时回到定价页 `{PUBLIC_BASE_URL}/pricing`。可选：`ALIPAY_GATEWAY`，默认 `https://openapi.alipay.com/gateway.do`。沙箱网关是 `https://openapi-sandbox.dl.alipaydev.com/gateway.do`。

`PUBLIC_BASE_URL` 是站点对外地址，生产环境是 `https://mp.nbn168.uk`。配置中心「服务」页可以改它，保存后盖过环境变量。回调地址留空时分别是 `{PUBLIC_BASE_URL}/api/billing/wechat/notify`、`{PUBLIC_BASE_URL}/api/billing/alipay/notify`、`{PUBLIC_BASE_URL}/pricing`、`{PUBLIC_BASE_URL}/api/auth/wechat/callback`、`{PUBLIC_BASE_URL}/api/auth/wechat/component/callback`。不会用浏览器请求里的 Host 来猜。

价格默认是普通版月 30 元 / 年 299 元，高级版月 99 元 / 年 990 元，买断 3000 元，加量包 19 元 10 篇，单位在环境变量里是分。可选覆盖：`PAY_PRICE_STANDARD_MONTH`、`PAY_PRICE_STANDARD_YEAR`、`PAY_PRICE_PREMIUM_MONTH`、`PAY_PRICE_PREMIUM_YEAR`、`PAY_PRICE_LIFETIME`、`PAY_PRICE_PACK`、`PAY_PACK_ARTICLES`。

## 加量包、补差、到期提醒、找回和备份

- 加量包在定价页购买。订阅当月（或买断的当周年）额度用完后，下一篇才扣加量包，每天篇数上限仍然有效。入账后 90 天到期。
- 同周期升级按剩余天数补差，到期时间不变。换成更长周期时，先扣掉当前周期还没用完的金额，再从付款时重算到期。差价为 0 时直接改档位，不再向微信或支付宝下单。
- 调度每轮检查到期日。还剩 7 天、3 天、1 天时各写一条站内通知，同一天不重复。
- 登录页可以找回密码。邮箱走 `SMTP_*`，手机走和短信登录同一组 `SMS_*`。没配对应通道时，这个方式会提示先配置。未注册的账号也返回成功，避免探测账号是否存在。验证码 10 分钟内有效。发送和验证的次数限制与短信登录相同：同一手机号 60 秒一次、24 小时最多 5 次；验证先占用一次机会，满 5 次作废。同一 IP 每小时最多发 20 条；验证时同一 IP 每小时 60 次，同一手机号 10 分钟最多 30 次。
- 会员页可以换绑手机（同样要短信），也可以撤回某一条同意。撤回用户协议或隐私政策后要重新同意。撤回自动发布会关掉这个用户的自动发布开关。
- 首次付费订单在 7 天内、且没有确认消耗付费额度时，会员页可以申请全额退款，档位回到免费。退款到账仍要在商户平台手工操作。管理员在订单上标记退款不会改档位。
- 配置中心的会员行可以保存定制合同的公司、编号和备注。
- 备份：`gonghao backup backup.json`，恢复：`gonghao restore backup.json`。文件含密码散列和加密后的密钥，权限是 `0600`。恢复会按备份里的表清空再写入，请先停写。

## 认证号数据、图表和模板消息

- 认证号在同意「读取统计」之后，可以拉取前一天的发表内容阅读、分享和新增关注。接口是 `/datacube/getarticleread`、`/datacube/getarticleshare`、`/datacube/getusersummary`。北京时间 8 点后定时任务会错峰拉一次，已经有记录的日期不重复拉。未认证号不调用这些接口，可以手工填写阅读数。
- 周报只复述已经保存的数字。样本少于 3 天时不调整选题。高级版和买断版在某个子方向的发布或弃用反馈达到 3 条后，才会提高或降低该子方向的权重，写作计划里可以撤销。
- 稿件章节里如果自带图表数字，会画成柱状图上传到微信，正文同时写出这些数字。阅读页用同样的数字画柱。
- 推送新草稿前会查询草稿箱开关和总数。开关关闭，或总数达到 `draft_box_cap`（默认 1000，官方总数接口没有公布硬上限）时先不写，稍后重试。开关接口若已废弃，按已开启处理。
- 稿件进入草稿箱的通知会写明：本产品不调用群发，订阅号每天 1 次、服务号每月 4 次。
- 服务号模板消息要同时配置 `WECHAT_TPL_APP_ID`、`WECHAT_TPL_APP_SECRET`、`WECHAT_TPL_ID`。可选 `WECHAT_TPL_API_BASE`、`WECHAT_TPL_FIELDS`（默认 `thing1,thing2`，内容截成 20 字）。缺任意一项就不发。用户保存自己的 OpenID 后，站内通知会再发一条模板消息。
- 配置中心「实测」页记录模板在浅色和暗色下的检查结果。两类都有通过记录才显示已实测。
- 示例文章在 `/examples`，关于我们在 `/about`。多架构镜像用 `scripts/release-images.sh`，需要本机 Docker buildx。

## 风格、试写、素材和来源检查

- 写作方向可以填固定开头、固定结尾。栏目名和栏目风格都填了，并且这一篇的栏目对得上时，才改用栏目风格。
- `POST /api/styles/trial` 生成约 200 字试写。每个账号每天默认 3 次，设置项 `trial_daily_cap` 可以改。
- `POST /api/sample` 每个账号只出一次站内样稿，不写公众号。
- 高级版和买断版可以用 `POST /api/style-profiles` 提交 3 到 5 篇范文。库里只留句长、问句数和第一人称次数，不留原文。
- 正文如果有连续 40 个字和某条来源相同，这篇任务失败。
- 最近 5 篇成功稿都没有任何反馈时，计划暂停并通知。已经打开自动发布的计划不走这条规则。反馈可以选「已处理」。
- 同意读取草稿标题后，生成前会取出最多 20 条本账号草稿标题，放进去重列表。
- 用户素材走 `POST /api/assets`，只收 PNG 或 JPEG，必须写授权说明，不超过 200KB。写作计划的 `asset_id` 会优先当作封面。
- 模型协议可以选 `http`。额外参数里的 `response_text`、`response_prompt_tokens`、`response_completion_tokens` 用点号指出响应字段，这些键不会发给对方。
- 非流式模型调用在进程内缓存 24 小时。多段文本可以走一次批量调用。
- 配置中心可以保存提示词分组。已发布类反馈会提高该分组的采用率。

## 没有实现（相对开发书）

这些留在后面，代码里没有假装已经可用：

- 宣传页里的绝对化承诺。落地页和定价页只说明已实现的能力

对照开发书的逐条状态见 `docs/spec-coverage.md`。

## 运营看板、成本熔断、协议版本和 AI 标识

配置中心有「看板」「审计」「协议」三页。

- 看板统计注册、付费转化、近 30 天续费、成功稿件和估算成本。单价可以改：文本按每千 token、检索按次、AI 配图按张，单位是分。
- 用户每月成本上限为 0 时不熔断。达到上限后，「降级」会停掉这篇的联网检索和 AI 配图，文章继续写；「暂停」在这篇降级写出之后把计划暂停，并写一条通知。下个月成本从新月份重新累计，计划需要手动恢复。
- 「本文由人工智能辅助生成」默认关闭。打开后，新稿文末会加上这句话。开关变更写入审计日志。
- 协议页可以发布新版本。用户协议或隐私政策若勾选「要求重新同意」，已登录用户要重新勾选后才能继续用。另一份核心协议会带到同一版本号。旧版本仍可通过 `?version=` 打开。首页、帮助、关于也可以各存一版补充文本，公开地址是 `/legal/page_home`、`/legal/page_help`、`/legal/page_about`。
- 定时扫描会清空超过 30 天的正文、HTML 和来源列表，标题和任务记录保留。
- 审计页列出后台操作。同意记录按用户 ID 查询。

## 短信登录和微信登录

密码登录一直可用。下面几项没配齐时，接口返回未开通，登录页和绑定页不显示对应按钮。环境变量仍然有效。配置中心「服务」页保存的值会盖过同名环境变量，空白的密钥不会把已经保存的密钥清掉，页面上只显示末四位。保存后不用重启，登录方式和支付方式会跟着打开或关闭。

「服务」页可以填写短信、邮件、微信支付、支付宝、微信扫码登录、微信第三方平台、模板消息和失败转发，并按项做连通测试。测试每小时最多 5 次。每次修改写入审计日志，只记改了哪些字段，不记字段内容。

注册验证码默认关闭。短信和邮件都还没配好时，开关不能打开，页面会说明原因。打开之后，用手机号注册要先通过短信验证码，用邮箱注册要先通过邮件验证码。验证码 5 分钟有效。同一账号 60 秒一次、24 小时最多 5 次。提交时先占用一次机会，满 5 次作废。同一 IP 每小时最多发 20 条、校验 60 次；同一手机号或邮箱 10 分钟内最多校验 30 次。没配好的那种账号不能在开关打开时注册。

短信（`SMS_PROVIDER` 为 `aliyun` 或 `tencent`）：

- `SMS_ACCESS_KEY`、`SMS_ACCESS_SECRET`、`SMS_SIGN_NAME`、`SMS_TEMPLATE_CODE`
- 腾讯云还要 `SMS_SDK_APP_ID`。可选 `SMS_REGION`（默认 `ap-guangzhou`）
- 可选 `SMS_API_BASE`，用来改短信网关地址。阿里云默认 `https://dysmsapi.aliyuncs.com`，腾讯云默认 `https://sms.tencentcloudapi.com`
- 模板里放一个验证码变量。同一手机号 60 秒一次、24 小时最多 5 次。验证码 5 分钟有效。每次提交先占用一次机会，满 5 次作废，并行猜测也不能超过 5 次。同一 IP 每小时最多发 20 条、登录尝试 60 次；同一手机号 10 分钟内最多登录尝试 30 次。新手机号必须勾选协议后才会注册
- 微信扫码和开放平台授权会给发起浏览器种下 `HttpOnly`、`SameSite=Lax` 的校验 cookie。回调必须带上这枚 cookie，state 和登录票据只能用一次。开放平台授权还会记下当时登录的用户，不能把授权绑到别的账号

微信网站扫码登录：

- `WECHAT_OPEN_APP_ID`、`WECHAT_OPEN_APP_SECRET`、`WECHAT_OPEN_REDIRECT_URL`（留空时用 `PUBLIC_BASE_URL` 拼 `/api/auth/wechat/callback`）
- 回调地址填开放平台里登记的域名，指向 `/api/auth/wechat/callback`
- 接口基址沿用 `WECHAT_API_BASE`，默认 `https://api.weixin.qq.com`

微信开放平台第三方平台授权（用来代替用户填写 AppSecret）：

- `WECHAT_COMPONENT_APP_ID`、`WECHAT_COMPONENT_APP_SECRET`、`WECHAT_COMPONENT_TOKEN`、`WECHAT_COMPONENT_AES_KEY`（43 位）、`WECHAT_COMPONENT_REDIRECT_URL`（留空时用 `PUBLIC_BASE_URL` 拼 `/api/auth/wechat/component/callback`）
- 授权事件接收地址指向 `/api/wechat/component/notify`。收到 `component_verify_ticket` 之后，绑定页才出现「使用微信开放平台授权」
- 回调指向 `/api/auth/wechat/component/callback`。刷新令牌加密存在侧表里，公众号表的 AppSecret 留空
- 微信如果轮换刷新令牌，需要用户重新授权。AppID / AppSecret 绑定仍然可用，正式模式下的重新检测和草稿回读会走已保存的授权

## 选题确认、热点和邀请

- 高级版和买断版可以在写作计划里打开选题确认。扫描发现 24 小时内要写的计划时，先把选题放进通知，不立刻生成。采用后仍等到原定时间再写。换一个会否决当前选题并换下一条，确认截止时间不重置。24 小时没处理，就按当时那条写。免费版和普通版不能打开这个开关。
- 热点源在配置中心单独添加，协议是 HTTP JSON 或远程 MCP。密钥加密保存。缓存按来源里的小时数刷新，默认 12 小时，失败时沿用上一份缓存。没添加启用的来源时，仍按常青选题轮换。普通版用缓存里的第一条，高级版和买断版优先官方域名和多源条目。免费版不用热点。
- 每个账号有邀请码，注册页可以带 `invite`。无效邀请码不影响注册。免费用户每个有效邀请当月加 2 篇，最多加 20 篇，月额度不超过 30 篇。付费期间不加，降回免费后恢复。参数在配置中心「邀请」页可改。
- 有效邀请要同时满足：手机号不是 170、171、165、167、162 开头，设备标识不与已有账号相同（用户勾选同意才记录），当天同一 IP 段的邀请注册不超过 3 个，绑定的公众号以前没被绑定过，写出 3 篇，并过 7 天观察期。解绑或停用会撤销。当天第 4 个本来可以生效的邀请会冻结，等管理员通过或撤销。已经写出的文章不追回。

同日多篇会分别进草稿箱。微信订阅号每天只能群发 1 次，多篇需要用户在公众号助手里自己合并或分次发布。

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

资料检索用单独的提示词：要求先用中文搜索、优先中文网页，并优先 gov.cn、samr.gov.cn、std.samr.gov.cn、openstd.samr.gov.cn、mohurd.gov.cn、国家标准、中国行业协会和主流媒体，不要聚合站、内容农场、文库镜像、网店、帮助台、登录页或购物车。同一网站最多两条。弱电智能化会再点名国家标准（例如 GB 50311）和中国工程建设标准化协会。这一步不强制 `json_schema`。来源只采用接口里的结构化引用：`citations`、`search_results`、`annotations`。Responses 还要 `num_sources_used` 大于 0，才采信里面的 url 注释。模型正文、Markdown 和它自己写的 `sources` 里的网址一律不算，避免把编造的链接写进「参考来源」。`grok-chat-fast` 使用流式请求，这样 Cloudflare 大约 120 秒的空闲切断不会作用在这次等待上；引用如果在流的最后一块里，也会被读到。单次超时默认 150 秒（`RESEARCH_TIMEOUT_SECONDS`，限制在 30 到 300 秒）。不能流式的检索模型把等待压到 110 秒。同一次检索里，40 秒内返回的 502、503 或连接错误会短暂停后再试，最多两次；更慢的失败不在这次里重试。没有可用引用或请求失败时，再用更短的中文检索词补救一次，默认仍是 `grok-chat-fast`（`RESEARCH_BACKUP_MODEL` 可改），不再改走 `grok-4.7` 的 Responses，也不附带 `web_search`。补救之后仍然没有引用，稿件照常进草稿箱，文章和通知会标「无参考来源」，不因此失败。同一环节里，模型名包含 `chat-fast` 的会排到最前面再按原回退顺序调用。严格过滤后如果一条可用网址都没有，先对同一批引用放宽过滤（去掉允许名单，屏蔽名单仍然生效），还是没有才做上面那一次补救，补救同样先严格、再放宽。丢掉的网址和原因写入 `model_trace` 的 `dropped`（屏蔽域名、网店或登录页、不在允许名单、没有网址、无法解析、没有解析到来源）。来源交给大纲和正文，并写入文末「参考来源」；网址里的「.」写成「点」。引用会全部收下，再按屏蔽名单、网店页和同一主机两条过滤，官方站、标准站和主流媒体排在前面，中文选题再优先中文来源，最后保留前 8 条。默认丢掉 `xilu.com`、`fyun.com`、`huanancj.com`、`bzko.com`、`yuanlin8.com`、`bbs.co188.com`、`ireader.com`、`abook.qq.com`、`gcs66.com`、`bzfyw.com`、`baijiantest.com`。配置中心「敏感词」页可以改屏蔽名单和允许名单；允许名单不为空时只保留这些域名。还没保存过时用默认屏蔽名单，保存成空名单则不再屏蔽。

正文字数统计各小节的小标题、段落、列表、表格单元格和结尾，不含文章标题、摘要、参考来源和脚注。可接受区间仍是 `word_target` 的 85% 到 130%。偏短时用同一写作模型扩写一次，目标大约是 105%，写过 110% 就裁回这个目标，不再把扩写放到 130%。偏长时缩写一次。模型缩写后仍然超过 130%，就从文末裁到上限。事实核对和审核可能把正文重新写长，所以长度裁剪是最后一次改正文：审核之后如果又超过 130%，同样从文末裁到上限，不再次调用模型。

正文、扩写、缩写和审核都要求：来源里没有的阈值、电压、标准编号和统计数字不要编造。正文定稿前还有一步「事实核对」，用审核环节的模型对照 `research_notes`、`research_sources` 和领域常识改写定义与数字，并且不得新编网址或标准编号。例如建筑智能化里的「弱电」是通信、安防、楼宇自控、火灾报警、音视频等电子系统（常见 36V 及以下的安全特低电压或信号电平），不是低压配电的 1000V。核对失败不会让整篇文章失败，会留下追踪并继续用当前正文做敏感词审核。

环节路由在「配置中心 → 环节路由」。选题、资料检索、大纲、正文、审核可以各自指定一串模型，**顺序就是回退顺序**：前一个超时、HTTP 错误或返回的不是合法 JSON，就换下一个。路由可以按会员档位区分；某一档没有专属路由时，使用「全部档位」。资料检索是例外：没有单独配置就不会联网，也不会借用其它环节的模型。免费档不检索。

如果一个环节既没有路由，也没有启用的模型：

- `LLM_MODE=mock`（默认）：使用内置模拟写手，不访问网络，本地和测试能走完「立即生成」。
- `LLM_MODE=live`：任务失败并退回额度，提示管理员先配置模型。

选题、大纲、正文、审核使用流式响应（`stream: true`），带超时和指数退避。资料检索对 `grok-chat-fast` 也使用流式，并且只有上面说的那一次补救。不能流式的检索调用把超时压到 110 秒。选题、大纲、正文和审核遇到 429 / 5xx / 网络错误会重试；模型把内容返回成无法解析的 JSON 时不重试，直接换下一个供应商。Chat Completions 不会擅自加上很小的 `max_tokens`。Anthropic 在管理员没写 `max_tokens` 时用 16384，避免推理把额度花完后没有正文。`access_token` 和后台配置都有进程内缓存。

`https://ai.1so.org` 的文档说明：Cloudflare 大约 120 秒没有字节就会返回 524，流式响应每收到一块数据就刷新这个窗口，并建议能选的话优先用 Responses。grok-4.7 在 `medium` 推理时，短提示也要大约 87 秒才吐出第一个字，真实提示会越过这个窗口，表现为 HTTP 524 或 HTTP/2 `stream error INTERNAL_ERROR`。长环节因此默认 `low`。如果调用仍然被这种空闲切断打断，会立刻再试一次（不再按指数退避空等）：推理档位降到 `low`；原来走 Chat Completions 的推理模型（名字里有 `grok-4`、`grok-composer` 或 `grok-build`，且不是生图或 `chat-fast`）改走 Responses。Responses 流里的 `response.reasoning_summary_text.delta` 和 `response.reasoning_text.delta` 会被读走以保持连接，但不会写进正文。该站文档没有列出这两个事件名；只要上游按 OpenAI Responses 发送，就会被接受。已经降过档、或无法再降时，马上换下一个供应商。每一次失败尝试都写入 `model_trace`（供应商、模型、实际协议、推理档位、错误）。任务最终失败时这些记录也会留下。

「立即生成」和「重新生成」只负责入队，接口马上返回「生成中」。文章页和文章列表会轮询，完成后刷新。任务的 `duration_ms` 从开始写到结束，而不是只记入队那一瞬间。

## 封面和资料检索

封面：在模型里把用途选成「封面生图」，协议用 `openai`，Base URL 写到 `/v1`，模型名填生图模型。请求会带 `aspect_ratio: 16:9` 和 `response_format: b64_json`（上游图片 URL 经 Cloudflare 下载时常 502）。返回 base64 时直接使用；如果上游仍给了 URL，再尝试下载。程序按文件头区分 JPEG 和 PNG，上传文件名为 `cover.jpg` 或 `cover.png`。大约超过 2MB 会重新编码，超过微信 10MB 上限则放弃这张图。任务追踪会记下封面是模型生成还是内置图、字节数、文件名，以及失败原因。失败时改用内置纯色封面，文章仍然可以进草稿箱。

资料检索：给「资料检索」单独配一个会联网的文本模型，例如 `grok-chat-fast`。如果路由里同时有推理模型和 `chat-fast`，会先调用 `chat-fast`。付费档会在选题之后先检索。`grok-chat-fast` 使用流式，单次默认最多等 150 秒；不能流式的模型最多等 110 秒，以免被 Cloudflare 的空闲切断打断。失败或没有结构化引用时，用更短的中文检索词再试一次，默认还是 `grok-chat-fast`。正文里的链接不算来源，只收 `citations`、`search_results`、`annotations`；Responses 要 `num_sources_used` 大于 0。引用先全部收集，过滤和排序后再留前 8 条。40 秒内的 502、503 和连接错误会在同一次检索里再试两次。仍然没有来源就继续写稿，并在文章和通知里标「无参考来源」。同一网站最多两条，网店和登录页会丢掉。允许名单把结果全部滤掉时，会放宽一次（仍丢掉屏蔽名单）并把原因记进追踪。没配这个环节就跳过，不因此失败。检索追踪会写下模型名和供应商名称，失败的那一次也会留下。屏蔽名单和允许名单在「敏感词」页。

重写：每次都重新做封面并上传。旧的 `draft_media_id` 还有效时更新那一篇；微信返回 40007（素材 media_id 无效）或其它 media_id / thumb_media_id 无效时，重新上传封面并新建草稿，文章改记新的 `media_id`。这样草稿被删掉或已经发布后，重写仍然能进草稿箱，而旧稿还在时也不会多留一份过期副本。

配置中心的用户列表可以停用或删除测试账号。停用后无法登录。删除会清掉该用户的公众号、计划、文章和额度记录。不能停用或删除当前登录的管理员，也不能动最后一位管理员。

以 `https://ai.1so.org` 为例（密钥只放后台或环境变量）：

| 用途 | 协议 | Base URL | 模型 |
| --- | --- | --- | --- |
| 选题 / 大纲 / 正文 / 审核 | `openai` 或 `responses` | `https://ai.1so.org/v1` | `grok-4.7`。大纲、正文、审核在管理员没指定更高档位时发送 `reasoning_effort=low`。被 524 或流中断切断后，推理模型会改用 Responses 再试一次 |
| 资料检索 | `openai` | `https://ai.1so.org/v1` | `grok-chat-fast`，流式。路由里如果还有别的模型，名字里带 `chat-fast` 的会先调用。第一次没有结构化引用时，用更短的中文检索词再调一次 `grok-chat-fast` |
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
| `RESEARCH_TIMEOUT_SECONDS` | 资料检索单次等待，默认 150，限制在 30 到 300 秒。`grok-chat-fast` 会流式使用这个值；不能流式的模型最多 110 秒 |
| `RESEARCH_BACKUP_MODEL` | 补救检索用的模型名，默认 `grok-chat-fast`。用同一协议再请求一次，不改走 Responses，也不加 `web_search` |
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

并发额度测试连真实 Postgres：10 个立即生成同时打到每日 5 篇的用户上，成功的任务必须正好 5 个。没设置 `POSTGRES_TEST_URL` 时这条会跳过。

```bash
docker run --rm -d --name gonghao-pg-test \
  -e POSTGRES_USER=gonghao -e POSTGRES_PASSWORD=gonghao -e POSTGRES_DB=gonghao \
  -p 5432:5432 postgres:16-alpine
POSTGRES_TEST_URL=postgres://gonghao:gonghao@127.0.0.1:5432/gonghao?sslmode=disable go test ./...
```

## 还没做

1. 用真实连通测试，在个人订阅号草稿箱里核对一篇稿。认证号自动发布要等有发布权限的号再试。
2. 法务替换协议文本；确定模型供应商是否出境，并补单独同意。支付密钥、短信密钥、微信开放平台密钥和模板消息密钥由部署环境填写，不要写进仓库。
