package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/pbkdf2"
)

func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	digest := pbkdf2.Key([]byte(password), salt, 100000, 32, sha256.New)
	return base64.StdEncoding.EncodeToString(salt) + "$" + base64.StdEncoding.EncodeToString(digest), nil
}

func VerifyPassword(password, stored string) bool {
	saltB64, digestB64, ok := strings.Cut(stored, "$")
	if !ok {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(saltB64)
	if err != nil {
		return false
	}
	expected, err := base64.StdEncoding.DecodeString(digestB64)
	if err != nil {
		return false
	}
	digest := pbkdf2.Key([]byte(password), salt, 100000, 32, sha256.New)
	if len(digest) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare(digest, expected) == 1
}

func fernetKeys(master string) (signKey, encKey []byte) {
	sum := sha256.Sum256([]byte(master))
	return sum[:16], sum[16:]
}

func pkcs7Pad(in []byte, block int) []byte {
	n := block - (len(in) % block)
	if n == 0 {
		n = block
	}
	out := make([]byte, len(in)+n)
	copy(out, in)
	for i := len(in); i < len(out); i++ {
		out[i] = byte(n)
	}
	return out
}

func pkcs7Unpad(in []byte, block int) ([]byte, error) {
	if len(in) == 0 || len(in)%block != 0 {
		return nil, errors.New("bad padding")
	}
	n := int(in[len(in)-1])
	if n == 0 || n > block || n > len(in) {
		return nil, errors.New("bad padding")
	}
	for _, b := range in[len(in)-n:] {
		if int(b) != n {
			return nil, errors.New("bad padding")
		}
	}
	return in[:len(in)-n], nil
}

// EncryptSecret 与 Python cryptography.Fernet 兼容：密钥为 sha256(master) 的 urlsafe base64。
func EncryptSecret(master, plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	signKey, encKey := fernetKeys(master)
	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return "", err
	}
	padded := pkcs7Pad([]byte(plain), aes.BlockSize)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	buf := make([]byte, 1+8+16+len(ct))
	buf[0] = 0x80
	binary.BigEndian.PutUint64(buf[1:9], uint64(time.Now().Unix()))
	copy(buf[9:25], iv)
	copy(buf[25:], ct)
	mac := hmac.New(sha256.New, signKey)
	mac.Write(buf)
	token := append(buf, mac.Sum(nil)...)
	return base64.URLEncoding.EncodeToString(token), nil
}

func DecryptSecret(master, token string) (string, error) {
	if token == "" {
		return "", nil
	}
	raw, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		raw, err = base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			return "", fmt.Errorf("无法解密凭证，请确认主密钥未变更")
		}
	}
	if len(raw) < 1+8+16+32 || raw[0] != 0x80 {
		return "", fmt.Errorf("无法解密凭证，请确认主密钥未变更")
	}
	signKey, encKey := fernetKeys(master)
	macStart := len(raw) - 32
	mac := hmac.New(sha256.New, signKey)
	mac.Write(raw[:macStart])
	if subtle.ConstantTimeCompare(mac.Sum(nil), raw[macStart:]) != 1 {
		return "", fmt.Errorf("无法解密凭证，请确认主密钥未变更")
	}
	iv := raw[9:25]
	ct := raw[25:macStart]
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return "", err
	}
	if len(ct)%aes.BlockSize != 0 {
		return "", fmt.Errorf("无法解密凭证，请确认主密钥未变更")
	}
	plain := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ct)
	plain, err = pkcs7Unpad(plain, aes.BlockSize)
	if err != nil {
		return "", fmt.Errorf("无法解密凭证，请确认主密钥未变更")
	}
	return string(plain), nil
}

func MaskSecret(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return "****"
	}
	return "****" + value[len(value)-4:]
}

func MakeToken(userID int64, secret string, days int) (string, error) {
	if days <= 0 {
		days = 7
	}
	claims := jwt.MapClaims{
		"sub": strconv.FormatInt(userID, 10),
		"exp": time.Now().Add(time.Duration(days) * 24 * time.Hour).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

func ReadToken(token, secret string) (int64, error) {
	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected alg")
		}
		return []byte(secret), nil
	})
	if err != nil || !parsed.Valid {
		return 0, fmt.Errorf("invalid token")
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return 0, fmt.Errorf("invalid claims")
	}
	switch sub := claims["sub"].(type) {
	case string:
		return strconv.ParseInt(sub, 10, 64)
	case float64:
		return int64(sub), nil
	default:
		return 0, fmt.Errorf("invalid sub")
	}
}
