package pay

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
)

type Alipay struct {
	AppID     string
	NotifyURL string
	ReturnURL string
	Gateway   string
	Private   *rsa.PrivateKey
	Public    *rsa.PublicKey
	HTTP      *http.Client
}

func NewAlipay(cfg config.Settings, client *http.Client) (*Alipay, error) {
	if !cfg.AlipayReady() {
		return nil, errors.New("支付宝未配置")
	}
	priv, err := parsePrivateKey(cfg.AlipayPrivateKey)
	if err != nil {
		return nil, err
	}
	pub, _, err := parsePublicKey(cfg.AlipayPublicKey)
	if err != nil {
		return nil, err
	}
	gateway := strings.TrimSpace(cfg.AlipayGateway)
	if gateway == "" {
		gateway = "https://openapi.alipay.com/gateway.do"
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &Alipay{AppID: cfg.AlipayAppID, NotifyURL: cfg.AlipayNotifyURL, ReturnURL: cfg.AlipayReturnURL, Gateway: gateway, Private: priv, Public: pub, HTTP: client}, nil
}

func (a *Alipay) Precreate(ctx context.Context, outTradeNo, subject string, cents int) (string, error) {
	biz, _ := json.Marshal(map[string]string{
		"out_trade_no": outTradeNo, "total_amount": CentsToYuan(cents), "subject": subject, "product_code": "FACE_TO_FACE_PAYMENT",
	})
	params := a.base("alipay.trade.precreate", string(biz))
	body := a.signedForm(params)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Gateway, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var data struct {
		Response struct {
			Code   string `json:"code"`
			Msg    string `json:"msg"`
			SubMsg string `json:"sub_msg"`
			QRCode string `json:"qr_code"`
		} `json:"alipay_trade_precreate_response"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", errors.New("支付宝下单结果无法解析")
	}
	if data.Response.Code != "10000" || data.Response.QRCode == "" {
		msg := data.Response.SubMsg
		if msg == "" {
			msg = data.Response.Msg
		}
		if msg == "" {
			msg = "支付宝下单失败"
		}
		return "", errors.New(msg)
	}
	return data.Response.QRCode, nil
}

func (a *Alipay) PagePayURL(outTradeNo, subject string, cents int) (string, error) {
	if strings.TrimSpace(a.ReturnURL) == "" {
		return "", errors.New("电脑网站支付还需要 ALIPAY_RETURN_URL")
	}
	biz, _ := json.Marshal(map[string]string{
		"out_trade_no": outTradeNo, "total_amount": CentsToYuan(cents), "subject": subject, "product_code": "FAST_INSTANT_TRADE_PAY",
	})
	params := a.base("alipay.trade.page.pay", string(biz))
	params["return_url"] = a.ReturnURL
	return a.Gateway + "?" + a.signedForm(params), nil
}

func (a *Alipay) ParseNotify(values url.Values) (Notice, error) {
	params := map[string]string{}
	for key, list := range values {
		if len(list) == 0 {
			continue
		}
		params[key] = list[0]
	}
	if err := verifyParams(a.Public, params); err != nil {
		return Notice{}, errors.New("支付宝签名校验失败")
	}
	if params["app_id"] != "" && params["app_id"] != a.AppID {
		return Notice{}, errors.New("支付宝应用编号不一致")
	}
	cents, err := YuanToCents(params["total_amount"])
	if err != nil {
		return Notice{}, err
	}
	state := params["trade_status"]
	paid := state == "TRADE_SUCCESS" || state == "TRADE_FINISHED"
	return Notice{OutTradeNo: params["out_trade_no"], TradeNo: params["trade_no"], AmountCents: cents, Paid: paid}, nil
}

func (a *Alipay) base(method, biz string) map[string]string {
	now := time.Now().In(schedule.Shanghai).Format("2006-01-02 15:04:05")
	return map[string]string{
		"app_id": a.AppID, "method": method, "format": "JSON", "charset": "utf-8", "sign_type": "RSA2",
		"timestamp": now, "version": "1.0", "notify_url": a.NotifyURL, "biz_content": biz,
	}
}

func (a *Alipay) signedForm(params map[string]string) string {
	sig, _ := signParams(a.Private, params)
	params["sign"] = sig
	return encodeParams(params)
}

func canonical(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for key, value := range params {
		if key == "sign" || key == "sign_type" || value == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+params[key])
	}
	return strings.Join(parts, "&")
}

func signParams(priv *rsa.PrivateKey, params map[string]string) (string, error) {
	sum := sha256.Sum256([]byte(canonical(params)))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

func verifyParams(pub *rsa.PublicKey, params map[string]string) error {
	sig, err := base64.StdEncoding.DecodeString(params["sign"])
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(canonical(params)))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig)
}

func encodeParams(params map[string]string) string {
	values := url.Values{}
	for key, value := range params {
		values.Set(key, value)
	}
	return values.Encode()
}
