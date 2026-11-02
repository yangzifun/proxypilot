package api

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"
)

type jwksDoc struct {
	Keys []struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		N   string `json:"n"`
		E   string `json:"e"`
		Alg string `json:"alg"`
		Use string `json:"use"`
	} `json:"keys"`
}

func b64urlDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

func parseRSAPublicKey(nB64, eB64 string) (*rsa.PublicKey, error) {
	nb, err := b64urlDecode(nB64)
	if err != nil {
		return nil, err
	}
	eb, err := b64urlDecode(eB64)
	if err != nil {
		return nil, err
	}
	e := 0
	for _, b := range eb {
		e = e<<8 | int(b)
	}
	if e == 0 {
		return nil, errors.New("invalid exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}, nil
}

func verifyRS256JWT(token string, jwks jwksDoc, issuer, clientID, nonce string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("id_token 结构错误")
	}
	headRaw, err := b64urlDecode(parts[0])
	if err != nil {
		return nil, errors.New("id_token header 解码失败")
	}
	var head struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headRaw, &head); err != nil {
		return nil, errors.New("id_token header 解析失败")
	}
	if strings.ToUpper(head.Alg) != "RS256" {
		return nil, errors.New("不支持的 id_token 签名算法：" + head.Alg)
	}
	var pub *rsa.PublicKey
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" {
			continue
		}
		if head.Kid != "" && k.Kid != head.Kid {
			continue
		}
		p, err := parseRSAPublicKey(k.N, k.E)
		if err != nil {
			continue
		}
		pub = p
		if head.Kid == k.Kid {
			break
		}
	}
	if pub == nil {
		return nil, errors.New("JWKS 中找不到匹配的公钥")
	}
	sig, err := b64urlDecode(parts[2])
	if err != nil {
		return nil, errors.New("id_token 签名解码失败")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		return nil, errors.New("id_token 签名校验失败")
	}
	payloadRaw, err := b64urlDecode(parts[1])
	if err != nil {
		return nil, errors.New("id_token payload 解码失败")
	}
	var claims map[string]any
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		return nil, errors.New("id_token payload 解析失败")
	}
	if iss := jsonStr(claims["iss"]); iss != "" && issuer != "" && strings.TrimRight(iss, "/") != strings.TrimRight(issuer, "/") {
		return nil, errors.New("id_token issuer 不匹配")
	}
	audOK := false
	switch v := claims["aud"].(type) {
	case string:
		audOK = v == clientID
	case []any:
		for _, a := range v {
			if jsonStr(a) == clientID {
				audOK = true
			}
		}
	}
	if !audOK {
		return nil, errors.New("id_token aud 不匹配")
	}
	if exp, ok := claims["exp"].(float64); ok && int64(exp)*1000 < time.Now().UnixMilli()-300000 {
		return nil, errors.New("id_token 已过期")
	}
	if nonce != "" {
		if n := jsonStr(claims["nonce"]); n != nonce {
			return nil, errors.New("id_token nonce 不匹配")
		}
	}
	return claims, nil
}
