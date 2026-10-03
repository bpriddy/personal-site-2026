package connect

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// GLBInfo summarizes a glTF 2.0 binary (.glb).
type GLBInfo struct {
	Meshes, Primitives, Triangles, Textures, Animations int
	Min, Max                                            [3]float64 // POSITION bounds, before node transforms
	Extensions                                          []string
}

// unsupported extensions: site.loadModel (site-host.js) can't decode them
var unsupportedGLB = []string{"KHR_draco_mesh_compression", "EXT_meshopt_compression", "KHR_texture_basisu", "EXT_texture_avif"}

// InspectGLB checks that b is a self-contained glTF 2.0 binary that
// site.loadModel can read, and summarizes it.
func InspectGLB(b []byte) (*GLBInfo, error) {
	if len(b) < 20 || !bytes.Equal(b[:4], []byte("glTF")) {
		return nil, fmt.Errorf("not a .glb file")
	}
	if v := binary.LittleEndian.Uint32(b[4:8]); v != 2 {
		return nil, fmt.Errorf("glTF version %d (need 2)", v)
	}
	clen := binary.LittleEndian.Uint32(b[12:16])
	if binary.LittleEndian.Uint32(b[16:20]) != 0x4E4F534A || 20+int(clen) > len(b) {
		return nil, fmt.Errorf("bad .glb JSON chunk")
	}
	var doc struct {
		ExtensionsUsed     []string `json:"extensionsUsed"`
		ExtensionsRequired []string `json:"extensionsRequired"`
		Meshes             []struct {
			Primitives []struct {
				Attributes map[string]int `json:"attributes"`
				Indices    *int           `json:"indices"`
				Mode       *int           `json:"mode"`
			} `json:"primitives"`
		} `json:"meshes"`
		Accessors []struct {
			Count int       `json:"count"`
			Min   []float64 `json:"min"`
			Max   []float64 `json:"max"`
		} `json:"accessors"`
		Textures   []json.RawMessage `json:"textures"`
		Animations []json.RawMessage `json:"animations"`
		Images     []struct {
			URI string `json:"uri"`
		} `json:"images"`
		Buffers []struct {
			URI string `json:"uri"`
		} `json:"buffers"`
	}
	if err := json.Unmarshal(b[20:20+clen], &doc); err != nil {
		return nil, fmt.Errorf("bad glTF JSON: %v", err)
	}
	for _, e := range append(slices.Clone(doc.ExtensionsRequired), doc.ExtensionsUsed...) {
		if slices.Contains(unsupportedGLB, e) {
			return nil, fmt.Errorf("uses %s, which the site's model loader doesn't decode", e)
		}
	}
	for _, im := range doc.Images {
		if im.URI != "" && !strings.HasPrefix(im.URI, "data:") {
			return nil, fmt.Errorf("references an external image (%s); only self-contained .glb files work", im.URI)
		}
	}
	for i, bf := range doc.Buffers {
		if i > 0 || bf.URI != "" {
			return nil, fmt.Errorf("references an external buffer; only self-contained .glb files work")
		}
	}
	info := &GLBInfo{Meshes: len(doc.Meshes), Textures: len(doc.Textures), Animations: len(doc.Animations), Extensions: doc.ExtensionsUsed}
	info.Min = [3]float64{1e300, 1e300, 1e300}
	info.Max = [3]float64{-1e300, -1e300, -1e300}
	acc := func(i int) (int, []float64, []float64) {
		if i < 0 || i >= len(doc.Accessors) {
			return 0, nil, nil
		}
		a := doc.Accessors[i]
		return a.Count, a.Min, a.Max
	}
	for _, m := range doc.Meshes {
		for _, p := range m.Primitives {
			if p.Mode != nil && *p.Mode != 4 {
				continue // not triangles
			}
			info.Primitives++
			pos, ok := p.Attributes["POSITION"]
			n, mn, mx := acc(pos)
			if !ok {
				continue
			}
			if p.Indices != nil {
				n, _, _ = acc(*p.Indices)
			}
			info.Triangles += n / 3
			if len(mn) == 3 && len(mx) == 3 {
				for k := range 3 {
					info.Min[k] = min(info.Min[k], mn[k])
					info.Max[k] = max(info.Max[k], mx[k])
				}
			}
		}
	}
	if info.Primitives == 0 {
		return nil, fmt.Errorf("has no triangle meshes")
	}
	return info, nil
}

// Summary describes info for the model.
func (i *GLBInfo) Summary() string {
	s := fmt.Sprintf("%d meshes (%d triangle primitives), %d triangles, %d textures", i.Meshes, i.Primitives, i.Triangles, i.Textures)
	if i.Animations > 0 {
		s += fmt.Sprintf(", %d animations (not played by site.loadModel: you get the rest pose)", i.Animations)
	}
	if i.Min[0] <= i.Max[0] {
		s += fmt.Sprintf(". Raw position bounds (before node transforms): min [%.3g %.3g %.3g], max [%.3g %.3g %.3g]; site.loadModel returns the true bounds",
			i.Min[0], i.Min[1], i.Min[2], i.Max[0], i.Max[1], i.Max[2])
	}
	return s
}
