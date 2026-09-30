// Package fetoken signs and verifies the capability tokens that gate front-end
// files on the user-content domain. A token names one front end (a Ref) and when
// it was issued; the main site mints it, the user-content service verifies it.
// Expiry policy (60s for index.html, 30m for assets) lives in the verifier.
//
// Format: base64url(payload) "." base64url(HMAC-SHA256(key, payload)), where
// payload is compact JSON {"r": ref, "i": issued-unix-seconds}. No padding, so
// the token is safe as a URL path segment.
package fetoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrMalformed = errors.New("fetoken: malformed token")
	ErrSignature = errors.New("fetoken: bad signature")
)

type Claims struct {
	Ref    string
	Issued time.Time
}

type payload struct {
	R string `json:"r"`
	I int64  `json:"i"`
}

var b64 = base64.RawURLEncoding

func Sign(key []byte, c Claims) string {
	p, _ := json.Marshal(payload{R: c.Ref, I: c.Issued.Unix()})
	body := b64.EncodeToString(p)
	return body + "." + b64.EncodeToString(mac(key, body))
}

// Verify checks the signature and decodes the claims. It does not check age;
// callers apply their own expiry per file type.
func Verify(key []byte, tok string) (Claims, error) {
	body, sig, ok := strings.Cut(tok, ".")
	if !ok || body == "" || sig == "" {
		return Claims{}, ErrMalformed
	}
	got, err := b64.DecodeString(sig)
	if err != nil {
		return Claims{}, ErrMalformed
	}
	if !hmac.Equal(got, mac(key, body)) {
		return Claims{}, ErrSignature
	}
	raw, err := b64.DecodeString(body)
	if err != nil {
		return Claims{}, ErrMalformed
	}
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil || p.R == "" || p.I == 0 {
		return Claims{}, ErrMalformed
	}
	return Claims{Ref: p.R, Issued: time.Unix(p.I, 0)}, nil
}

func mac(key []byte, body string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(body))
	return h.Sum(nil)
}
