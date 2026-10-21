// Package crypto encrypts third-party credentials at rest and masks them for display.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"unicode/utf8"
)

// Box seals secrets with AES-256-GCM. The key is derived from the master key.
type Box struct{ aead cipher.AEAD }

const prefix = "v1:"

func NewBox(masterKey string) (*Box, error) {
	if len(masterKey) < 32 {
		return nil, errors.New("crypto: master key too short")
	}
	k := sha256.Sum256([]byte("gonghao-v2/secret-box\x00" + masterKey))
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal returns "v1:" + base64(nonce|ciphertext).
func (b *Box) Seal(plain string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.RawStdEncoding.EncodeToString(out), nil
}

func (b *Box) Open(sealed string) (string, error) {
	if !strings.HasPrefix(sealed, prefix) {
		return "", errors.New("crypto: unknown format")
	}
	raw, err := base64.RawStdEncoding.DecodeString(sealed[len(prefix):])
	if err != nil {
		return "", errors.New("crypto: bad encoding")
	}
	n := b.aead.NonceSize()
	if len(raw) < n {
		return "", errors.New("crypto: too short")
	}
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", errors.New("crypto: decrypt failed")
	}
	return string(plain), nil
}

// Mask shows at most the first 3 and last 4 characters, e.g. "sk-****abcd".
// Short secrets are fully masked.
func Mask(s string) string {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return ""
	}
	if n <= 10 {
		return "****"
	}
	r := []rune(s)
	return string(r[:3]) + "****" + string(r[n-4:])
}
