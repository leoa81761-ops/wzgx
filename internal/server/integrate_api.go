package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/integrate"
	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/notify"
	"github.com/leoa81761-ops/wzgx/internal/pay"
	"github.com/leoa81761-ops/wzgx/internal/security"
	"github.com/leoa81761-ops/wzgx/internal/sms"
	"github.com/leoa81761-ops/wzgx/internal/wxopen"
)

func (a *App) effective(ctx context.Context) config.Settings {
	if a == nil || a.Store == nil {
		return a.Cfg
	}
	rows, err := a.Store.ListIntegrations(ctx)
	if err != nil {
		log.Printf("读取服务配置失败: %v", err)
		return a.Cfg
	}
	if len(rows) == 0 {
		return integrate.WithCallbackDefaults(a.Cfg)
	}
	return integrate.Merge(a.Cfg, rows, a.Cfg.MasterKey)
}

func (a *App) integrationDocs(ctx context.Context) (map[string]integrate.Doc, error) {
	rows, err := a.Store.ListIntegrations(ctx)
	if err != nil {
		return nil, err
	}
	docs := map[string]integrate.Doc{}
	for _, row := range rows {
		docs[row.Name] = integrate.Decode(row, a.Cfg.MasterKey)
	}
	return docs, nil
}

func (a *App) adminIntegrations(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	docs, err := a.integrationDocs(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, 200, a.integrationView(r.Context(), docs))
	return nil
}

func (a *App) adminSaveIntegration(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	section := r.PathValue("section")
	if section != "register" && !knownSection(section) {
		return &AppError{Status: 404, Message: "找不到这项配置"}
	}
	var body struct {
		Fields map[string]string `json:"fields"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if body.Fields == nil {
		body.Fields = map[string]string{}
	}
	docs, err := a.integrationDocs(r.Context())
	if err != nil {
		return err
	}
	next, changed, err := integrate.Patch(section, docs[section], body.Fields)
	if err != nil {
		return bad(err.Error())
	}
	if section == "register" && next.Plain["verify"] == "1" {
		cfg := a.effective(r.Context())
		if !cfg.SMSEnabled() && strings.TrimSpace(cfg.SMTPHost) == "" {
			return bad("请先配好短信或邮件，再打开注册验证码")
		}
	}
	if len(changed) > 0 {
		params, err := integrate.Encode(next, a.Cfg.MasterKey)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := a.Store.UpsertIntegration(r.Context(), section, params, now); err != nil {
			return err
		}
		actor := admin.ID
		sort.Strings(changed)
		if err := a.Store.InsertAudit(r.Context(), nil, model.AuditLog{
			ActorID: &actor, Action: "integration_update", Target: "service_config:" + section,
			Detail: "keys: " + strings.Join(changed, ","), CreatedAt: now,
		}); err != nil {
			return err
		}
		docs[section] = next
	}
	writeJSON(w, 200, a.integrationView(r.Context(), docs))
	return nil
}

func (a *App) adminTestIntegration(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	section := r.PathValue("section")
	if !knownSection(section) {
		return &AppError{Status: 404, Message: "找不到这项配置"}
	}
	if !a.credTest.allow(strconv.FormatInt(admin.ID, 10)) {
		return bad("测试太频繁，请稍后再试")
	}
	var body struct {
		Phone string `json:"phone"`
	}
	if r.Body != nil {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if len(strings.TrimSpace(string(raw))) > 0 {
			if json.Unmarshal(raw, &body) != nil {
				return &AppError{Status: 422, Message: "请求参数不正确"}
			}
		}
	}
	cfg := a.effective(r.Context())
	if err := a.probeIntegration(r.Context(), section, cfg, admin, strings.TrimSpace(body.Phone)); err != nil {
		return bad(err.Error())
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "连通成功"})
	return nil
}

func (a *App) probeIntegration(ctx context.Context, section string, cfg config.Settings, admin *model.User, phone string) error {
	switch section {
	case "sms":
		if !cfg.SMSEnabled() {
			return errString("短信还没配齐")
		}
		if !phoneRe.MatchString(phone) {
			return errString("请填写要接收测试短信的手机号")
		}
		code, err := numericCode(6)
		if err != nil {
			return err
		}
		return sms.Send(ctx, a.HTTP, cfg, sms.Message{Phone: phone, Code: code})
	case "smtp":
		if strings.TrimSpace(cfg.SMTPHost) == "" {
			return errString("邮件还没配齐")
		}
		account := admin.Email
		if account == "" {
			account = a.Cfg.AdminEmail
		}
		if !strings.Contains(account, "@") {
			return errString("管理员账号不是邮箱，无法发送测试信")
		}
		return (notify.SMTP{Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Password: cfg.SMTPPassword, From: cfg.SMTPFrom}).Send(ctx, notify.Message{
			UserID: admin.ID, Account: account, Title: "服务配置连通测试", Body: "这是一封配置中心发出的测试邮件。",
		})
	case "wxpay":
		client, err := pay.NewWeChat(cfg, a.HTTP)
		if err != nil {
			return errString("微信支付还没配齐")
		}
		return client.Probe(ctx)
	case "alipay":
		client, err := pay.NewAlipay(cfg, a.HTTP)
		if err != nil {
			return errString("支付宝还没配齐")
		}
		return client.Probe(ctx)
	case "wechat_open":
		if !cfg.WeChatScanEnabled() {
			return errString("微信扫码登录还没配齐")
		}
		return fetchClientToken(ctx, a.HTTP, cfg.WeChatAPIBase, cfg.WeChatOpenAppID, cfg.WeChatOpenSecret)
	case "wechat_component":
		if !cfg.WeChatComponentEnabled() {
			return errString("微信第三方平台还没配齐")
		}
		ticket, err := a.Store.GetSetting(ctx, nil, "wechat_component_ticket")
		if err != nil {
			return err
		}
		if ticket == "" {
			return errString("还没有收到平台票据，微信推送 ticket 之后才能测连通")
		}
		_, err = wxopen.ComponentToken(ctx, a.HTTP, cfg.WeChatAPIBase, cfg.WeChatComponentAppID, cfg.WeChatComponentSecret, ticket)
		return err
	case "wechat_tpl":
		if !cfg.TemplateEnabled() {
			return errString("模板消息还没配齐")
		}
		return fetchClientToken(ctx, a.HTTP, cfg.TplAPIBase, cfg.TplAppID, cfg.TplSecret)
	case "search":
		rows, err := llm.SearchChain(ctx, a.HTTP, searchTargets(cfg), "综合布线验收")
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return errString("检索没有返回结果")
		}
		return nil
	case "notify":
		if strings.TrimSpace(cfg.NotifyWebhookURL) == "" {
			return errString("失败转发还没配置")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.NotifyWebhookURL, strings.NewReader(`{"kind":"config_test","title":"服务配置连通测试"}`))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := a.HTTP.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode >= 300 {
			return errString("转发地址返回 " + strconv.Itoa(resp.StatusCode))
		}
		return nil
	default:
		return errString("这项不能测试连通")
	}
}

func fetchClientToken(ctx context.Context, client *http.Client, base, appID, secret string) error {
	if client == nil {
		client = http.DefaultClient
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = "https://api.weixin.qq.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/cgi-bin/token?grant_type=client_credential&appid="+appID+"&secret="+secret, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var data map[string]any
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data) != nil {
		return errString("凭证接口没有返回 JSON")
	}
	if code, ok := data["errcode"].(float64); ok && code != 0 {
		msg, _ := data["errmsg"].(string)
		if msg == "" {
			msg = "凭证被拒绝"
		}
		return errString(msg)
	}
	token, _ := data["access_token"].(string)
	if token == "" {
		return errString("没有拿到调用凭证")
	}
	return nil
}

func (a *App) integrationView(ctx context.Context, docs map[string]integrate.Doc) map[string]any {
	cfg := integrate.Apply(a.Cfg, docs)
	sections := []map[string]any{}
	for _, spec := range integrate.Sections() {
		doc := docs[spec.Key]
		fields := []map[string]any{}
		for _, field := range spec.Fields {
			stored := ""
			if field.Secret {
				stored = doc.Secret[field.Key]
			} else {
				stored = doc.Plain[field.Key]
			}
			env := integrate.EnvValue(a.Cfg, spec.Key, field.Key)
			if spec.Key == "smtp" && field.Key == "port" && (env == "0" || env == "") {
				env = ""
			}
			effective := stored
			source := ""
			if stored != "" {
				source = "db"
			} else if env != "" {
				effective = env
				source = "env"
			}
			item := map[string]any{
				"key": field.Key, "label": field.Label, "secret": field.Secret, "multiline": field.Multiline,
				"source": source, "set": effective != "",
			}
			placeholder := ""
			if spec.Key == "public" && field.Key == "base_url" {
				effective = stored
				if stored != "" {
					source = "db"
				} else {
					source = ""
				}
				placeholder = integrate.PublicBaseHint
				hint := "留空则使用环境变量 PUBLIC_BASE_URL。生产环境为 " + integrate.PublicBaseHint + "。"
				if env != "" {
					hint = "当前环境变量：" + env + "。" + hint
				}
				item["source"] = source
				item["set"] = stored != ""
				item["hint"] = hint
			}
			if field.Secret {
				item["value"] = ""
				item["hint"] = security.MaskSecret(effective)
			} else if spec.Key != "public" || field.Key != "base_url" {
				item["value"] = effective
				item["hint"] = ""
			} else {
				item["value"] = effective
			}
			if placeholder == "" && effective == "" {
				placeholder = integrate.CallbackDefault(cfg.PublicBaseURL, spec.Key, field.Key)
			}
			item["placeholder"] = placeholder
			fields = append(fields, item)
		}
		row := map[string]any{
			"key": spec.Key, "title": spec.Title, "ready": integrate.Ready(spec.Key, cfg), "fields": fields,
		}
		if doc.DecryptErr {
			row["error"] = "无法解密凭证，请确认主密钥未变更"
		}
		sections = append(sections, row)
	}
	smsReady := cfg.SMSEnabled()
	emailReady := strings.TrimSpace(cfg.SMTPHost) != ""
	reason := ""
	note := ""
	if !smsReady && !emailReady {
		reason = "短信和邮件都还没配好，不能打开注册验证码"
	} else if !smsReady {
		note = "短信还没配好。开关打开后，手机号不能注册。"
	} else if !emailReady {
		note = "邮件还没配好。开关打开后，邮箱不能注册。"
	}
	verify := false
	if doc, ok := docs["register"]; ok && (doc.Plain["verify"] == "1" || doc.Plain["verify"] == "true" || doc.Plain["verify"] == "on") {
		verify = true
	}
	return map[string]any{
		"sections": sections,
		"register": map[string]any{
			"verify": verify, "sms_ready": smsReady, "email_ready": emailReady, "reason": reason, "note": note,
		},
	}
}

func searchTargets(cfg config.Settings) []llm.SearchTarget {
	var out []llm.SearchTarget
	if target, ok := llm.ConfiguredSearch(cfg.ResearchSearchProvider, cfg.ResearchSearchAPI, cfg.ResearchSearchKey); ok {
		out = append(out, target)
	}
	for _, raw := range []string{cfg.ResearchBackupSearch, cfg.ResearchBackupSearchAlt, cfg.ResearchBackupSearchBaidu} {
		if strings.TrimSpace(raw) != "" {
			out = append(out, llm.SearchTarget{URL: raw})
		}
	}
	return out
}

func knownSection(key string) bool {
	for _, item := range integrate.Sections() {
		if item.Key == key {
			return true
		}
	}
	return false
}

type errString string

func (e errString) Error() string { return string(e) }
