package connect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// GoogleFonts self-hosts a Google Fonts family: it fetches the family's CSS,
// copies the latin (and latin-ext) .woff2 files into the media store, and
// returns @font-face rules pointing at them. No key; the fonts are OFL or
// Apache licensed. (Front ends can't load fonts.googleapis.com: their CSP
// allows only the user-content origin.)
type GoogleFonts struct {
	Store Store
	CSS   string // CSS API base (tests point it at a fake)
}

const (
	googleFontsCSS    = "https://fonts.googleapis.com/css2"
	googleFontsPerRun = 3
	googleFontsMax    = 1 << 20 // per file
	googleFontsBase   = "assets/fonts/google/"
	// a modern browser's user agent: the CSS API serves woff2 to these
	googleFontsUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"
)

var (
	googleFamily = regexp.MustCompile(`^[A-Za-z0-9 ]{2,60}$`)
	googleAxes   = regexp.MustCompile(`^[a-zA-Z,]{3,40}@[0-9.,;]{1,200}$`)
	fontFaceRe   = regexp.MustCompile(`(?s)/\* ([a-z-]+) \*/\s*(@font-face \{.*?\})`)
	fontURLRe    = regexp.MustCompile(`url\((https://fonts\.gstatic\.com/[^)]+\.woff2)\)`)
)

func (g *GoogleFonts) css() string {
	if g.CSS != "" {
		return g.CSS
	}
	return googleFontsCSS
}

func (g *GoogleFonts) Name() string { return "googlefonts" }

func (g *GoogleFonts) Tools() []Tool {
	return []Tool{
		{Name: "google_fonts_import", Description: "Self-host a Google Fonts family for this front end. Returns @font-face rules (font files under /media/) to paste into your CSS as they are. family: the exact Google Fonts name (\"Space Grotesk\", \"Fraunces\", \"JetBrains Mono\"). axes: the CSS API axis spec, e.g. \"wght@400;700\", \"wght@300..800\" for a variable range, or \"ital,wght@0,400;1,400\". Latin subsets only. At most 3 families per run. Read the typography skill first.",
			Props: map[string]any{
				"family": map[string]any{"type": "string"},
				"axes":   map[string]any{"type": "string"},
			}},
	}
}

func (g *GoogleFonts) Call(ctx context.Context, tool string, input json.RawMessage) (Output, error) {
	if tool != "google_fonts_import" {
		return Output{}, Userf("Unknown tool %q", tool)
	}
	var in struct {
		Family string `json:"family"`
		Axes   string `json:"axes"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Output{}, Userf("Invalid input: %v", err)
	}
	fam, axes := strings.TrimSpace(in.Family), strings.ReplaceAll(strings.TrimSpace(in.Axes), " ", "")
	if !googleFamily.MatchString(fam) {
		return Output{}, Userf("family must be a Google Fonts family name (letters, digits, spaces).")
	}
	if axes != "" && !googleAxes.MatchString(axes) {
		return Output{}, Userf("axes must look like \"wght@400;700\" or \"ital,wght@0,400;1,700\" (or be empty).")
	}
	spec := fam
	if axes != "" {
		spec += ":" + axes
	}
	q := url.Values{"family": {spec}, "display": {"swap"}}
	cssBody, err := get(ctx, g.css()+"?"+q.Encode(), map[string]string{"User-Agent": googleFontsUA}, 512<<10)
	if err != nil {
		var se *StatusError
		if errors.As(err, &se) && se.Code == 400 {
			return Output{}, Userf("Google Fonts doesn't have %q with axes %q. Check the family name and the weights it offers.", fam, axes)
		}
		return Output{}, Userf("Google Fonts failed (%v).", err)
	}
	if err := Spend(ctx, "font families", googleFontsPerRun); err != nil {
		return Output{}, err
	}
	slug := strings.ToLower(strings.ReplaceAll(fam, " ", "-"))
	var rules []string
	files := 0
	for _, m := range fontFaceRe.FindAllStringSubmatch(string(cssBody), -1) {
		subset, rule := m[1], m[2]
		if subset != "latin" && subset != "latin-ext" {
			continue
		}
		u := fontURLRe.FindStringSubmatch(rule)
		if u == nil {
			continue
		}
		sum := sha256.Sum256([]byte(u[1]))
		name := googleFontsBase + slug + "/" + hex.EncodeToString(sum[:8]) + ".woff2"
		exists, err := g.Store.Exists(ctx, name)
		if err != nil {
			return Output{}, Userf("The media store is unavailable (%v).", err)
		}
		if !exists {
			b, err := get(ctx, u[1], nil, googleFontsMax)
			if err != nil {
				return Output{}, Userf("Downloading %s failed (%v).", fam, err)
			}
			if err := g.Store.Put(ctx, name, b, "font/woff2"); err != nil {
				return Output{}, Userf("Saving %s failed (%v).", fam, err)
			}
		}
		files++
		rules = append(rules, "/* "+subset+" */\n"+strings.Replace(rule, u[0], "url(/media/"+name+")", 1))
	}
	if files == 0 {
		return Output{}, Userf("Google Fonts returned no latin woff2 files for %q.", fam)
	}
	return Output{Text: fmt.Sprintf("Self-hosted %s (%d files). Paste these rules into your CSS as they are, then use font-family: %q with a fallback stack:\n\n%s\n\nLicense: SIL Open Font License (or Apache 2.0); no credit needed.",
		fam, files, fam, strings.Join(rules, "\n"))}, nil
}
