package notify

import (
	"crypto/sha256"
	"errors"
	"net/mail"
	"strings"
)

// ErrAddress: not a usable email address.
var ErrAddress = errors.New("notify: not an email address")

// Address validates and normalizes an email address typed by a visitor:
// a bare addr-spec (no display name), at most 254 characters, with a dot in
// its domain. The domain is lower-cased.
func Address(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 254 || strings.ContainsAny(s, "<>\"\r\n,;") {
		return "", ErrAddress
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Name != "" || a.Address != s {
		return "", ErrAddress
	}
	local, domain, ok := strings.Cut(s, "@")
	if !ok || local == "" || !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", ErrAddress
	}
	return local + "@" + strings.ToLower(domain), nil
}

// AddressHash identifies an address for the daily cap without keeping it.
func AddressHash(addr string) []byte {
	h := sha256.Sum256([]byte(strings.ToLower(addr)))
	return h[:]
}

// Mask shows an address without giving it away: b•••@gmail.com.
func Mask(addr string) string {
	local, domain, ok := strings.Cut(addr, "@")
	if !ok || local == "" {
		return "your email"
	}
	r := []rune(local)
	return string(r[0]) + "•••@" + domain
}
