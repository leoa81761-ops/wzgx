package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
)

// v1Open decrypts a v1 secret (Fernet-style token keyed by sha256(MASTER_KEY): first 16 bytes sign,
// last 16 bytes encrypt). The plaintext never leaves this process except re-sealed for v2.
func v1Open(master, token string) (string, error) {
	raw, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		if raw, err = base64.RawURLEncoding.DecodeString(token); err != nil {
			return "", errors.New("decode")
		}
	}
	if len(raw) < 57 || raw[0] != 0x80 {
		return "", errors.New("format")
	}
	sum := sha256.Sum256([]byte(master))
	sign, enc := sum[:16], sum[16:]
	ms := len(raw) - 32
	m := hmac.New(sha256.New, sign)
	m.Write(raw[:ms])
	if subtle.ConstantTimeCompare(m.Sum(nil), raw[ms:]) != 1 {
		return "", errors.New("mac mismatch (wrong V1_MASTER_KEY?)")
	}
	iv, ct := raw[9:25], raw[25:ms]
	if len(ct) == 0 || len(ct)%16 != 0 {
		return "", errors.New("length")
	}
	b, _ := aes.NewCipher(enc)
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(b, iv).CryptBlocks(pt, ct)
	n := int(pt[len(pt)-1])
	if n == 0 || n > 16 {
		return "", errors.New("padding")
	}
	return string(pt[:len(pt)-n]), nil
}
