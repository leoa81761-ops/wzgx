package wxopen

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

type Profile struct {
	OpenID  string
	UnionID string
}

func ScanURL(appID, redirect, state string) string {
	values := url.Values{}
	values.Set("appid", appID)
	values.Set("redirect_uri", redirect)
	values.Set("response_type", "code")
	values.Set("scope", "snsapi_login")
	values.Set("state", state)
	return "https://open.weixin.qq.com/connect/qrconnect?" + values.Encode() + "#wechat_redirect"
}

func ExchangeScan(ctx context.Context, client *http.Client, apiBase, appID, secret, code string) (Profile, error) {
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := strings.TrimRight(apiBase, "/") + "/sns/oauth2/access_token?" + url.Values{
		"appid": {appID}, "secret": {secret}, "code": {code}, "grant_type": {"authorization_code"},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Profile{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Profile{}, err
	}
	defer resp.Body.Close()
	var data map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data); err != nil {
		return Profile{}, err
	}
	if codeNum, ok := data["errcode"].(float64); ok && codeNum != 0 {
		msg, _ := data["errmsg"].(string)
		return Profile{}, fmt.Errorf("微信扫码登录失败：%s", msg)
	}
	openID, _ := data["openid"].(string)
	unionID, _ := data["unionid"].(string)
	if openID == "" {
		return Profile{}, fmt.Errorf("微信没有返回身份")
	}
	return Profile{OpenID: openID, UnionID: unionID}, nil
}

type AuthInfo struct {
	AppID        string
	RefreshToken string
	Name         string
	ServiceType  string
	Verified     bool
}

func ComponentToken(ctx context.Context, client *http.Client, apiBase, appID, secret, ticket string) (string, error) {
	data, err := postJSON(ctx, client, strings.TrimRight(apiBase, "/")+"/cgi-bin/component/api_component_token", map[string]any{
		"component_appid": appID, "component_appsecret": secret, "component_verify_ticket": ticket,
	})
	if err != nil {
		return "", err
	}
	token, _ := data["component_access_token"].(string)
	if token == "" {
		return "", fmt.Errorf("没有拿到第三方平台调用凭证")
	}
	return token, nil
}

func PreAuthURL(ctx context.Context, client *http.Client, apiBase, appID, secret, ticket, redirect string) (string, error) {
	token, err := ComponentToken(ctx, client, apiBase, appID, secret, ticket)
	if err != nil {
		return "", err
	}
	data, err := postJSON(ctx, client, strings.TrimRight(apiBase, "/")+"/cgi-bin/component/api_create_preauthcode?component_access_token="+url.QueryEscape(token), map[string]any{
		"component_appid": appID,
	})
	if err != nil {
		return "", err
	}
	pre, _ := data["pre_auth_code"].(string)
	if pre == "" {
		return "", fmt.Errorf("没有拿到预授权码")
	}
	values := url.Values{}
	values.Set("component_appid", appID)
	values.Set("pre_auth_code", pre)
	values.Set("redirect_uri", redirect)
	values.Set("auth_type", "1")
	return "https://mp.weixin.qq.com/cgi-bin/componentloginpage?" + values.Encode(), nil
}

func QueryAuth(ctx context.Context, client *http.Client, apiBase, appID, secret, ticket, authCode string) (AuthInfo, error) {
	token, err := ComponentToken(ctx, client, apiBase, appID, secret, ticket)
	if err != nil {
		return AuthInfo{}, err
	}
	data, err := postJSON(ctx, client, strings.TrimRight(apiBase, "/")+"/cgi-bin/component/api_query_auth?component_access_token="+url.QueryEscape(token), map[string]any{
		"component_appid": appID, "authorization_code": authCode,
	})
	if err != nil {
		return AuthInfo{}, err
	}
	info, _ := data["authorization_info"].(map[string]any)
	if info == nil {
		return AuthInfo{}, fmt.Errorf("授权结果为空")
	}
	authorizer, _ := info["authorizer_appid"].(string)
	refresh, _ := info["authorizer_refresh_token"].(string)
	if authorizer == "" || refresh == "" {
		return AuthInfo{}, fmt.Errorf("授权结果缺少公众号身份")
	}
	detail, err := postJSON(ctx, client, strings.TrimRight(apiBase, "/")+"/cgi-bin/component/api_get_authorizer_info?component_access_token="+url.QueryEscape(token), map[string]any{
		"component_appid": appID, "authorizer_appid": authorizer,
	})
	out := AuthInfo{AppID: authorizer, RefreshToken: refresh, Name: authorizer, ServiceType: "subscription"}
	if err == nil {
		if nick, _ := detail["authorizer_info"].(map[string]any); nick != nil {
			if name, _ := nick["nick_name"].(string); name != "" {
				out.Name = name
			}
			if service, _ := nick["service_type_info"].(map[string]any); service != nil {
				if id, _ := service["id"].(float64); id == 2 {
					out.ServiceType = "service"
				}
			}
			if verify, _ := nick["verify_type_info"].(map[string]any); verify != nil {
				if id, _ := verify["id"].(float64); id == 0 {
					out.Verified = true
				}
			}
		}
	}
	return out, nil
}

func postJSON(ctx context.Context, client *http.Client, endpoint string, body map[string]any) (map[string]any, error) {
	if client == nil {
		client = http.DefaultClient
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var data map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data); err != nil {
		return nil, err
	}
	if code, ok := data["errcode"].(float64); ok && code != 0 {
		msg, _ := data["errmsg"].(string)
		return nil, fmt.Errorf("微信开放平台错误：%s", msg)
	}
	return data, nil
}

type Ticket struct {
	InfoType string
	Value    string
	AppID    string
}

func DecryptMessage(encodingAESKey, token, timestamp, nonce, signature, encrypted string) ([]byte, error) {
	expect := sha1Sign(token, timestamp, nonce, encrypted)
	if signature == "" || signature != expect {
		return nil, fmt.Errorf("签名不正确")
	}
	key, err := aesKey(encodingAESKey)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw)%block.BlockSize() != 0 {
		return nil, fmt.Errorf("密文长度不正确")
	}
	plain := make([]byte, len(raw))
	cipher.NewCBCDecrypter(block, key[:16]).CryptBlocks(plain, raw)
	plain, err = pkcs7Unpad(plain, 32)
	if err != nil {
		return nil, err
	}
	if len(plain) < 20 {
		return nil, fmt.Errorf("明文过短")
	}
	size := int(binary.BigEndian.Uint32(plain[16:20]))
	if size < 0 || 20+size > len(plain) {
		return nil, fmt.Errorf("明文长度不正确")
	}
	return plain[20 : 20+size], nil
}

func EncryptMessage(encodingAESKey, token, appID, timestamp, nonce, plain string) (encrypted, signature string, err error) {
	key, err := aesKey(encodingAESKey)
	if err != nil {
		return "", "", err
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", "", err
	}
	msg := []byte(plain)
	buf := make([]byte, 0, 20+len(msg)+len(appID))
	buf = append(buf, random...)
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(msg)))
	buf = append(buf, lenBuf...)
	buf = append(buf, msg...)
	buf = append(buf, []byte(appID)...)
	buf = pkcs7Pad(buf, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", err
	}
	out := make([]byte, len(buf))
	cipher.NewCBCEncrypter(block, key[:16]).CryptBlocks(out, buf)
	encrypted = base64.StdEncoding.EncodeToString(out)
	return encrypted, sha1Sign(token, timestamp, nonce, encrypted), nil
}

func ParseTicket(plain []byte) (Ticket, error) {
	var body struct {
		AppID    string `xml:"AppId"`
		InfoType string `xml:"InfoType"`
		Ticket   string `xml:"ComponentVerifyTicket"`
	}
	if err := xml.Unmarshal(plain, &body); err != nil {
		return Ticket{}, err
	}
	return Ticket{InfoType: body.InfoType, Value: body.Ticket, AppID: body.AppID}, nil
}

func ExtractEncrypt(raw []byte) string {
	var body struct {
		Encrypt string `xml:"Encrypt"`
	}
	if xml.Unmarshal(raw, &body) == nil && body.Encrypt != "" {
		return body.Encrypt
	}
	var jsonBody struct {
		Encrypt string `json:"Encrypt"`
	}
	if json.Unmarshal(raw, &jsonBody) == nil {
		return jsonBody.Encrypt
	}
	return ""
}

func sha1Sign(parts ...string) string {
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, "")))
	return fmt.Sprintf("%x", sum)
}

func aesKey(encodingAESKey string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("EncodingAESKey 不正确")
	}
	return key, nil
}

func pkcs7Pad(data []byte, block int) []byte {
	pad := block - len(data)%block
	if pad == 0 {
		pad = block
	}
	out := append([]byte{}, data...)
	for i := 0; i < pad; i++ {
		out = append(out, byte(pad))
	}
	return out
}

func pkcs7Unpad(data []byte, block int) ([]byte, error) {
	if len(data) == 0 || len(data)%block != 0 {
		return nil, fmt.Errorf("填充不正确")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > block || pad > len(data) {
		return nil, fmt.Errorf("填充不正确")
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return nil, fmt.Errorf("填充不正确")
		}
	}
	return data[:len(data)-pad], nil
}
