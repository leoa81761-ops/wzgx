package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strings"
)

// Password hashes use PBKDF2-SHA256, 100,000 iterations, 16-byte salt, 32-byte key, stored as
// base64(salt) + "$" + base64(key). This matches the v1 format so migrated users keep their passwords.
const (
	pbkdfIter = 100_000
	pbkdfLen  = 32
)

func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, pbkdfIter, pbkdfLen)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(salt) + "$" + base64.StdEncoding.EncodeToString(key), nil
}

func VerifyPassword(pw, stored string) bool {
	s, k, ok := strings.Cut(stored, "$")
	if !ok {
		return false
	}
	salt, err1 := base64.StdEncoding.DecodeString(s)
	want, err2 := base64.StdEncoding.DecodeString(k)
	if err1 != nil || err2 != nil || len(salt) == 0 || len(want) != pbkdfLen {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, pbkdfIter, pbkdfLen)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}
