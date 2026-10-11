package pay

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
)

func parsePrivateKey(raw string) (*rsa.PrivateKey, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, `\n`, "\n"))
	if raw == "" {
		return nil, errors.New("缺少私钥")
	}
	if !strings.Contains(raw, "BEGIN") {
		compact := strings.Map(func(r rune) rune {
			if r == '\n' || r == '\r' || r == ' ' {
				return -1
			}
			return r
		}, raw)
		der, err := base64.StdEncoding.DecodeString(compact)
		if err != nil {
			return nil, errors.New("私钥不是有效的 PEM 或 Base64")
		}
		return parsePrivateDER(der)
	}
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("私钥 PEM 无法解析")
	}
	return parsePrivateDER(block.Bytes)
}

func parsePrivateDER(der []byte) (*rsa.PrivateKey, error) {
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, errors.New("私钥无法解析")
	}
	priv, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("私钥不是 RSA")
	}
	return priv, nil
}

func parsePublicKey(raw string) (*rsa.PublicKey, *x509.Certificate, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, `\n`, "\n"))
	if raw == "" {
		return nil, nil, errors.New("缺少公钥")
	}
	if !strings.Contains(raw, "BEGIN") {
		compact := strings.Map(func(r rune) rune {
			if r == '\n' || r == '\r' || r == ' ' {
				return -1
			}
			return r
		}, raw)
		der, err := base64.StdEncoding.DecodeString(compact)
		if err != nil {
			return nil, nil, errors.New("公钥不是有效的 PEM 或 Base64")
		}
		return parsePublicDER(der)
	}
	var last error
	rest := []byte(raw)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		pub, cert, err := parsePublicDER(block.Bytes)
		if err == nil {
			return pub, cert, nil
		}
		last = err
	}
	if last == nil {
		last = errors.New("公钥 PEM 无法解析")
	}
	return nil, nil, last
}

func parsePublicDER(der []byte) (*rsa.PublicKey, *x509.Certificate, error) {
	if cert, err := x509.ParseCertificate(der); err == nil {
		pub, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok {
			return nil, nil, errors.New("证书公钥不是 RSA")
		}
		return pub, cert, nil
	}
	if pub, err := x509.ParsePKIXPublicKey(der); err == nil {
		key, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, nil, errors.New("公钥不是 RSA")
		}
		return key, nil, nil
	}
	if pub, err := x509.ParsePKCS1PublicKey(der); err == nil {
		return pub, nil, nil
	}
	return nil, nil, errors.New("公钥无法解析")
}
