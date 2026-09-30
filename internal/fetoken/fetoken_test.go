package fetoken

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	key := []byte("k")
	now := time.Unix(1_800_000_000, 0)
	tok := Sign(key, Claims{Ref: "builtin/site", Issued: now})
	if strings.ContainsAny(tok, "/+=") {
		t.Fatalf("token not path-safe: %q", tok)
	}
	c, err := Verify(key, tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.Ref != "builtin/site" || !c.Issued.Equal(now) {
		t.Fatalf("got %+v", c)
	}
}

func TestRejects(t *testing.T) {
	key := []byte("k")
	tok := Sign(key, Claims{Ref: "builtin/site", Issued: time.Now()})
	body, sig, _ := strings.Cut(tok, ".")
	forged := Sign([]byte("other"), Claims{Ref: "builtin/site", Issued: time.Now()})
	swapped := Sign(key, Claims{Ref: "builtin/particle-stream", Issued: time.Now()})
	_, swappedSig, _ := strings.Cut(swapped, ".")

	cases := map[string]error{
		"":                      ErrMalformed,
		"nodot":                 ErrMalformed,
		body + ".":              ErrMalformed,
		"." + sig:               ErrMalformed,
		body + ".!!!":           ErrMalformed,
		forged:                  ErrSignature,
		body + "." + swappedSig: ErrSignature,
	}
	for in, want := range cases {
		if _, err := Verify(key, in); !errors.Is(err, want) {
			t.Errorf("Verify(%q) = %v, want %v", in, err, want)
		}
	}
}
