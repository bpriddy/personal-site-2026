package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// PolyHaven offers Poly Haven's textures (CC0, no key): search, then import a
// texture's maps (colour, normal, roughness, ambient occlusion) as JPEGs.
type PolyHaven struct {
	Store Store
	API   string // API base (tests point it at a fake)
}

const (
	polyHavenAPI     = "https://api.polyhaven.com"
	polyHavenPerRun  = 4
	polyHavenMaxMap  = 6 << 20
	polyHavenResults = 8
	polyHavenBase    = "assets/textures/polyhaven/"
)

var polyHavenID = regexp.MustCompile(`^[a-z0-9_]{1,80}$`)

// the maps imported, by Poly Haven's file key → our file name
var polyHavenMaps = []struct{ key, file, what string }{
	{"Diffuse", "color.jpg", "base colour (sRGB)"},
	{"nor_gl", "normal.jpg", "normal map, OpenGL convention (+Y up; linear)"},
	{"Rough", "rough.jpg", "roughness (linear, white = rough)"},
	{"AO", "ao.jpg", "ambient occlusion (linear)"},
}

func (p *PolyHaven) api() string {
	if p.API != "" {
		return p.API
	}
	return polyHavenAPI
}

func (p *PolyHaven) Name() string { return "polyhaven" }

func (p *PolyHaven) Tools() []Tool {
	return []Tool{
		{Name: "polyhaven_search_textures", Description: "Search Poly Haven's free (CC0) surface textures: wood, concrete, fabric, metal, rock, plaster... Returns up to 8 ids with names and categories, and shows you their thumbnails. Read the textures skill first.",
			Props: map[string]any{"query": map[string]any{"type": "string", "description": "A material, e.g. \"brushed metal\" or \"old plaster\""}}},
		{Name: "polyhaven_import_texture", Description: "Import a Poly Haven texture's maps (colour, normal, roughness, AO JPEGs) into the site's media. Returns their /media/ paths. resolution: \"1k\" (default choice) or \"2k\" (only for a texture seen big and close). At most 4 per run.",
			Props: map[string]any{
				"id":         map[string]any{"type": "string", "description": "The texture id from polyhaven_search_textures"},
				"resolution": map[string]any{"type": "string", "enum": []string{"1k", "2k"}},
			}},
	}
}

func (p *PolyHaven) Call(ctx context.Context, tool string, input json.RawMessage) (Output, error) {
	var in struct {
		Query      string `json:"query"`
		ID         string `json:"id"`
		Resolution string `json:"resolution"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Output{}, Userf("Invalid input: %v", err)
	}
	switch tool {
	case "polyhaven_search_textures":
		return p.search(ctx, strings.ToLower(strings.TrimSpace(in.Query)))
	case "polyhaven_import_texture":
		return p.importTexture(ctx, strings.TrimSpace(in.ID), in.Resolution)
	}
	return Output{}, Userf("Unknown tool %q", tool)
}

type phAsset struct {
	Name       string   `json:"name"`
	Tags       []string `json:"tags"`
	Categories []string `json:"categories"`
}

func (p *PolyHaven) search(ctx context.Context, q string) (Output, error) {
	if q == "" || len(q) > 100 {
		return Output{}, Userf("Give a short search query.")
	}
	var all map[string]phAsset
	if err := getJSON(ctx, p.api()+"/assets?t=textures", nil, &all); err != nil {
		return Output{}, Userf("Poly Haven search failed (%v). Try again, or do without.", err)
	}
	words := strings.Fields(q)
	type hit struct {
		id    string
		a     phAsset
		score int
	}
	var hits []hit
	for id, a := range all {
		hay := strings.ToLower(a.Name + " " + strings.Join(a.Tags, " ") + " " + strings.Join(a.Categories, " ") + " " + strings.ReplaceAll(id, "_", " "))
		score := 0
		for _, w := range words {
			if strings.Contains(hay, w) {
				score++
			}
			if strings.Contains(strings.ToLower(a.Name), w) {
				score++
			}
		}
		if score > 0 {
			hits = append(hits, hit{id, a, score})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].id < hits[j].id
	})
	if len(hits) == 0 {
		return Output{Text: fmt.Sprintf("No Poly Haven textures for %q. Try a simpler material word (\"wood\", \"metal\", \"concrete\").", q)}, nil
	}
	var sb strings.Builder
	var out Output
	for i, h := range hits {
		if i == polyHavenResults {
			break
		}
		fmt.Fprintf(&sb, "%d. id %s — %s — %s (thumbnail %d below)\n", i+1, h.id, h.a.Name, strings.Join(h.a.Categories, ", "), i+1)
		out.Images = append(out.Images, "https://cdn.polyhaven.com/asset_img/thumbs/"+h.id+".png?width=256&height=256")
	}
	out.Text = fmt.Sprintf("Poly Haven textures for %q (CC0):\n\n%s\nThen polyhaven_import_texture the id.", q, sb.String())
	return out, nil
}

func (p *PolyHaven) importTexture(ctx context.Context, id, res string) (Output, error) {
	if !polyHavenID.MatchString(id) {
		return Output{}, Userf("id must be a Poly Haven texture id from polyhaven_search_textures.")
	}
	if !slices.Contains([]string{"1k", "2k"}, res) {
		res = "1k"
	}
	var files map[string]map[string]map[string]struct {
		URL  string `json:"url"`
		Size int64  `json:"size"`
	}
	if err := getJSON(ctx, p.api()+"/files/"+id, nil, &files); err != nil {
		return Output{}, Userf("No Poly Haven texture %q (%v).", id, err)
	}
	base := polyHavenBase + id + "/" + res + "/"
	var sb strings.Builder
	spent := false
	for _, m := range polyHavenMaps {
		f, ok := files[m.key][res]["jpg"]
		if !ok || !strings.HasPrefix(f.URL, "https://") {
			continue
		}
		name := base + m.file
		exists, err := p.Store.Exists(ctx, name)
		if err != nil {
			return Output{}, Userf("The media store is unavailable (%v).", err)
		}
		if !exists {
			if !spent {
				if err := Spend(ctx, "texture imports", polyHavenPerRun); err != nil {
					return Output{}, err
				}
				spent = true
			}
			b, err := get(ctx, f.URL, nil, polyHavenMaxMap)
			if err != nil {
				return Output{}, Userf("Downloading %s's %s failed (%v).", id, m.key, err)
			}
			if err := p.Store.Put(ctx, name, b, "image/jpeg"); err != nil {
				return Output{}, Userf("Saving %s failed (%v).", name, err)
			}
		}
		fmt.Fprintf(&sb, "- /media/%s — %s\n", name, m.what)
	}
	if sb.Len() == 0 {
		return Output{}, Userf("%s has no %s JPEG maps. Pick another texture.", id, res)
	}
	return Output{Text: fmt.Sprintf("Imported Poly Haven texture %s at %s:\n%s\nCC0 (no credit required; \"Texture: Poly Haven\" in a credits line is a kind gesture). Load images with crossOrigin = \"anonymous\" to draw them into WebGPU.", id, res, sb.String())}, nil
}
