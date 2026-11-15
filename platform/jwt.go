package platform

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Minimal HS256 JWT implementation (sign/verify) using only the standard
// library — enough for the single-admin login flow of this platform.

var (
	// ErrJWTMalformed covers structurally invalid or badly signed tokens.
	ErrJWTMalformed = errors.New("platform: malformed jwt")
	// ErrJWTExpired covers tokens whose exp has passed.
	ErrJWTExpired = errors.New("platform: expired jwt")
)

type jwtClaims struct {
	Sub string `json:"sub"`
	IAT int64  `json:"iat"`
	EXP int64  `json:"exp"`
	// TV is the token_version at signing time (契约 B): bumping the stored
	// version invalidates every previously issued token. Tokens issued before
	// the field existed decode to TV=0, which matches the initial version.
	TV int `json:"tv,omitempty"`
}

func base64URLJSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// signJWT issues an HS256 token for sub, expiring at exp, bound to the given
// token_version.
func signJWT(secret []byte, sub string, exp time.Time, tokenVersion int) (string, error) {
	header, err := base64URLJSON(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := base64URLJSON(jwtClaims{Sub: sub, IAT: time.Now().Unix(), EXP: exp.Unix(), TV: tokenVersion})
	if err != nil {
		return "", err
	}
	signingInput := header + "." + claims
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig, nil
}

// verifyJWT checks structure, signature (constant-time) and expiry.
func verifyJWT(secret []byte, token string) (*jwtClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrJWTMalformed
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrJWTMalformed
	}
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, ErrJWTMalformed
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrJWTMalformed
	}
	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrJWTMalformed, err)
	}
	if claims.EXP <= 0 || time.Now().Unix() >= claims.EXP {
		return nil, ErrJWTExpired
	}
	return &claims, nil
}
