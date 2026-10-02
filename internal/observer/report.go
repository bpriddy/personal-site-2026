package observer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bpriddy/personal-site-2026/internal/store"
)

// A Report is one POST /api/observe body (docs/frontend-protocol.md,
// "Observer ingestion"). It comes from the public internet: every field is
// untrusted data, validated here and never treated as instructions.
type Report struct {
	Kind       string `json:"kind"`
	Frontend   string `json:"frontend"`
	Serve      string `json:"serve"`
	Route      string `json:"route"`
	Collection string `json:"collection"`
	Item       string `json:"item"`
	Field      string `json:"field"`
	Expect     string `json:"expect"`
	Got        string `json:"got"`
	Message    string `json:"message"`
	Stack      string `json:"stack"`
}

// Limits on report fields. Message and stack are truncated; everything else
// must match its pattern or the report is rejected.
const (
	MaxBody       = 8 << 10 // bytes
	maxMessage    = 500     // runes
	maxStack      = 2000    // runes
	maxFieldName  = 64
	maxSignatureM = 200 // runes of normalized message in a signature
)

var (
	frontendIDPattern = regexp.MustCompile(`^(builtin|fe)/[a-z0-9][a-z0-9-]{0,62}$`)
	servePattern      = regexp.MustCompile(`^(builtin/[a-z0-9][a-z0-9-]{0,62}|rev/[a-z0-9]{8,40})$`)
	routePattern      = regexp.MustCompile(`^[A-Za-z0-9/_.~-]{0,200}$`)
	itemPattern       = regexp.MustCompile(`^([a-z0-9][a-z0-9-]{0,127})?$`) // "" is the home page
	fieldPattern      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	gotPattern        = regexp.MustCompile(`^[a-z]{1,16}$`)
)

// Collections front ends may report on, and the expectations site.field accepts.
var (
	collections = map[string]bool{"pages": true, "experiments": true, "projects": true}
	expects     = map[string]bool{"text": true, "list": true, "number": true, "bool": true}
	// fields a front end can't ask the observer to generate
	reservedFields = map[string]bool{"slug": true, "published": true, "order": true, "contractVersion": true}
)

// ParseReport decodes and validates a report body. Unknown JSON fields are
// ignored (forward compatibility); a field of the wrong JSON type is an error.
func ParseReport(body []byte) (Report, error) {
	var r Report
	if err := json.Unmarshal(body, &r); err != nil {
		return r, fmt.Errorf("observe: bad JSON: %w", err)
	}
	return r, r.normalize()
}

func (r *Report) normalize() error {
	switch r.Kind {
	case store.KindContentGap, store.KindTypeBreak, store.KindFrontendError:
	default:
		return errors.New("observe: bad kind")
	}
	if !frontendIDPattern.MatchString(r.Frontend) {
		return errors.New("observe: bad frontend")
	}
	if r.Serve != "" && !servePattern.MatchString(r.Serve) {
		return errors.New("observe: bad serve")
	}
	r.Route = strings.TrimPrefix(r.Route, "/")
	if !routePattern.MatchString(r.Route) {
		return errors.New("observe: bad route")
	}
	r.Message = clean(r.Message, maxMessage)
	r.Stack = clean(r.Stack, maxStack)

	if r.Kind == store.KindFrontendError {
		r.Collection, r.Item, r.Field, r.Expect = "", "", "", ""
		if r.Got != "" && !gotPattern.MatchString(r.Got) {
			return errors.New("observe: bad got")
		}
		return nil
	}
	if !collections[r.Collection] {
		return errors.New("observe: bad collection")
	}
	if !itemPattern.MatchString(r.Item) || (r.Item == "" && r.Collection != "pages") {
		return errors.New("observe: bad item")
	}
	if !fieldPattern.MatchString(r.Field) || reservedFields[r.Field] {
		return errors.New("observe: bad field")
	}
	if !expects[r.Expect] {
		return errors.New("observe: bad expect")
	}
	if r.Got == "" {
		r.Got = "missing"
	}
	if !gotPattern.MatchString(r.Got) {
		return errors.New("observe: bad got")
	}
	// the protocol's rule, applied server-side so a client can't mislabel it
	if r.Got == "missing" || r.Got == "empty" {
		r.Kind = store.KindContentGap
	} else {
		r.Kind = store.KindTypeBreak
	}
	r.Message, r.Stack = "", ""
	return nil
}

// clean drops control characters (except newlines and tabs) and invalid UTF-8,
// and truncates to max runes.
func clean(s string, max int) string {
	s = strings.ToValidUTF8(s, "")
	var b strings.Builder
	n := 0
	for _, c := range s {
		if n == max {
			break
		}
		if unicode.IsControl(c) && c != '\n' && c != '\t' {
			continue
		}
		b.WriteRune(c)
		n++
	}
	return strings.TrimSpace(b.String())
}

var (
	numberRun = regexp.MustCompile(`[0-9]+`)
	hexRun    = regexp.MustCompile(`\b[0-9a-f]{8,}\b`)
	urlRun    = regexp.MustCompile(`\b[a-z]+://\S+`)
	spaceRun  = regexp.MustCompile(`\s+`)
)

// normalizeMessage folds variable parts of an error message (numbers, ids,
// URLs, line/column positions) so repeats of one error share a signature.
func normalizeMessage(m string) string {
	m = strings.ToLower(m)
	m = urlRun.ReplaceAllString(m, "<url>")
	m = hexRun.ReplaceAllString(m, "<id>")
	m = numberRun.ReplaceAllString(m, "#")
	m = strings.TrimSpace(spaceRun.ReplaceAllString(m, " "))
	if utf8.RuneCountInString(m) > maxSignatureM {
		m = string([]rune(m)[:maxSignatureM])
	}
	return m
}

// Signature is the deduplication key: kind, front end, collection and item,
// plus the field (gaps, type breaks) or the normalized message (errors).
func (r Report) Signature() string {
	what := r.Field
	if r.Kind == store.KindFrontendError {
		what = normalizeMessage(r.Message)
	}
	return signature(r.Kind, r.Frontend, r.Collection, r.Item, what)
}

func signature(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// sample is the report as stored in a detection's sample column.
func (r Report) sample() json.RawMessage {
	b, _ := json.Marshal(r)
	return b
}
