package integrate

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/pay"
	"github.com/leoa81761-ops/wzgx/internal/security"
)

const Type = "integration"

type Field struct {
	Key       string
	Label     string
	Secret    bool
	Multiline bool
}

type Section struct {
	Key    string
	Title  string
	Fields []Field
}

type Doc struct {
	Plain      map[string]string
	Secret     map[string]string
	DecryptErr bool
}

func Sections() []Section {
	return []Section{
		{Key: "public", Title: "站点地址", Fields: []Field{
			{Key: "base_url", Label: "PUBLIC_BASE_URL"},
		}},
		{Key: "sms", Title: "短信", Fields: []Field{
			{Key: "provider", Label: "服务商（aliyun 或 tencent）"},
			{Key: "access_key", Label: "AccessKey", Secret: true},
			{Key: "access_secret", Label: "AccessSecret", Secret: true},
			{Key: "sign_name", Label: "签名"},
			{Key: "template_code", Label: "模板编号"},
			{Key: "sdk_app_id", Label: "腾讯云 SdkAppId"},
			{Key: "region", Label: "腾讯云地域"},
			{Key: "api_base", Label: "接口地址（可选）"},
		}},
		{Key: "smtp", Title: "邮件", Fields: []Field{
			{Key: "host", Label: "SMTP 主机"},
			{Key: "port", Label: "端口"},
			{Key: "user", Label: "用户名"},
			{Key: "password", Label: "密码", Secret: true},
			{Key: "from", Label: "发件人"},
		}},
		{Key: "wxpay", Title: "微信支付", Fields: []Field{
			{Key: "mch_id", Label: "商户号"},
			{Key: "app_id", Label: "应用 AppID"},
			{Key: "serial", Label: "商户证书序列号"},
			{Key: "api_v3_key", Label: "APIv3 密钥（32 字节）", Secret: true},
			{Key: "private_key", Label: "商户私钥 PEM", Secret: true, Multiline: true},
			{Key: "platform_cert", Label: "平台证书 PEM", Secret: true, Multiline: true},
			{Key: "notify_url", Label: "回调地址"},
			{Key: "api_base", Label: "接口地址（可选）"},
		}},
		{Key: "alipay", Title: "支付宝", Fields: []Field{
			{Key: "app_id", Label: "应用 AppID"},
			{Key: "private_key", Label: "应用私钥 PEM", Secret: true, Multiline: true},
			{Key: "public_key", Label: "支付宝公钥 PEM", Secret: true, Multiline: true},
			{Key: "notify_url", Label: "回调地址"},
			{Key: "return_url", Label: "电脑网站回跳地址"},
			{Key: "gateway", Label: "网关地址（可选）"},
		}},
		{Key: "wechat_open", Title: "微信扫码登录", Fields: []Field{
			{Key: "app_id", Label: "网站应用 AppID"},
			{Key: "secret", Label: "AppSecret", Secret: true},
			{Key: "redirect_url", Label: "回调地址"},
		}},
		{Key: "wechat_component", Title: "微信第三方平台", Fields: []Field{
			{Key: "app_id", Label: "第三方平台 AppID"},
			{Key: "secret", Label: "AppSecret", Secret: true},
			{Key: "token", Label: "消息校验 Token", Secret: true},
			{Key: "aes_key", Label: "消息加解密 Key（43 位）", Secret: true},
			{Key: "redirect_url", Label: "授权回调地址"},
		}},
		{Key: "wechat_tpl", Title: "模板消息", Fields: []Field{
			{Key: "app_id", Label: "服务号 AppID"},
			{Key: "secret", Label: "AppSecret", Secret: true},
			{Key: "template_id", Label: "模板 ID"},
			{Key: "api_base", Label: "接口地址（可选）"},
			{Key: "fields", Label: "字段名，逗号分隔"},
		}},
		{Key: "notify", Title: "失败转发", Fields: []Field{
			{Key: "webhook_url", Label: "Webhook 地址", Secret: true},
		}},
		{Key: "search", Title: "备用检索", Fields: []Field{
			{Key: "provider", Label: "检索接口（bocha / tavily / bing），填了密钥就优先用"},
			{Key: "api_url", Label: "接口地址，留空则用该接口的默认地址"},
			{Key: "api_key", Label: "接口密钥", Secret: true},
			{Key: "primary_url", Label: "必应 HTML（国内默认可达）"},
			{Key: "backup_url", Label: "搜狗 HTML"},
			{Key: "baidu_url", Label: "百度 HTML"},
		}},
	}
}

func sectionBy(key string) (Section, bool) {
	for _, item := range Sections() {
		if item.Key == key {
			return item, true
		}
	}
	return Section{}, false
}

func Decode(row model.ServiceConfig, master string) Doc {
	doc := Doc{Plain: map[string]string{}, Secret: map[string]string{}}
	if row.ExtraParams == nil {
		return doc
	}
	doc.Plain = stringMap(row.ExtraParams["plain"])
	enc := stringMap(row.ExtraParams["secret"])
	for key, token := range enc {
		plain, err := security.DecryptSecret(master, token)
		if err != nil || plain == "" {
			doc.DecryptErr = true
			continue
		}
		doc.Secret[key] = plain
	}
	return doc
}

func Encode(doc Doc, master string) (map[string]any, error) {
	secret := map[string]string{}
	for key, plain := range doc.Secret {
		if strings.TrimSpace(plain) == "" {
			continue
		}
		enc, err := security.EncryptSecret(master, plain)
		if err != nil {
			return nil, err
		}
		secret[key] = enc
	}
	plain := map[string]string{}
	for key, value := range doc.Plain {
		if strings.TrimSpace(value) != "" {
			plain[key] = strings.TrimSpace(value)
		}
	}
	return map[string]any{"plain": plain, "secret": secret}, nil
}

// Merge keeps base values and lets a non-empty saved field replace them.
func Merge(base config.Settings, rows []model.ServiceConfig, master string) config.Settings {
	docs := map[string]Doc{}
	for _, row := range rows {
		if row.ConfigType != Type || row.Name == "" {
			continue
		}
		docs[row.Name] = Decode(row, master)
	}
	return Apply(base, docs)
}

func Apply(base config.Settings, docs map[string]Doc) config.Settings {
	out := base
	set := func(section, key string) string {
		doc := docs[section]
		if doc.Plain == nil && doc.Secret == nil {
			return ""
		}
		if v := strings.TrimSpace(doc.Plain[key]); v != "" {
			return v
		}
		return strings.TrimSpace(doc.Secret[key])
	}
	if v := strings.ToLower(set("sms", "provider")); v != "" {
		out.SMSProvider = v
	}
	if v := set("sms", "access_key"); v != "" {
		out.SMSAccessKey = v
	}
	if v := set("sms", "access_secret"); v != "" {
		out.SMSAccessSecret = v
	}
	if v := set("sms", "sign_name"); v != "" {
		out.SMSSignName = v
	}
	if v := set("sms", "template_code"); v != "" {
		out.SMSTemplateCode = v
	}
	if v := set("sms", "sdk_app_id"); v != "" {
		out.SMSSdkAppID = v
	}
	if v := set("sms", "region"); v != "" {
		out.SMSRegion = v
	}
	if v := set("sms", "api_base"); v != "" {
		out.SMSAPIBase = v
	}
	if v := set("smtp", "host"); v != "" {
		out.SMTPHost = v
	}
	if v := set("smtp", "port"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			out.SMTPPort = n
		}
	}
	if v := set("smtp", "user"); v != "" {
		out.SMTPUser = v
	}
	if v := set("smtp", "password"); v != "" {
		out.SMTPPassword = v
	}
	if v := set("smtp", "from"); v != "" {
		out.SMTPFrom = v
	}
	if v := set("wxpay", "mch_id"); v != "" {
		out.WxPayMchID = v
	}
	if v := set("wxpay", "app_id"); v != "" {
		out.WxPayAppID = v
	}
	if v := set("wxpay", "serial"); v != "" {
		out.WxPayMchSerial = v
	}
	if v := set("wxpay", "api_v3_key"); v != "" {
		out.WxPayAPIv3Key = v
	}
	if v := set("wxpay", "private_key"); v != "" {
		out.WxPayPrivateKey = v
	}
	if v := set("wxpay", "platform_cert"); v != "" {
		out.WxPayPlatformCert = v
	}
	if v := set("wxpay", "notify_url"); v != "" {
		out.WxPayNotifyURL = v
	}
	if v := set("wxpay", "api_base"); v != "" {
		out.WxPayAPIBase = v
	}
	if v := set("alipay", "app_id"); v != "" {
		out.AlipayAppID = v
	}
	if v := set("alipay", "private_key"); v != "" {
		out.AlipayPrivateKey = v
	}
	if v := set("alipay", "public_key"); v != "" {
		out.AlipayPublicKey = v
	}
	if v := set("alipay", "notify_url"); v != "" {
		out.AlipayNotifyURL = v
	}
	if v := set("alipay", "return_url"); v != "" {
		out.AlipayReturnURL = v
	}
	if v := set("alipay", "gateway"); v != "" {
		out.AlipayGateway = v
	}
	if v := set("wechat_open", "app_id"); v != "" {
		out.WeChatOpenAppID = v
	}
	if v := set("wechat_open", "secret"); v != "" {
		out.WeChatOpenSecret = v
	}
	if v := set("wechat_open", "redirect_url"); v != "" {
		out.WeChatOpenRedirect = v
	}
	if v := set("wechat_component", "app_id"); v != "" {
		out.WeChatComponentAppID = v
	}
	if v := set("wechat_component", "secret"); v != "" {
		out.WeChatComponentSecret = v
	}
	if v := set("wechat_component", "token"); v != "" {
		out.WeChatComponentToken = v
	}
	if v := set("wechat_component", "aes_key"); v != "" {
		out.WeChatComponentAESKey = v
	}
	if v := set("wechat_component", "redirect_url"); v != "" {
		out.WeChatComponentRedirect = v
	}
	if v := set("wechat_tpl", "app_id"); v != "" {
		out.TplAppID = v
	}
	if v := set("wechat_tpl", "secret"); v != "" {
		out.TplSecret = v
	}
	if v := set("wechat_tpl", "template_id"); v != "" {
		out.TplID = v
	}
	if v := set("wechat_tpl", "api_base"); v != "" {
		out.TplAPIBase = v
	}
	if v := set("wechat_tpl", "fields"); v != "" {
		out.TplFields = v
	}
	if v := set("notify", "webhook_url"); v != "" {
		out.NotifyWebhookURL = v
	}
	if v := set("public", "base_url"); v != "" {
		out.PublicBaseURL = strings.TrimRight(v, "/")
	}
	if v := set("search", "primary_url"); v != "" {
		out.ResearchBackupSearch = v
	}
	if v := set("search", "backup_url"); v != "" {
		out.ResearchBackupSearchAlt = v
	}
	if v := set("search", "baidu_url"); v != "" {
		out.ResearchBackupSearchBaidu = v
	}
	if v := set("search", "api_url"); v != "" {
		out.ResearchSearchAPI = v
	}
	if v := set("search", "api_key"); v != "" {
		out.ResearchSearchKey = v
	}
	if v := set("search", "provider"); v != "" {
		out.ResearchSearchProvider = strings.ToLower(v)
	}
	out = WithCallbackDefaults(out)
	if doc, ok := docs["register"]; ok {
		if v, exists := doc.Plain["verify"]; exists {
			out.RegisterVerify = v == "1" || v == "true" || v == "on"
		}
	}
	return out
}

func EnvValue(base config.Settings, section, key string) string {
	switch section + "." + key {
	case "sms.provider":
		return base.SMSProvider
	case "sms.access_key":
		return base.SMSAccessKey
	case "sms.access_secret":
		return base.SMSAccessSecret
	case "sms.sign_name":
		return base.SMSSignName
	case "sms.template_code":
		return base.SMSTemplateCode
	case "sms.sdk_app_id":
		return base.SMSSdkAppID
	case "sms.region":
		return base.SMSRegion
	case "sms.api_base":
		return base.SMSAPIBase
	case "smtp.host":
		return base.SMTPHost
	case "smtp.port":
		if base.SMTPPort > 0 {
			return strconv.Itoa(base.SMTPPort)
		}
		return ""
	case "smtp.user":
		return base.SMTPUser
	case "smtp.password":
		return base.SMTPPassword
	case "smtp.from":
		return base.SMTPFrom
	case "wxpay.mch_id":
		return base.WxPayMchID
	case "wxpay.app_id":
		return base.WxPayAppID
	case "wxpay.serial":
		return base.WxPayMchSerial
	case "wxpay.api_v3_key":
		return base.WxPayAPIv3Key
	case "wxpay.private_key":
		return base.WxPayPrivateKey
	case "wxpay.platform_cert":
		return base.WxPayPlatformCert
	case "wxpay.notify_url":
		return base.WxPayNotifyURL
	case "wxpay.api_base":
		return base.WxPayAPIBase
	case "alipay.app_id":
		return base.AlipayAppID
	case "alipay.private_key":
		return base.AlipayPrivateKey
	case "alipay.public_key":
		return base.AlipayPublicKey
	case "alipay.notify_url":
		return base.AlipayNotifyURL
	case "alipay.return_url":
		return base.AlipayReturnURL
	case "alipay.gateway":
		return base.AlipayGateway
	case "wechat_open.app_id":
		return base.WeChatOpenAppID
	case "wechat_open.secret":
		return base.WeChatOpenSecret
	case "wechat_open.redirect_url":
		return base.WeChatOpenRedirect
	case "wechat_component.app_id":
		return base.WeChatComponentAppID
	case "wechat_component.secret":
		return base.WeChatComponentSecret
	case "wechat_component.token":
		return base.WeChatComponentToken
	case "wechat_component.aes_key":
		return base.WeChatComponentAESKey
	case "wechat_component.redirect_url":
		return base.WeChatComponentRedirect
	case "wechat_tpl.app_id":
		return base.TplAppID
	case "wechat_tpl.secret":
		return base.TplSecret
	case "wechat_tpl.template_id":
		return base.TplID
	case "wechat_tpl.api_base":
		return base.TplAPIBase
	case "wechat_tpl.fields":
		return base.TplFields
	case "notify.webhook_url":
		return base.NotifyWebhookURL
	case "public.base_url":
		return base.PublicBaseURL
	case "search.primary_url":
		return base.ResearchBackupSearch
	case "search.backup_url":
		return base.ResearchBackupSearchAlt
	case "search.baidu_url":
		return base.ResearchBackupSearchBaidu
	case "search.api_url":
		return base.ResearchSearchAPI
	case "search.api_key":
		return base.ResearchSearchKey
	case "search.provider":
		return base.ResearchSearchProvider
	default:
		return ""
	}
}

// Patch applies a save. A blank secret keeps the stored secret. Changed keys are names only.
func Patch(section string, old Doc, incoming map[string]string) (Doc, []string, error) {
	spec, ok := sectionBy(section)
	if !ok && section != "register" {
		return Doc{}, nil, fmt.Errorf("未知配置")
	}
	next := Doc{Plain: map[string]string{}, Secret: map[string]string{}}
	for key, value := range old.Plain {
		next.Plain[key] = value
	}
	for key, value := range old.Secret {
		next.Secret[key] = value
	}
	var changed []string
	if section == "register" {
		raw, ok := incoming["verify"]
		if !ok {
			return next, nil, nil
		}
		raw = strings.TrimSpace(raw)
		value := "0"
		if raw == "1" || raw == "true" || raw == "on" {
			value = "1"
		}
		if next.Plain["verify"] != value {
			next.Plain["verify"] = value
			changed = append(changed, "verify")
		}
		return next, changed, nil
	}
	allowed := map[string]Field{}
	for _, field := range spec.Fields {
		allowed[field.Key] = field
	}
	for key, raw := range incoming {
		field, ok := allowed[key]
		if !ok {
			continue
		}
		value := strings.TrimSpace(raw)
		if field.Secret {
			if value == "" || value == next.Secret[key] {
				continue
			}
			if err := validate(section, key, value); err != nil {
				return Doc{}, nil, err
			}
			next.Secret[key] = value
			changed = append(changed, key)
			continue
		}
		if value == next.Plain[key] {
			continue
		}
		if value != "" {
			if err := validate(section, key, value); err != nil {
				return Doc{}, nil, err
			}
			next.Plain[key] = value
		} else {
			delete(next.Plain, key)
		}
		changed = append(changed, key)
	}
	sort.Strings(changed)
	return next, changed, nil
}

func validate(section, key, value string) error {
	if len(value) > 20000 {
		return fmt.Errorf("内容过长")
	}
	switch section + "." + key {
	case "sms.provider":
		if value != "aliyun" && value != "tencent" {
			return fmt.Errorf("短信服务商只能是 aliyun 或 tencent")
		}
	case "search.provider":
		switch strings.ToLower(value) {
		case "bocha", "tavily", "bing":
		default:
			return fmt.Errorf("检索接口只能是 bocha、tavily 或 bing")
		}
	case "wechat_component.aes_key":
		if len(value) != 43 {
			return fmt.Errorf("消息加解密 Key 必须是 43 位")
		}
	case "wxpay.api_v3_key":
		if len(value) != 32 {
			return fmt.Errorf("APIv3 密钥必须是 32 字节")
		}
	case "wxpay.private_key", "alipay.private_key":
		if err := pay.ValidPrivateKey(value); err != nil {
			return fmt.Errorf("私钥 PEM 无法解析")
		}
	case "wxpay.platform_cert", "alipay.public_key":
		if err := pay.ValidPublicKey(value); err != nil {
			return fmt.Errorf("PEM 无法解析")
		}
	case "smtp.port":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("端口不正确")
		}
	case "smtp.host":
		if strings.Contains(value, "://") || strings.ContainsAny(value, " /") {
			return fmt.Errorf("SMTP 主机不正确")
		}
	case "public.base_url", "wxpay.notify_url", "wxpay.api_base", "alipay.notify_url", "alipay.return_url", "alipay.gateway",
		"wechat_open.redirect_url", "wechat_component.redirect_url", "wechat_tpl.api_base", "sms.api_base", "notify.webhook_url",
		"search.primary_url", "search.backup_url", "search.baidu_url", "search.api_url":
		if err := httpURL(value); err != nil {
			return err
		}
	default:
		if len(value) > 512 && key != "private_key" && key != "platform_cert" && key != "public_key" {
			return fmt.Errorf("内容过长")
		}
	}
	return nil
}

func httpURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("地址需要以 http:// 或 https:// 开头")
	}
	return nil
}

func stringMap(v any) map[string]string {
	raw, ok := v.(map[string]any)
	if !ok {
		return map[string]string{}
	}
	out := map[string]string{}
	for key, item := range raw {
		if s, ok := item.(string); ok {
			out[key] = s
		}
	}
	return out
}

func Ready(section string, cfg config.Settings) bool {
	switch section {
	case "sms":
		return cfg.SMSEnabled()
	case "smtp":
		return strings.TrimSpace(cfg.SMTPHost) != ""
	case "wxpay":
		if !cfg.WeChatPayReady() {
			return false
		}
		_, err := pay.NewWeChat(cfg, nil)
		return err == nil
	case "alipay":
		if !cfg.AlipayReady() {
			return false
		}
		_, err := pay.NewAlipay(cfg, nil)
		return err == nil
	case "wechat_open":
		return cfg.WeChatScanEnabled()
	case "wechat_component":
		return cfg.WeChatComponentEnabled()
	case "wechat_tpl":
		return cfg.TemplateEnabled()
	case "notify":
		return strings.TrimSpace(cfg.NotifyWebhookURL) != ""
	case "public":
		return strings.TrimSpace(cfg.PublicBaseURL) != ""
	case "search":
		keyed := strings.TrimSpace(cfg.ResearchSearchKey) != "" && (strings.TrimSpace(cfg.ResearchSearchAPI) != "" || strings.TrimSpace(cfg.ResearchSearchProvider) != "")
		return strings.TrimSpace(cfg.ResearchBackupSearch) != "" || keyed
	default:
		return false
	}
}
