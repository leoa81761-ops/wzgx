# 开发书覆盖情况

对照《项目开发书：公众号自动写稿智能体（V1.13）》。状态只用下面几类：

| 状态 | 含义 |
| --- | --- |
| done | 当前 main 上已经可用 |
| added | 相对已合并的 main，本系列分支补上 |
| needs-external-credentials | 代码在，没配环境变量就关闭 |
| 未做 | 还没有，排在后面的分支 |
| 范围外 | 开发书写明不做 |

法务正文仍是草案。代码能版本化、能要求重新同意，不能代替律师审过的正式文本。

## 1–2 概述与用户

| 要求 | 状态 |
| --- | --- |
| 设定方向后定期生成并写入草稿箱 | done |
| 免费 / 普通 / 高级 / 买断 | done |
| 范围内：用户、绑定、方向、定时、草稿、认证号发布、表格图片、会员支付、后台 | done，支付见 needs-external-credentials |
| 认证号数据分析（P2，7.11） | 未做 |
| 自动群发、小程序、视频号 / 小红书、自动抓取他人图片 | 范围外 |
| 个人号只推草稿，企业认证号可自动发布 | done |

## 3.1 用户端

| 要求 | 状态 |
| --- | --- |
| 手机号 / 微信扫码登录 | 手机号或邮箱 + 密码 done；短信验证码与微信扫码 needs-external-credentials，没配环境变量就关闭 |
| 公众号绑定、主体与认证、权限检测 | AppID / AppSecret done；开放平台授权 needs-external-credentials，没配时仍用 AppSecret |
| 全托管：只选方向 | done |
| 精细设置：关键词、读者、风格、模板、篇幅、禁用词、固定开头结尾 | done。禁用词走全局敏感词。写作方向可以填写固定开头和结尾 |
| 推送周期：每日 / 每周、时段、每号每日一批、按账号类型推荐 | 每日 / 每周和时段 done；同日多篇分别进草稿，合并多图文由用户在公众号助手完成 |
| 文章管理：记录、状态、预览、重写、失败原因 | done |
| 入草稿后站内信 / 服务通知 | 站内通知 done；邮件 needs-external-credentials（`SMTP_*`）；服务号模板消息 未做 |
| 会员中心：等级、额度、开通续费、订单 | done；在线支付 needs-external-credentials |
| 立即生成、指定题目、暂停 / 恢复、否决选题 | done |
| 邀请链接与奖励额度 | added。会员页展示链接、计数和规则；无效邀请码不影响注册 |
| 同意记录、撤回、注销、导出 | 同意记录、注销、导出 done；单独同意可再记一条，撤回入口 未做 |

## 3.2 智能体

| 要求 | 状态 |
| --- | --- |
| 选题、子方向、月历、主题去重 | done（日历按规则预告，不另调模型） |
| 联网检索 | done（付费档，且要配置检索路由） |
| 按风格和篇幅生成结构化正文 | done |
| 敏感词、风险、来源校验，不通过重写 | done |
| 内联 HTML 与文末参考来源 | done |
| 封面与配图 | done（免费版程序封面） |
| 草稿推送与失败重试 | done |
| 评分、草稿回读、修改检测 | done |
| 按采用率做提示词 A/B | done。管理员可保存「正文」分组。采用率更高的分组优先，约五分之一的稿做对照。已发布或修改后发布的反馈计入采用 |

## 3.3 运营后台

| 要求 | 状态 |
| --- | --- |
| 用户与订单 | done |
| 套餐与额度 | done |
| 服务配置中心（5.9） | 文本、生图、检索路由、审核词、来源名单 done；热点源 added（管理员填写地址，没配来源就关闭）；支付与邮件 needs-external-credentials |
| 买断与定制授权、权益调整 | 买断用管理员改档位 done；独立定制套餐合同字段 未做 |
| 邀请奖励配置与风控审核 | added。配置中心可改参数，冻结的邀请可通过或撤销 |
| 垂类、风格、模板 | done |
| 页面与法律文本版本化 | added |
| 同意记录查询 | added（按用户 ID） |
| 投诉与举报 | done |
| 成本看板与熔断 | added |
| 任务监控 | done（任务列表、失败通知） |
| 操作审计日志 | 写入 done；后台页面 added |
| 数据看板（注册、转化、续费、单篇成本） | added |
| AI 标识开关 | added（默认关） |

## 3.4 优先级里还没单独落地的

| 要求 | 状态 |
| --- | --- |
| P2 个人风格学习、事实核查加强、原创度 | done。事实核对仍在。高级版和买断版可提交 3 到 5 篇范文，只保存句长、问句和第一人称等特征。正文若整段重复来源，任务失败 |
| P2 多账号 | 高级档可绑多个号 done |
| P2 第三方平台授权 | needs-external-credentials。没配 `WECHAT_COMPONENT_*` 时关闭，AppID / AppSecret 仍可用 |
| P2 数据图表、授权引用图片 | 数据图表 未做。授权图片走用户素材库：上传 PNG 或 JPEG 并填写授权说明，写作计划可以指定为封面。自动抓图保持禁止 |

## 3.5 页面与法律

| 要求 | 状态 |
| --- | --- |
| 落地页、功能、定价、帮助、教程、投诉 | done。示例文章墙、关于我们独立页 未做；`page_about` 可发布文本 |
| 邀请落地页 | added。`/register?invite=` 带上邀请码，规则在会员页和邀请规则草案 |
| 找回账号 / 换绑手机 | 未做 |
| 六类法律文本 | done（草案） |
| 后台版本化，旧版本可查 | added |
| 注册主动勾选，consent_log 含版本、时间、IP、设备 | done |
| 条款重大变更重新同意 | added |
| 单独同意：绑号、自动发布 | done |
| 单独同意：读他人草稿标题、数据分析、设备指纹、修改差异学风格 | 草稿标题 done：同意后才读取本账号草稿标题并并入去重。数据分析、设备指纹、修改差异学风格 未做 |
| 注销删凭证、导出 | done |
| 仅限成年人 | done（注册声明） |
| 宣传页不写绝对化承诺 | done |
| ICP 备案 | 范围外（开发书写明延后） |

## 4 会员

| 要求 | 状态 |
| --- | --- |
| 四档价格与额度，后台可调 | done |
| 免费夜间、付费时段、高级优先 | done |
| 公众号数、方向数、字数上限 | done |
| 模型按档位路由 | done |
| 检索：免费无、普通基础、高级完整 | 免费不检索 done；普通和高级都走已配置的检索路由，没有再拆「基础 / 完整」两套深度 |
| 来源强制标注 | 付费档有检索时强制；免费不写具体来源 done |
| 认证号周报 / 自动调权重 | 未做 |
| 配图档位 | done |
| 风格学习仅高级 | done。免费和普通保存风格画像会被拒绝 |
| 风格与模板档位 | done |
| 事实核查 | done。原创度会拒绝与来源连续 40 字相同的正文 |
| AI 标识默认不加、开关可开 | added |
| 到期前提醒、宽限期、降级保留 | 宽限期与降级 done；到期前 7/3/1 天提醒 未做 |
| 升级补差、退款规则 | 升级立即生效 done；按剩余天数折算补差、7 天未使用全额退款 未做。管理员可标记已退款 |
| 买断与授权写审计 | done |
| 加量包 | 未做 |
| 4.1 成本测算表 | 文档假设，不是功能。熔断按真实用量 added |
| 4.2 邀请：+2 篇、上限 +20、付费不计、观察期、防刷 | added。虚拟号撤销，同设备或同一 IP 段当天满 3 个冻结，解绑、停用、曾绑定过的 AppID 撤销。观察满 7 天且写出 3 篇后才生效 |

## 5 系统设计

| 要求 | 状态 |
| --- | --- |
| 不自建模型、单体、数据库队列、协程池、时段打散 | done |
| 静态前端打进同一进程 | done |
| 图片不长期入库 | done |
| 批量接口 | done。同一 OpenAI 兼容服务上的多段文本可以合成一次调用，解析失败再退回逐条调用 |
| Go + Vue、SKIP LOCKED、敏感词、封面压缩 | done |
| Docker / compose、amd64 与 arm64 | 仓库有 Dockerfile 与 compose 的，以仓库文件为准；多架构发布 未做 |
| 健康检查、stdout 日志 | done |
| 5.6 提示词缓存、选题批量、检索缓存 24 小时 | done。非流式调用在进程内缓存 24 小时，相同提示词和检索不再打第二次。选题仍按计划逐篇生成 |
| 正文 30 天清理 | added |
| 5.8 表：user、wx_account、writing_plan、article_task、article、article_source、quota、order、quota_ledger、topic_coverage、vertical_pack、style、layout、legal_doc、consent_log、complaint、publish、service_config、plan_config、entitlement、feedback、audit_log | done |
| content_calendar 独立表 | 用规则预告，没有单独的月表 |
| mp_stats、ops_report | 未做 |
| referral | added |
| image_asset | 配图直接进 HTML，没有独立素材表 |
| prompt_version 表 | 版本号常量，没有 A/B 表 |
| site_page | added（用 legal_doc 的 `page_home` / `page_help` / `page_about`） |
| 5.9 统一 chat / image，密钥加密掩码，回退，连通测试，审计 | done |
| search、hot_topics 统一接口 | 检索走资料检索环节 done；热点 added，HTTP JSON 与远程 MCP，只读缓存 |
| 自定义 HTTP 模板 | done。协议 `http` 可用 `response_text`、`response_prompt_tokens`、`response_completion_tokens` 做点分字段映射 |
| 单价计入每篇并驱动熔断 | added |
| 5.10 环境变量、迁移、备份命令 | 迁移随启动执行 done；独立备份命令 未做 |

## 6 微信

| 要求 | 状态 |
| --- | --- |
| AppID / AppSecret、加密、解绑删除 | done |
| 绑定检测与唯一性、解绑 30 天 | done |
| 草稿失败改为复制 HTML | done |
| 手动发布提醒里的群发次数说明 | 帮助页说明 done；每条通知逐条附次数 未做 |
| 开放平台第三方扫码授权 | needs-external-credentials。票据到达后才能发起授权；刷新令牌加密保存 |
| 读他人草稿标题须同意 | done。`POST /api/me/consents/draft-titles` 同意后，生成前读取最多 20 条草稿标题 |
| token 失效暂停、草稿箱满、限频重试、内容检查重写 | 授权失效会标失败并通知 done；草稿箱开关与满员的专门探测 未做 |
| 服务号模板消息 | 未做，needs-external-credentials |
| 8.7 自动发布 | done（未经真实认证号实测） |

## 7 内容

| 要求 | 状态 |
| --- | --- |
| 分阶段流水线 | done。标题不是先出 15–20 个再打分，而是写作阶段一起生成 |
| 标题红线 | 敏感词与风险 done |
| 套话表、风格画像 | done。综上所述、在当今社会、不言而喻、值得一提的是会从正文去掉。风格画像见上 |
| 敏感领域拦截、垂类放行 | done |
| 不抓取仿写 | done |
| 免费少调用且不写具体政策数据 | done |
| 角标与文末来源、「.」改「点」 | done |
| 网址可访问性逐条请求 | done。正式模型调用时对来源网址做短请求，结果写进来源的可访问字段。超时记为未确认，不因此删来源 |
| 全托管定位方案一键确认 | 选方向即跑，没有单独的定位确认步 |
| 内容日历、90 天去重、风险分级 | done |
| 垂类规则包 | done |
| 表格、配图、失败兜底封面 | done |
| 数据图表、用户素材库 | 数据图表 未做。用户素材库 done，须填写授权说明，单张不超过 200KB |
| 7.10 授权引用图片 | done（用户上传并声明授权）。自动抓图保持关闭 |
| 7.11 认证号数据 | 未做，needs-external-credentials（认证号统计权限） |
| 7.12 热点多源 | added。多源去重，gov.cn 和两个来源以上优先。普通版取第一条，高级和买断取最高分。免费版不用热点 |
| 7.13 风格库、模板、站内预览、超长降级 | done |
| 试写 200 字、按栏目换风格、模板实测记录 | 试写和按栏目 done。试写默认每天 3 次，可用 `trial_daily_cap` 调整。栏目名和栏目风格留空时不切换。模板上线实测记录 未做 |

## 8 业务规则

| 要求 | 状态 |
| --- | --- |
| 额度预占、确认、失败退回、流水 | done |
| 每日 / 周期按用户合计 | done |
| 首次重写免费 | done |
| 邀请额度 | added。只加在免费版月额度上 |
| 加量包 90 天 | 未做 |
| 会员状态、宽限、降级 | done |
| 幂等、草稿更新不覆盖用户改过的稿 | done |
| 连续 5 篇无反馈自动暂停 | done。最近 5 篇成功稿都没有反馈时暂停并通知。已开启自动发布的计划不暂停。反馈里可以选「已处理」 |
| 选题确认模式 | added。仅高级版和买断版。提前推送，24 小时未处理按当前选题写，换一个不重置计时 |
| 常青选题与热点分开 | added。没配热点源或免费版时仍走常青轮换 |
| 注册样稿限一次、再引导绑定 | done。`POST /api/sample` 每个账号只生成一次站内样稿，不写公众号 |
| 采用率 / 修改率 | 反馈和回读 done；没有单独的 KPI 看板 |
| 自动发布规则 | done |

## 9–13 与附录

| 要求 | 状态 |
| --- | --- |
| 并发、隔离、凭证加密、日志不写密钥 | done |
| 单篇成本上限 | added（月上限熔断） |
| 可用性 99.5%、成功率 95% | 运行指标，不是代码开关 |
| 第 10 节风险对策里已有对应功能的 | 见上面各行 |
| 标识开关与日志 | added |
| 第 11 节排期 | 过程文档，不单独立项 |
| 第 12 节验收 | 邀请防刷、选题确认等待、热点缓存、短信登录和微信授权有测试；其余主路径沿用原测试 |
| 第 13 节待确认 | 保持开发书原文，不在代码里替运营方做主体和法务决定 |
| 附录竞品 | 调研记录，不实现 |

## 需要外部凭证才打开的

| 能力 | 变量 |
| --- | --- |
| 微信支付 | `WXPAY_MCH_ID`、`WXPAY_APP_ID`、`WXPAY_MCH_SERIAL`、`WXPAY_API_V3_KEY`、`WXPAY_PRIVATE_KEY` 或 `_FILE`、`WXPAY_PLATFORM_CERT` 或 `_FILE`、`WXPAY_NOTIFY_URL` |
| 支付宝 | `ALIPAY_APP_ID`、`ALIPAY_PRIVATE_KEY` 或 `_FILE`、`ALIPAY_PUBLIC_KEY` 或 `_FILE`、`ALIPAY_NOTIFY_URL`；电脑网站再加 `ALIPAY_RETURN_URL` |
| 邮件 | `SMTP_HOST`、`SMTP_PORT`、`SMTP_USER`、`SMTP_PASSWORD`、`SMTP_FROM` |
| 失败转发 | `NOTIFY_WEBHOOK_URL` |
| 真实模型 / 微信 | 配置中心或 `LLM_*`、`WX_APP_ID`、`WX_APP_SECRET` |
| 短信登录 | `SMS_PROVIDER`（`aliyun` 或 `tencent`）、`SMS_ACCESS_KEY`、`SMS_ACCESS_SECRET`、`SMS_SIGN_NAME`、`SMS_TEMPLATE_CODE`；腾讯云再加 `SMS_SDK_APP_ID`。可选 `SMS_REGION`、`SMS_API_BASE` |
| 微信扫码登录 | `WECHAT_OPEN_APP_ID`、`WECHAT_OPEN_APP_SECRET`、`WECHAT_OPEN_REDIRECT_URL`。接口基址用 `WECHAT_API_BASE` |
| 微信开放平台授权 | `WECHAT_COMPONENT_APP_ID`、`WECHAT_COMPONENT_APP_SECRET`、`WECHAT_COMPONENT_TOKEN`、`WECHAT_COMPONENT_AES_KEY`、`WECHAT_COMPONENT_REDIRECT_URL` |

没配这些变量时，对应按钮保持关闭或只走站内通知，进程照常启动。
