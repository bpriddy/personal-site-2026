package connect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Sketchfab searches Sketchfab's downloadable models and imports one as a
// .glb into the media store. Search is public; downloading needs an API
// token (SKETCHFAB_API_TOKEN: Sketchfab → Settings → Password & API). Only
// licenses that allow redistribution and commercial use are offered: CC0,
// CC BY and CC BY-SA; the credit line each requires comes back with the
// import, and the builder makes sure the front end shows it.
type Sketchfab struct {
	Token string
	Store Store
	// API is the API base (tests point it at a fake).
	API string
}

const (
	sketchfabAPI       = "https://api.sketchfab.com/v3"
	SketchfabMaxBytes  = 15 << 20 // per model
	sketchfabMaxFaces  = 300_000
	sketchfabPerRun    = 4
	sketchfabResults   = 8
	sketchfabThumbs    = 6
	sketchfabAssetBase = "assets/models/sketchfab/"
)

// allowed licenses, by label (search results carry only the label) and slug
var sketchfabLicenses = map[string]struct{ slug, name, url string }{
	"CC0 Public Domain":         {"cc0", "CC0 1.0", "https://creativecommons.org/publicdomain/zero/1.0/"},
	"CC Attribution":            {"by", "CC BY 4.0", "https://creativecommons.org/licenses/by/4.0/"},
	"CC Attribution-ShareAlike": {"by-sa", "CC BY-SA 4.0", "https://creativecommons.org/licenses/by-sa/4.0/"},
}

var sketchfabUID = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (s *Sketchfab) api() string {
	if s.API != "" {
		return s.API
	}
	return sketchfabAPI
}

func (s *Sketchfab) Name() string { return "sketchfab" }

func (s *Sketchfab) Tools() []Tool {
	return []Tool{
		{Name: "sketchfab_search", Description: "Search Sketchfab for downloadable 3D models you may use (CC0, CC BY, CC BY-SA; small enough for the web). Returns up to 8 models with their uid, title, author, license, size and triangle count, and shows you thumbnails of the first few. Search in English with plain nouns (\"x-wing\", \"low poly tree\", \"vintage camera\"). Read the 3d-models skill first.",
			Props: map[string]any{"query": map[string]any{"type": "string", "description": "What to search for"}}},
		{Name: "sketchfab_import", Description: "Import a Sketchfab model (by uid from sketchfab_search) into the site's media as a .glb. Returns its /media/ path (load it with site.loadModel), what's in it, and the credit line its license requires you to show. At most 4 imports per run.",
			Props: map[string]any{"uid": map[string]any{"type": "string", "description": "The model's uid (32 hex characters)"}}},
	}
}

type sfModel struct {
	UID            string `json:"uid"`
	Name           string `json:"name"`
	ViewerURL      string `json:"viewerUrl"`
	IsDownloadable bool   `json:"isDownloadable"`
	FaceCount      int    `json:"faceCount"`
	AnimationCount int    `json:"animationCount"`
	License        struct {
		Label string `json:"label"`
		Slug  string `json:"slug"`
	} `json:"license"`
	User struct {
		Username    string `json:"username"`
		DisplayName string `json:"displayName"`
		ProfileURL  string `json:"profileUrl"`
	} `json:"user"`
	Archives struct {
		GLB *struct {
			Size         int64 `json:"size"`
			TextureCount int   `json:"textureCount"`
			FaceCount    int   `json:"faceCount"`
		} `json:"glb"`
	} `json:"archives"`
	Thumbnails struct {
		Images []struct {
			URL   string `json:"url"`
			Width int    `json:"width"`
		} `json:"images"`
	} `json:"thumbnails"`
}

func (m *sfModel) thumb() string {
	best, bw := "", 0
	for _, im := range m.Thumbnails.Images {
		// the smallest that's at least 256 wide (else the largest)
		if im.Width >= 256 && (bw < 256 || im.Width < bw) || bw < 256 && im.Width > bw {
			best, bw = im.URL, im.Width
		}
	}
	if !strings.HasPrefix(best, "https://") {
		return ""
	}
	return best
}

func (m *sfModel) author() string {
	if m.User.DisplayName != "" {
		return m.User.DisplayName
	}
	return m.User.Username
}

func (s *Sketchfab) Call(ctx context.Context, tool string, input json.RawMessage) (Output, error) {
	var in struct {
		Query string `json:"query"`
		UID   string `json:"uid"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Output{}, Userf("Invalid input: %v", err)
	}
	switch tool {
	case "sketchfab_search":
		return s.search(ctx, strings.TrimSpace(in.Query))
	case "sketchfab_import":
		return s.importModel(ctx, strings.TrimSpace(in.UID))
	}
	return Output{}, Userf("Unknown tool %q", tool)
}

func (s *Sketchfab) search(ctx context.Context, q string) (Output, error) {
	if q == "" || len(q) > 200 {
		return Output{}, Userf("Give a short search query.")
	}
	v := url.Values{"type": {"models"}, "q": {q}, "downloadable": {"true"}, "archives_flavours": {"false"},
		"count": {"24"}, "max_face_count": {fmt.Sprint(sketchfabMaxFaces)}}
	var res struct {
		Results []sfModel `json:"results"`
	}
	if err := getJSON(ctx, s.api()+"/search?"+v.Encode(), nil, &res); err != nil {
		return Output{}, Userf("Sketchfab search failed (%v). Try again, or do without a model.", err)
	}
	var sb strings.Builder
	var out Output
	n := 0
	for _, m := range res.Results {
		lic, ok := sketchfabLicenses[m.License.Label]
		if !ok || !m.IsDownloadable || !sketchfabUID.MatchString(m.UID) || m.Archives.GLB == nil ||
			m.Archives.GLB.Size <= 0 || m.Archives.GLB.Size > SketchfabMaxBytes {
			continue
		}
		n++
		fmt.Fprintf(&sb, "%d. uid %s — %q by %s — %s — %s, %d triangles, %d textures", n, m.UID, m.Name, m.author(), lic.name,
			MB(m.Archives.GLB.Size), m.Archives.GLB.FaceCount, m.Archives.GLB.TextureCount)
		if m.AnimationCount > 0 {
			sb.WriteString(" (animated: only the rest pose is used)")
		}
		sb.WriteString("\n")
		if t := m.thumb(); t != "" && len(out.Images) < sketchfabThumbs {
			out.Images = append(out.Images, t)
			fmt.Fprintf(&sb, "   (thumbnail %d below)\n", len(out.Images))
		}
		if n == sketchfabResults {
			break
		}
	}
	if n == 0 {
		return Output{Text: fmt.Sprintf("No usable models for %q (downloadable, CC0/CC BY/CC BY-SA, under %s). Try other words, a simpler object, or do without.", q, MB(SketchfabMaxBytes))}, nil
	}
	out.Text = fmt.Sprintf("Models for %q (licensed for this use; the thumbnails follow in order):\n\n%s\nPick by the look in the thumbnails, then sketchfab_import the uid. Prefer fewer triangles and textures for a fast page.", q, sb.String())
	return out, nil
}

func (s *Sketchfab) importModel(ctx context.Context, uid string) (Output, error) {
	if !sketchfabUID.MatchString(uid) {
		return Output{}, Userf("uid must be the 32-character id from sketchfab_search.")
	}
	var m sfModel
	if err := getJSON(ctx, s.api()+"/models/"+uid, nil, &m); err != nil {
		var se *StatusError
		if errors.As(err, &se) && se.Code == 404 {
			return Output{}, Userf("No Sketchfab model %s.", uid)
		}
		return Output{}, Userf("Couldn't read model %s from Sketchfab (%v).", uid, err)
	}
	lic, ok := sketchfabLicenses[m.License.Label]
	if !ok || (m.License.Slug != "" && m.License.Slug != lic.slug) {
		return Output{}, Userf("%q is licensed %q, which this site can't use. Pick a CC0, CC BY or CC BY-SA model.", m.Name, m.License.Label)
	}
	if !m.IsDownloadable {
		return Output{}, Userf("%q isn't downloadable. Pick another.", m.Name)
	}
	credit := &Credit{
		Line: fmt.Sprintf("“%s” by %s (%s), %s (%s)", m.Name, m.author(), strings.TrimSpace(m.ViewerURL), lic.name, lic.url),
		Must: m.author(),
	}
	if lic.slug == "cc0" {
		credit.Line = fmt.Sprintf("“%s” by %s (%s), %s", m.Name, m.author(), strings.TrimSpace(m.ViewerURL), lic.name)
		credit.Must = "" // credit is welcome, not required
	}
	name := sketchfabAssetBase + uid + ".glb"
	credit.Asset = "/media/" + name
	exists, err := s.Store.Exists(ctx, name)
	if err != nil {
		return Output{}, Userf("The media store is unavailable (%v). Do without a model for now.", err)
	}
	var body []byte
	if !exists {
		if err := Spend(ctx, "model imports", sketchfabPerRun); err != nil {
			return Output{}, err
		}
		if s.Token == "" {
			return Output{}, Userf("Importing from Sketchfab isn't set up on this site (no API token). Do without a model.")
		}
		var dl struct {
			GLB *struct {
				URL  string `json:"url"`
				Size int64  `json:"size"`
			} `json:"glb"`
		}
		if err := getJSON(ctx, s.api()+"/models/"+uid+"/download", map[string]string{"Authorization": "Token " + s.Token}, &dl); err != nil {
			return Output{}, Userf("Sketchfab wouldn't give a download for %q (%v). Pick another model.", m.Name, err)
		}
		if dl.GLB == nil || !strings.HasPrefix(dl.GLB.URL, "https://") {
			return Output{}, Userf("%q has no .glb download. Pick another model.", m.Name)
		}
		if dl.GLB.Size > SketchfabMaxBytes {
			return Output{}, Userf("%q is %s; at most %s. Pick a lighter model.", m.Name, MB(dl.GLB.Size), MB(SketchfabMaxBytes))
		}
		body, err = get(ctx, dl.GLB.URL, nil, SketchfabMaxBytes)
		if errors.Is(err, ErrTooBig) {
			return Output{}, Userf("%q is over %s. Pick a lighter model.", m.Name, MB(SketchfabMaxBytes))
		} else if err != nil {
			return Output{}, Userf("Downloading %q failed (%v). Try again or pick another.", m.Name, err)
		}
	} else {
		f, err := openAll(ctx, s.Store, name)
		if err != nil {
			return Output{}, Userf("The media store is unavailable (%v). Do without a model for now.", err)
		}
		body = f
	}
	info, err := InspectGLB(body)
	if err != nil {
		return Output{}, Userf("%q can't be used: %v. Pick another model.", m.Name, err)
	}
	if !exists {
		if err := s.Store.Put(ctx, name, body, "model/gltf-binary"); err != nil {
			return Output{}, Userf("Saving %q failed (%v). Try again.", m.Name, err)
		}
	}
	text := fmt.Sprintf("Imported %q to %s (%s).\nLoad it with: const model = await site.loadModel(%q);\nContents: %s.\n",
		m.Name, credit.Asset, MB(int64(len(body))), credit.Asset, info.Summary())
	if credit.Must != "" {
		text += "Credit, required by its license: show this line visibly (small is fine) on every route where the model appears, as text in your files:\n" + credit.Line
	} else {
		text += "It's CC0: no credit required, but a small credit is a kind gesture:\n" + credit.Line
	}
	return Output{Text: text, Credit: credit}, nil
}
