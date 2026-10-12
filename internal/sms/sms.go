package sms

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
)

type Message struct {
	Phone string
	Code  string
}

func Send(ctx context.Context, client *http.Client, cfg config.Settings, msg Message) error {
	if !cfg.SMSEnabled() {
		return fmt.Errorf("短信登录未开通")
	}
	if client == nil {
		client = http.DefaultClient
	}
	switch cfg.SMSProvider {
	case "aliyun":
		return sendAliyun(ctx, client, cfg, msg)
	case "tencent":
		return sendTencent(ctx, client, cfg, msg)
	default:
		return fmt.Errorf("短信登录未开通")
	}
}

func sendAliyun(ctx context.Context, client *http.Client, cfg config.Settings, msg Message) error {
	endpoint := cfg.SMSAPIBase
	if endpoint == "" {
		endpoint = "https://dysmsapi.aliyuncs.com"
	}
	params := map[string]string{
		"AccessKeyId":      cfg.SMSAccessKey,
		"Action":           "SendSms",
		"Format":           "JSON",
		"PhoneNumbers":     msg.Phone,
		"RegionId":         "cn-hangzhou",
		"SignName":         cfg.SMSSignName,
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureNonce":   fmt.Sprintf("%d", time.Now().UnixNano()),
		"SignatureVersion": "1.0",
		"TemplateCode":     cfg.SMSTemplateCode,
		"TemplateParam":    fmt.Sprintf(`{"code":"%s"}`, msg.Code),
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"Version":          "2017-05-25",
	}
	params["Signature"] = signAliyun(params, cfg.SMSAccessSecret)
	values := url.Values{}
	for key, value := range params {
		values.Set(key, value)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/?"+values.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var data map[string]any
	_ = json.Unmarshal(raw, &data)
	if code, _ := data["Code"].(string); code != "OK" {
		message, _ := data["Message"].(string)
		if message == "" {
			message = "短信发送失败"
		}
		return fmt.Errorf("%s", message)
	}
	return nil
}

func signAliyun(params map[string]string, secret string) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		if key == "Signature" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, aliEscape(key)+"="+aliEscape(params[key]))
	}
	stringToSign := "GET&" + aliEscape("/") + "&" + aliEscape(strings.Join(parts, "&"))
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	_, _ = mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func aliEscape(value string) string {
	escaped := url.QueryEscape(value)
	escaped = strings.ReplaceAll(escaped, "+", "%20")
	escaped = strings.ReplaceAll(escaped, "*", "%2A")
	escaped = strings.ReplaceAll(escaped, "%7E", "~")
	return escaped
}

func sendTencent(ctx context.Context, client *http.Client, cfg config.Settings, msg Message) error {
	endpoint := cfg.SMSAPIBase
	if endpoint == "" {
		endpoint = "https://sms.tencentcloudapi.com"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("短信接口地址不正确")
	}
	body, _ := json.Marshal(map[string]any{
		"PhoneNumberSet":   []string{"+86" + msg.Phone},
		"SmsSdkAppId":      cfg.SMSSdkAppID,
		"SignName":         cfg.SMSSignName,
		"TemplateId":       cfg.SMSTemplateCode,
		"TemplateParamSet": []string{msg.Code},
	})
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	date := time.Unix(time.Now().Unix(), 0).UTC().Format("2006-01-02")
	authorization := signTencent(cfg.SMSAccessSecret, cfg.SMSAccessKey, parsed.Host, date, timestamp, body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/"), strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Host", parsed.Host)
	req.Header.Set("X-TC-Action", "SendSms")
	req.Header.Set("X-TC-Version", "2021-01-11")
	req.Header.Set("X-TC-Timestamp", timestamp)
	req.Header.Set("X-TC-Region", cfg.SMSRegion)
	req.Header.Set("Authorization", authorization)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var data struct {
		Response struct {
			Error *struct {
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"Response"`
	}
	if json.Unmarshal(raw, &data) != nil || data.Response.Error != nil {
		message := "短信发送失败"
		if data.Response.Error != nil && data.Response.Error.Message != "" {
			message = data.Response.Error.Message
		}
		return fmt.Errorf("%s", message)
	}
	return nil
}

func signTencent(secret, key, host, date, timestamp string, body []byte) string {
	hashed := sha256.Sum256(body)
	canonical := "POST\n/\n\ncontent-type:application/json\nhost:" + host + "\n\ncontent-type;host\n" + hex.EncodeToString(hashed[:])
	scope := date + "/sms/tc3_request"
	sum := sha256.Sum256([]byte(canonical))
	stringToSign := "TC3-HMAC-SHA256\n" + timestamp + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	secretDate := hmacSHA256([]byte("TC3"+secret), date)
	secretService := hmacSHA256(secretDate, "sms")
	secretSigning := hmacSHA256(secretService, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))
	return "TC3-HMAC-SHA256 Credential=" + key + "/" + scope + ", SignedHeaders=content-type;host, Signature=" + signature
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}
