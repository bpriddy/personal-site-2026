package connect

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/media"
)

// memStore is an in-memory Store.
type memStore struct {
	mu    sync.Mutex
	files map[string][]byte
	puts  int
}

func newMemStore() *memStore { return &memStore{files: map[string][]byte{}} }

func (m *memStore) Open(_ context.Context, name string) (*media.File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return &media.File{Body: bytes.NewReader(b), ModTime: time.Now()}, nil
}

func (m *memStore) Put(_ context.Context, name string, body []byte, _ string) error {
	if !media.ServableName(name) {
		return fs.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[name] = body
	m.puts++
	return nil
}

func (m *memStore) Exists(_ context.Context, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.files[name]
	return ok, nil
}

// testGLB is a minimal valid .glb: one triangle (plus extra JSON fields).
func testGLB(t *testing.T, extra map[string]any) []byte {
	t.Helper()
	bin := new(bytes.Buffer)
	for _, f := range []float32{0, 0, 0, 1, 0, 0, 0, 1, 0} {
		binary.Write(bin, binary.LittleEndian, f)
	}
	doc := map[string]any{
		"asset":       map[string]any{"version": "2.0"},
		"buffers":     []any{map[string]any{"byteLength": bin.Len()}},
		"bufferViews": []any{map[string]any{"buffer": 0, "byteOffset": 0, "byteLength": bin.Len()}},
		"accessors":   []any{map[string]any{"bufferView": 0, "componentType": 5126, "count": 3, "type": "VEC3", "min": []float64{0, 0, 0}, "max": []float64{1, 1, 0}}},
		"meshes":      []any{map[string]any{"primitives": []any{map[string]any{"attributes": map[string]any{"POSITION": 0}}}}},
		"nodes":       []any{map[string]any{"mesh": 0}},
		"scenes":      []any{map[string]any{"nodes": []int{0}}},
		"scene":       0,
	}
	for k, v := range extra {
		doc[k] = v
	}
	js, _ := json.Marshal(doc)
	for len(js)%4 != 0 {
		js = append(js, ' ')
	}
	out := new(bytes.Buffer)
	out.WriteString("glTF")
	binary.Write(out, binary.LittleEndian, uint32(2))
	binary.Write(out, binary.LittleEndian, uint32(12+8+len(js)+8+bin.Len()))
	binary.Write(out, binary.LittleEndian, uint32(len(js)))
	binary.Write(out, binary.LittleEndian, uint32(0x4E4F534A))
	out.Write(js)
	binary.Write(out, binary.LittleEndian, uint32(bin.Len()))
	binary.Write(out, binary.LittleEndian, uint32(0x004E4942))
	out.Write(bin.Bytes())
	return out.Bytes()
}

// routeAll sends every request to srv (keeping the path and query), and
// records the original host in X-Test-Host.
func routeAll(t *testing.T, srv *httptest.Server) {
	t.Helper()
	old := Client
	Client = &http.Client{Transport: rt(func(r *http.Request) (*http.Response, error) {
		r2 := r.Clone(r.Context())
		r2.Header.Set("X-Test-Host", r.URL.Host)
		r2.URL.Scheme, r2.URL.Host = "http", strings.TrimPrefix(srv.URL, "http://")
		return http.DefaultTransport.RoundTrip(r2)
	})}
	t.Cleanup(func() { Client = old })
}

type rt func(*http.Request) (*http.Response, error)

func (f rt) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestInspectGLB(t *testing.T) {
	info, err := InspectGLB(testGLB(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if info.Triangles != 1 || info.Meshes != 1 || info.Max[0] != 1 {
		t.Fatalf("info = %+v", info)
	}
	if _, err := InspectGLB(testGLB(t, map[string]any{"extensionsUsed": []string{"KHR_draco_mesh_compression"}})); err == nil {
		t.Error("draco must be refused (site.loadModel can't decode it)")
	}
	if _, err := InspectGLB(testGLB(t, map[string]any{"images": []any{map[string]any{"uri": "tex.png"}}})); err == nil {
		t.Error("external images must be refused")
	}
	if _, err := InspectGLB([]byte("not a glb at all, no")); err == nil {
		t.Error("garbage accepted")
	}
}

const uidOK = "0123456789abcdef0123456789abcdef"
const uidNC = "fedcba9876543210fedcba9876543210"

func sketchfabFake(t *testing.T, glb []byte, downloads *int) *httptest.Server {
	model := func(uid, label string) map[string]any {
		return map[string]any{"uid": uid, "name": "X-Wing " + label, "viewerUrl": "https://sketchfab.com/3d-models/x-" + uid,
			"isDownloadable": true, "license": map[string]any{"label": label},
			"user":     map[string]any{"username": "pilot", "displayName": "Red Five"},
			"archives": map[string]any{"glb": map[string]any{"size": len(glb), "faceCount": 1, "textureCount": 0}},
			"thumbnails": map[string]any{"images": []any{map[string]any{"url": "https://media.sketchfab.com/t/" + uid + "-200.jpg", "width": 200},
				map[string]any{"url": "https://media.sketchfab.com/t/" + uid + "-720.jpg", "width": 720}}}}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v3/search":
			json.NewEncoder(w).Encode(map[string]any{"results": []any{model(uidOK, "CC Attribution"), model(uidNC, "CC Attribution-NonCommercial")}})
		case r.URL.Path == "/v3/models/"+uidOK:
			json.NewEncoder(w).Encode(model(uidOK, "CC Attribution"))
		case r.URL.Path == "/v3/models/"+uidNC:
			json.NewEncoder(w).Encode(model(uidNC, "CC Attribution-NonCommercial"))
		case r.URL.Path == "/v3/models/"+uidOK+"/download":
			if r.Header.Get("Authorization") != "Token tok" {
				w.WriteHeader(401)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"glb": map[string]any{"url": "https://dl.example/x.glb", "size": len(glb)}})
		case r.URL.Path == "/x.glb":
			*downloads++
			w.Write(glb)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestSketchfabSearchAndImport(t *testing.T) {
	glb := testGLB(t, nil)
	downloads := 0
	srv := sketchfabFake(t, glb, &downloads)
	defer srv.Close()
	routeAll(t, srv)
	st := newMemStore()
	sf := &Sketchfab{Token: "tok", Store: st, API: "https://api.sketchfab.com/v3"}
	ctx := WithBudget(context.Background())

	out, err := sf.Call(ctx, "sketchfab_search", json.RawMessage(`{"query":"x-wing"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, uidOK) || strings.Contains(out.Text, uidNC) {
		t.Fatalf("search should offer only usable licenses:\n%s", out.Text)
	}
	if len(out.Images) != 1 || !strings.HasSuffix(out.Images[0], "-720.jpg") {
		t.Fatalf("thumbnails = %v (want the 720px one)", out.Images)
	}

	out, err = sf.Call(ctx, "sketchfab_import", json.RawMessage(`{"uid":"`+uidOK+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	name := "assets/models/sketchfab/" + uidOK + ".glb"
	if !bytes.Equal(st.files[name], glb) || out.Credit == nil || out.Credit.Must != "Red Five" ||
		!strings.Contains(out.Credit.Line, "CC BY 4.0") || out.Credit.Asset != "/media/"+name {
		t.Fatalf("import: store %v, credit %+v", len(st.files[name]), out.Credit)
	}
	if !strings.Contains(out.Text, `site.loadModel("/media/`+name+`")`) {
		t.Errorf("import text should say how to load it:\n%s", out.Text)
	}
	// a second import reuses the stored file
	if _, err := sf.Call(ctx, "sketchfab_import", json.RawMessage(`{"uid":"`+uidOK+`"}`)); err != nil || downloads != 1 {
		t.Fatalf("re-import: err %v, downloads %d", err, downloads)
	}
	// a license this site can't use
	if _, err := sf.Call(ctx, "sketchfab_import", json.RawMessage(`{"uid":"`+uidNC+`"}`)); err == nil || !strings.Contains(err.Error(), "can't use") {
		t.Fatalf("NC license: %v", err)
	}
	// without a token, search works but importing a new model can't
	sf2 := &Sketchfab{Store: newMemStore(), API: "https://api.sketchfab.com/v3"}
	if _, err := sf2.Call(ctx, "sketchfab_import", json.RawMessage(`{"uid":"`+uidOK+`"}`)); err == nil || !strings.Contains(err.Error(), "isn't set up") {
		t.Fatalf("no token: %v", err)
	}
}

func TestBudgetLimitsImportsPerRun(t *testing.T) {
	ctx := WithBudget(context.Background())
	for i := range 4 {
		if err := Spend(ctx, "model imports", 4); err != nil {
			t.Fatalf("spend %d: %v", i, err)
		}
	}
	if err := Spend(ctx, "model imports", 4); err == nil {
		t.Fatal("fifth import allowed")
	}
	if err := Spend(context.Background(), "x", 0); err != nil {
		t.Fatal("no budget in ctx: unlimited")
	}
}

func TestPolyHaven(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/assets":
			json.NewEncoder(w).Encode(map[string]any{
				"weathered_wood_01": map[string]any{"name": "Weathered Wood 01", "tags": []string{"plank"}, "categories": []string{"wood"}},
				"red_brick_03":      map[string]any{"name": "Red Brick 03", "categories": []string{"brick"}},
			})
		case r.URL.Path == "/files/weathered_wood_01":
			f := func(n string) map[string]any {
				return map[string]any{"1k": map[string]any{"jpg": map[string]any{"url": "https://dl.polyhaven.org/" + n + ".jpg", "size": 10}}}
			}
			json.NewEncoder(w).Encode(map[string]any{"Diffuse": f("diff"), "nor_gl": f("nor"), "Rough": f("rough"), "AO": f("ao")})
		case strings.HasSuffix(r.URL.Path, ".jpg"):
			w.Write([]byte("jpeg-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	routeAll(t, srv)
	st := newMemStore()
	ph := &PolyHaven{Store: st, API: "https://api.polyhaven.com"}
	ctx := WithBudget(context.Background())
	out, err := ph.Call(ctx, "polyhaven_search_textures", json.RawMessage(`{"query":"wood"}`))
	if err != nil || !strings.Contains(out.Text, "weathered_wood_01") || strings.Contains(out.Text, "red_brick") || len(out.Images) != 1 {
		t.Fatalf("search: %v\n%+v", err, out)
	}
	out, err = ph.Call(ctx, "polyhaven_import_texture", json.RawMessage(`{"id":"weathered_wood_01","resolution":"1k"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"color", "normal", "rough", "ao"} {
		if _, ok := st.files["assets/textures/polyhaven/weathered_wood_01/1k/"+f+".jpg"]; !ok {
			t.Errorf("missing %s map; text:\n%s", f, out.Text)
		}
	}
}

func TestGoogleFonts(t *testing.T) {
	css := `/* vietnamese */
@font-face {
  font-family: 'Space Grotesk';
  font-weight: 400;
  src: url(https://fonts.gstatic.com/s/sg/vi.woff2) format('woff2');
}
/* latin */
@font-face {
  font-family: 'Space Grotesk';
  font-weight: 400;
  src: url(https://fonts.gstatic.com/s/sg/latin.woff2) format('woff2');
  unicode-range: U+0000-00FF;
}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/css2":
			if r.URL.Query().Get("family") != "Space Grotesk:wght@400;700" {
				w.WriteHeader(400)
				return
			}
			w.Write([]byte(css))
		case strings.HasSuffix(r.URL.Path, ".woff2"):
			w.Write([]byte("wOF2" + r.URL.Path))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	routeAll(t, srv)
	st := newMemStore()
	gf := &GoogleFonts{Store: st}
	ctx := WithBudget(context.Background())
	out, err := gf.Call(ctx, "google_fonts_import", json.RawMessage(`{"family":"Space Grotesk","axes":"wght@400;700"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.files) != 1 || !strings.Contains(out.Text, "url(/media/assets/fonts/google/space-grotesk/") || strings.Contains(out.Text, "gstatic") {
		t.Fatalf("want only the latin file, self-hosted:\n%s\nfiles %v", out.Text, len(st.files))
	}
	if _, err := gf.Call(ctx, "google_fonts_import", json.RawMessage(`{"family":"Space Grotesk","axes":"wght@999"}`)); err == nil {
		t.Error("an unknown axis spec should be refused with a message")
	}
}

func TestKrea(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 64))
	polls := 0
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Host") == "api.krea.ai" && r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/generate/image/krea/krea-2/medium":
			json.NewDecoder(r.Body).Decode(&got)
			json.NewEncoder(w).Encode(map[string]any{"job_id": "550e8400-e29b-41d4-a716-446655440000", "status": "queued"})
		case r.URL.Path == "/jobs/550e8400-e29b-41d4-a716-446655440000":
			polls++
			if polls < 2 {
				json.NewEncoder(w).Encode(map[string]any{"status": "processing"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "completed", "result": map[string]any{"urls": []string{"https://app-uploads.krea.ai/public/x-image.png"}}})
		case r.URL.Path == "/public/x-image.png":
			w.Write(png)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	routeAll(t, srv)
	st := newMemStore()
	k := &Krea{Token: "k", Store: st, Public: "https://uc.example", API: "https://api.krea.ai", Poll: time.Millisecond}
	ctx := WithBudget(context.Background())
	out, err := k.Call(ctx, "krea_generate_image", json.RawMessage(`{"prompt":"a matte painting of a wedge-shaped ship","aspect_ratio":"16:9"}`))
	if err != nil {
		t.Fatal(err)
	}
	name := "assets/images/krea/550e8400-e29b-41d4-a716-446655440000.png"
	if !bytes.Equal(st.files[name], png) || got["aspect_ratio"] != "16:9" || got["resolution"] != "1K" || polls != 2 {
		t.Fatalf("stored %d bytes, request %v, polls %d", len(st.files[name]), got, polls)
	}
	if len(out.Images) != 1 || out.Images[0] != "https://uc.example/media/"+name || !strings.Contains(out.Text, "/media/"+name) {
		t.Fatalf("output %+v", out)
	}
	for range 3 {
		k.Call(ctx, "krea_generate_image", json.RawMessage(`{"prompt":"another image prompt here","aspect_ratio":"1:1"}`))
	}
	if _, err := k.Call(ctx, "krea_generate_image", json.RawMessage(`{"prompt":"one too many images now","aspect_ratio":"1:1"}`)); err == nil {
		t.Fatal("a fifth image in one run was allowed")
	}
}

func TestKreaVideo(t *testing.T) {
	mp4 := append([]byte{0, 0, 0, 0x18}, []byte("ftypisom"+strings.Repeat("v", 40))...)
	var body map[string]any
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/generate/video/bytedance/seedance-2-5":
			body = nil
			json.NewDecoder(r.Body).Decode(&body)
			json.NewEncoder(w).Encode(map[string]any{"job_id": "11111111-2222-4333-8444-555555555555", "status": "queued"})
		case r.URL.Path == "/jobs/11111111-2222-4333-8444-555555555555":
			if fail {
				json.NewEncoder(w).Encode(map[string]any{"status": "failed", "error": map[string]any{"message": "nope"}})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "completed", "result": map[string]any{"urls": []string{"https://app-uploads.krea.ai/public/v.mp4"}}})
		case r.URL.Path == "/public/v.mp4":
			w.Write(mp4)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	routeAll(t, srv)
	st := newMemStore()
	st.files["assets/images/krea/still.png"] = []byte("png")
	k := &KreaVideo{Token: "k", Store: st, Public: "https://uc.example", API: "https://api.krea.ai", Poll: time.Millisecond, DailyCap: 5}
	ctx := WithBudget(context.Background())
	call := func(in string) (Output, error) { return k.Call(ctx, "krea_generate_video", json.RawMessage(in)) }

	out, err := call(`{"prompt":"the ship glides slowly to the left","model":"seedance-2.5","aspect_ratio":"21:9","seconds":5,"start_image":"/media/assets/images/krea/still.png"}`)
	if err != nil {
		t.Fatal(err)
	}
	if body["start_image"] != "https://uc.example/media/assets/images/krea/still.png" || body["resolution"] != "720p" || body["duration"] != float64(5) {
		t.Fatalf("request = %v", body)
	}
	if !bytes.Equal(st.files["assets/videos/krea/11111111-2222-4333-8444-555555555555.mp4"], mp4) || !strings.Contains(out.Text, "$1.60") {
		t.Fatalf("stored/estimate: %s", out.Text)
	}
	// a start image that isn't in the media store is refused before any spend
	if _, err := call(`{"prompt":"the ship glides slowly to the left","model":"seedance-2.5","aspect_ratio":"16:9","seconds":5,"start_image":"https://elsewhere.example/x.png"}`); err == nil {
		t.Error("external start image accepted")
	}
	// a failed job is refunded; then the daily cap ($5: 1.60 spent) refuses a 12s clip ($3.84)
	fail = true
	if _, err := call(`{"prompt":"the ship glides slowly to the left","model":"seedance-2.5","aspect_ratio":"16:9","seconds":5,"start_image":""}`); err == nil {
		t.Fatal("failed job reported success")
	}
	fail = false
	ctx = WithBudget(context.Background()) // a new run
	if _, err := call(`{"prompt":"the ship glides slowly to the left","model":"seedance-2.5","aspect_ratio":"16:9","seconds":12,"start_image":""}`); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("over the daily cap: %v", err)
	}
	if k.spent != 1.60 {
		t.Fatalf("spent = %v, want 1.60 (the failed clip refunded)", k.spent)
	}
}

func TestForRunHidesBenOnly(t *testing.T) {
	all := []Connection{&PolyHaven{}, &KreaVideo{}}
	if n := len(ForRun(all, false, false)); n != 2 {
		t.Errorf("Ben's run: %d connections", n)
	}
	for _, c := range [][2]bool{{true, false}, {false, true}} {
		got := ForRun(all, c[0], c[1])
		if len(got) != 1 || got[0].Name() != "polyhaven" {
			t.Errorf("visitor=%v observer=%v: %v", c[0], c[1], got)
		}
	}
}
