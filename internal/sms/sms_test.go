package sms

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leoa81761-ops/wzgx/internal/config"
)

func TestAliyunAndTencentRequests(t *testing.T) {
	var aliyunPhone, tencentBody string
	aliyun := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aliyunPhone = r.URL.Query().Get("PhoneNumbers")
		_, _ = io.WriteString(w, `{"Code":"OK"}`)
	}))
	defer aliyun.Close()
	cfg := config.Settings{SMSProvider: "aliyun", SMSAccessKey: "id", SMSAccessSecret: "secret", SMSSignName: "公号智写", SMSTemplateCode: "SMS_1", SMSAPIBase: aliyun.URL}
	if !cfg.SMSEnabled() {
		t.Fatal("aliyun")
	}
	if err := Send(context.Background(), aliyun.Client(), cfg, Message{Phone: "13800138000", Code: "123456"}); err != nil {
		t.Fatal(err)
	}
	if aliyunPhone != "13800138000" {
		t.Fatal(aliyunPhone)
	}
	tencent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		tencentBody = string(raw)
		if r.Header.Get("X-TC-Action") != "SendSms" || !strings.Contains(r.Header.Get("Authorization"), "TC3-HMAC-SHA256") {
			http.Error(w, "bad", 400)
			return
		}
		_, _ = io.WriteString(w, `{"Response":{"SendStatusSet":[{"Code":"Ok"}]}}`)
	}))
	defer tencent.Close()
	cfg = config.Settings{SMSProvider: "tencent", SMSAccessKey: "id", SMSAccessSecret: "secret", SMSSignName: "公号智写", SMSTemplateCode: "99", SMSSdkAppID: "1400", SMSRegion: "ap-guangzhou", SMSAPIBase: tencent.URL}
	if err := Send(context.Background(), tencent.Client(), cfg, Message{Phone: "13800138000", Code: "654321"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tencentBody, "13800138000") || !strings.Contains(tencentBody, "654321") {
		t.Fatal(tencentBody)
	}
	if (config.Settings{}).SMSEnabled() {
		t.Fatal("off")
	}
}
