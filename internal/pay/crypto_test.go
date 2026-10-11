package pay

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
)

func TestWeChatNotifyVerifiesAndDecrypts(t *testing.T) {
	priv, certPEM := testCert(t)
	key := "0123456789abcdef0123456789abcdef"
	plain := []byte(`{"out_trade_no":"p1abc","transaction_id":"4200001","trade_state":"SUCCESS","amount":{"total":3000,"currency":"CNY"}}`)
	nonce := "abcdefghijkl"
	aad := "transaction"
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	sealed := gcm.Seal(nil, []byte(nonce), plain, []byte(aad))
	body, _ := json.Marshal(map[string]any{
		"resource": map[string]string{
			"algorithm": "AEAD_AES_256_GCM", "ciphertext": base64.StdEncoding.EncodeToString(sealed),
			"nonce": nonce, "associated_data": aad,
		},
	})
	client := &WeChat{APIv3Key: key, Private: priv, Platform: &priv.PublicKey, PlatformCert: mustCert(t, certPEM)}
	header := http.Header{}
	now := time.Now()
	header.Set("Wechatpay-Timestamp", strconv.FormatInt(now.Unix(), 10))
	header.Set("Wechatpay-Nonce", "nonce-1")
	header.Set("Wechatpay-Serial", client.PlatformCert.SerialNumber.Text(16))
	sig, err := signSHA256(priv, header.Get("Wechatpay-Timestamp")+"\n"+header.Get("Wechatpay-Nonce")+"\n"+string(body)+"\n")
	if err != nil {
		t.Fatal(err)
	}
	header.Set("Wechatpay-Signature", sig)
	notice, err := client.ParseNotify(now, header, body)
	if err != nil {
		t.Fatal(err)
	}
	if !notice.Paid || notice.OutTradeNo != "p1abc" || notice.TradeNo != "4200001" || notice.AmountCents != 3000 {
		t.Fatalf("%#v", notice)
	}
	header.Set("Wechatpay-Signature", sig[:len(sig)-2]+"aa")
	if _, err := client.ParseNotify(now, header, body); err == nil {
		t.Fatal("坏签名应该失败")
	}
}

func TestAlipayNotifyVerifies(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	client := &Alipay{AppID: "2021001", Public: &priv.PublicKey, Private: priv}
	params := map[string]string{
		"app_id": "2021001", "out_trade_no": "p2", "trade_no": "20261011", "trade_status": "TRADE_SUCCESS",
		"total_amount": "30.00", "sign_type": "RSA2",
	}
	sig, err := signParams(priv, params)
	if err != nil {
		t.Fatal(err)
	}
	params["sign"] = sig
	values := url.Values{}
	for k, v := range params {
		values.Set(k, v)
	}
	notice, err := client.ParseNotify(values)
	if err != nil {
		t.Fatal(err)
	}
	if !notice.Paid || notice.AmountCents != 3000 || notice.OutTradeNo != "p2" {
		t.Fatalf("%#v", notice)
	}
	values.Set("total_amount", "31.00")
	if _, err := client.ParseNotify(values); err == nil {
		t.Fatal("改过的回调应该验签失败")
	}
}

func TestPrices(t *testing.T) {
	cents, ok := PriceCents(config.Settings{}, "standard", "month")
	if !ok || cents != 3000 {
		t.Fatal(cents, ok)
	}
	cents, ok = PriceCents(config.Settings{PriceLifetime: 123}, "lifetime", "once")
	if !ok || cents != 123 {
		t.Fatal(cents, ok)
	}
	if _, ok := PriceCents(config.Settings{}, "free", "month"); ok {
		t.Fatal("免费档不能下单")
	}
	if CentsToYuan(29900) != "299.00" {
		t.Fatal(CentsToYuan(29900))
	}
}

func testCert(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(0xab12),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func mustCert(t *testing.T, pemText string) *x509.Certificate {
	t.Helper()
	_, cert, err := parsePublicKey(pemText)
	if err != nil || cert == nil {
		t.Fatal(err)
	}
	return cert
}
