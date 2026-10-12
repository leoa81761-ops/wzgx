package pay

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
)

type WeChat struct {
	MchID        string
	AppID        string
	Serial       string
	APIv3Key     string
	NotifyURL    string
	APIBase      string
	Private      *rsa.PrivateKey
	Platform     *rsa.PublicKey
	PlatformCert *x509.Certificate
	HTTP         *http.Client
}

type Notice struct {
	OutTradeNo  string
	TradeNo     string
	AmountCents int
	Paid        bool
}

func NewWeChat(cfg config.Settings, client *http.Client) (*WeChat, error) {
	if !cfg.WeChatPayReady() {
		return nil, errors.New("微信支付未配置")
	}
	priv, err := parsePrivateKey(cfg.WxPayPrivateKey)
	if err != nil {
		return nil, err
	}
	pub, cert, err := parsePublicKey(cfg.WxPayPlatformCert)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(cfg.WxPayAPIBase, "/")
	if base == "" {
		base = "https://api.mch.weixin.qq.com"
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &WeChat{
		MchID: cfg.WxPayMchID, AppID: cfg.WxPayAppID, Serial: cfg.WxPayMchSerial, APIv3Key: cfg.WxPayAPIv3Key,
		NotifyURL: cfg.WxPayNotifyURL, APIBase: base, Private: priv, Platform: pub, PlatformCert: cert, HTTP: client,
	}, nil
}

func (w *WeChat) CreateNative(ctx context.Context, outTradeNo, subject string, cents int) (string, error) {
	payload := map[string]any{
		"appid": w.AppID, "mchid": w.MchID, "description": subject, "out_trade_no": outTradeNo,
		"notify_url": w.NotifyURL, "amount": map[string]any{"total": cents, "currency": "CNY"},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	endpoint := w.APIBase + "/v3/pay/transactions/native"
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := randomNonce()
	message := "POST\n" + u.RequestURI() + "\n" + ts + "\n" + nonce + "\n" + string(body) + "\n"
	sig, err := signSHA256(w.Private, message)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf(`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",signature="%s",timestamp="%s",serial_no="%s"`, w.MchID, nonce, sig, ts, w.Serial))
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var data map[string]any
	_ = json.Unmarshal(raw, &data)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := data["message"].(string)
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		if msg == "" {
			msg = "微信支付下单失败"
		}
		return "", errors.New(msg)
	}
	code, _ := data["code_url"].(string)
	if code == "" {
		return "", errors.New("微信支付没有返回收款码")
	}
	return code, nil
}

// Probe asks WeChat for an order number that does not exist. ORDER_NOT_EXIST means the signature was accepted.
func (w *WeChat) Probe(ctx context.Context) error {
	outTradeNo := "probe" + strconv.FormatInt(time.Now().UnixNano(), 10)
	endpoint := w.APIBase + "/v3/pay/transactions/out-trade-no/" + url.PathEscape(outTradeNo) + "?mchid=" + url.QueryEscape(w.MchID)
	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := randomNonce()
	message := "GET\n" + u.RequestURI() + "\n" + ts + "\n" + nonce + "\n\n"
	sig, err := signSHA256(w.Private, message)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf(`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",signature="%s",timestamp="%s",serial_no="%s"`, w.MchID, nonce, sig, ts, w.Serial))
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var data map[string]any
	_ = json.Unmarshal(raw, &data)
	code, _ := data["code"].(string)
	if resp.StatusCode == http.StatusNotFound && (code == "ORDER_NOT_EXIST" || code == "RESOURCE_NOT_EXISTS") {
		return nil
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	msg, _ := data["message"].(string)
	if msg == "" {
		msg = strings.TrimSpace(string(raw))
	}
	if msg == "" {
		msg = "微信支付连通失败"
	}
	return errors.New(msg)
}

func (w *WeChat) ParseNotify(now time.Time, header http.Header, body []byte) (Notice, error) {
	ts := strings.TrimSpace(header.Get("Wechatpay-Timestamp"))
	nonce := strings.TrimSpace(header.Get("Wechatpay-Nonce"))
	sig := strings.TrimSpace(header.Get("Wechatpay-Signature"))
	serial := strings.TrimSpace(header.Get("Wechatpay-Serial"))
	if ts == "" || nonce == "" || sig == "" {
		return Notice{}, errors.New("缺少微信支付签名头")
	}
	if w.PlatformCert != nil {
		want := strings.ToUpper(w.PlatformCert.SerialNumber.Text(16))
		if !strings.EqualFold(serial, want) {
			return Notice{}, errors.New("微信支付平台证书序列号不一致")
		}
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return Notice{}, errors.New("微信支付时间戳不正确")
	}
	if now.IsZero() {
		now = time.Now()
	}
	delta := now.Unix() - unix
	if delta < 0 {
		delta = -delta
	}
	if delta > 300 {
		return Notice{}, errors.New("微信支付回调时间超出范围")
	}
	message := ts + "\n" + nonce + "\n" + string(body) + "\n"
	if err := verifySHA256(w.Platform, message, sig); err != nil {
		return Notice{}, errors.New("微信支付签名校验失败")
	}
	var envelope struct {
		Resource struct {
			Algorithm      string `json:"algorithm"`
			Ciphertext     string `json:"ciphertext"`
			Nonce          string `json:"nonce"`
			AssociatedData string `json:"associated_data"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return Notice{}, errors.New("微信支付回调无法解析")
	}
	if envelope.Resource.Algorithm != "" && envelope.Resource.Algorithm != "AEAD_AES_256_GCM" {
		return Notice{}, errors.New("不支持的微信支付加密算法")
	}
	plain, err := decryptGCM(w.APIv3Key, envelope.Resource.Nonce, envelope.Resource.AssociatedData, envelope.Resource.Ciphertext)
	if err != nil {
		return Notice{}, err
	}
	var tx struct {
		OutTradeNo    string `json:"out_trade_no"`
		TransactionID string `json:"transaction_id"`
		TradeState    string `json:"trade_state"`
		Amount        struct {
			Total int `json:"total"`
		} `json:"amount"`
	}
	if err := json.Unmarshal(plain, &tx); err != nil {
		return Notice{}, errors.New("微信支付回调解密后无法解析")
	}
	return Notice{OutTradeNo: tx.OutTradeNo, TradeNo: tx.TransactionID, AmountCents: tx.Amount.Total, Paid: tx.TradeState == "SUCCESS"}, nil
}

func decryptGCM(apiV3Key, nonce, associated, ciphertextB64 string) ([]byte, error) {
	if len(apiV3Key) != 32 {
		return nil, errors.New("微信支付 APIv3 密钥必须是 32 字节")
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, errors.New("微信支付密文无法解码")
	}
	block, err := aes.NewCipher([]byte(apiV3Key))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("微信支付加密随机串长度不正确")
	}
	plain, err := gcm.Open(nil, []byte(nonce), raw, []byte(associated))
	if err != nil {
		return nil, errors.New("微信支付回调解密失败")
	}
	return plain, nil
}

func signSHA256(priv *rsa.PrivateKey, message string) (string, error) {
	sum := sha256.Sum256([]byte(message))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

func verifySHA256(pub *rsa.PublicKey, message, sigB64 string) error {
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(message))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig)
}

func randomNonce() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return hexEncode(buf[:])
}

func hexEncode(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = digits[v>>4]
		out[i*2+1] = digits[v&0x0f]
	}
	return string(out)
}
