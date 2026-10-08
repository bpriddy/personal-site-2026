package notify

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

// Link is what an email's "open your build" link carries: the front end it
// hands over to whichever browser opens it. Sealed (AES-GCM), so it can't be
// forged or pointed at another build, and the URL says nothing readable.
type Link struct {
	Frontend string    `json:"f"`
	Expires  time.Time `json:"e"`
}

// ErrBadLink: the token is malformed, tampered with or sealed with another key.
var ErrBadLink = errors.New("notify: bad link")

// ErrExpired: the link is genuine but too old.
var ErrExpired = errors.New("notify: link expired")

func linkAEAD(secret []byte) (cipher.AEAD, error) {
	// a key of its own, derived from the server secret
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("benpriddy build-notify link v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal makes a link token.
func Seal(secret []byte, l Link) (string, error) {
	aead, err := linkAEAD(secret)
	if err != nil {
		return "", err
	}
	plain, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, plain, nil)), nil
}

// Open reads a link token. ErrBadLink, or ErrExpired (with the link) if it
// is older than now.
func Open(secret []byte, token string, now time.Time) (Link, error) {
	aead, err := linkAEAD(secret)
	if err != nil {
		return Link{}, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < aead.NonceSize()+aead.Overhead() {
		return Link{}, ErrBadLink
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
	if err != nil {
		return Link{}, ErrBadLink
	}
	var l Link
	if err := json.Unmarshal(plain, &l); err != nil {
		return Link{}, ErrBadLink
	}
	if now.After(l.Expires) {
		return l, ErrExpired
	}
	return l, nil
}
