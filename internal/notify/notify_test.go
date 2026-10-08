package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLinkRoundTrip(t *testing.T) {
	key := []byte("secret")
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	l := Link{SID: "0123456789abcdef0123456789abcdef", Frontend: "fe/x", Expires: now.Add(time.Hour)}
	tok, err := Seal(key, l)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tok, l.SID) {
		t.Fatal("sid readable in the token")
	}
	got, err := Open(key, tok, now)
	if err != nil || got.SID != l.SID || got.Frontend != l.Frontend {
		t.Fatalf("open: %+v %v", got, err)
	}
	if _, err := Open(key, tok, now.Add(2*time.Hour)); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired: %v", err)
	}
	if _, err := Open([]byte("other"), tok, now); !errors.Is(err, ErrBadLink) {
		t.Fatalf("other key: %v", err)
	}
	b := []byte(tok)
	b[len(b)/2] ^= 1
	if _, err := Open(key, string(b), now); !errors.Is(err, ErrBadLink) {
		t.Fatalf("tampered: %v", err)
	}
	if _, err := Open(key, "!!", now); !errors.Is(err, ErrBadLink) {
		t.Fatalf("garbage: %v", err)
	}
}

func TestAddress(t *testing.T) {
	for in, want := range map[string]string{
		"ben@Example.COM":                   "ben@example.com",
		"  a.b+c@mail.co.uk ":               "a.b+c@mail.co.uk",
		"Ben <ben@example.com>":             "",
		"ben@localhost":                     "",
		"ben":                               "",
		"a@b.com, c@d.com":                  "",
		"a@b.com\r\nBcc: x@y.z":             "",
		"":                                  "",
		strings.Repeat("a", 250) + "@b.com": "",
	} {
		got, err := Address(in)
		if want == "" {
			if err == nil {
				t.Errorf("%q: accepted as %q", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	if Mask("ben@example.com") != "b•••@example.com" {
		t.Errorf("mask: %q", Mask("ben@example.com"))
	}
	if string(AddressHash("A@B.com")) != string(AddressHash("a@b.com")) {
		t.Error("hash is case-sensitive")
	}
}

func TestSendGrid(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	sg := &SendGrid{Key: "k", From: "info@lanterns.build", FromName: "benpriddy.com", Endpoint: srv.URL}
	if err := sg.Send(context.Background(), Message{To: "v@example.com", Subject: "S", Text: "T", HTML: "<p>T</p>"}); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer k" || got["subject"] != "S" {
		t.Fatalf("auth %q body %v", auth, got)
	}
	if from := got["from"].(map[string]any); from["email"] != "info@lanterns.build" {
		t.Fatalf("from %v", from)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"errors":[{"message":"nope"}]}`, http.StatusForbidden)
	}))
	defer bad.Close()
	sg.Endpoint = bad.URL
	if err := sg.Send(context.Background(), Message{To: "v@example.com"}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("error: %v", err)
	}
}
