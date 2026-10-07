package connect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg" // DecodeConfig
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Krea generates images with Krea's API (KREA_API_KEY) and copies them into
// the media store. It uses one model, Krea 2 (medium, 1K), so a run's cost is
// known (about $0.03 an image, at most 4 a run): the model can't reach the
// API's pricier image or video models. (Krea's MCP server, which exposes all
// of them, is for interactive use, not builder runs.)
type Krea struct {
	Token string
	Store Store
	// Public is the user-content origin, so the model can see what it
	// generated (an image block with the stored file's public URL).
	Public string
	API    string // API base (tests point it at a fake)
	// Poll is the polling interval (tests shorten it).
	Poll time.Duration
}

const (
	kreaAPI      = "https://api.krea.ai"
	kreaModel    = "/generate/image/krea/krea-2/medium"
	kreaPerRun   = 4
	kreaMaxBytes = 8 << 20
	kreaWait     = 150 * time.Second
	kreaImageUSD = 0.03 // Krea 2 medium at 1K, per image (Krea's published price)
	kreaBase     = "assets/images/krea/"
)

var (
	kreaAspects = []string{"1:1", "4:3", "3:4", "16:9", "9:16", "21:9", "4:5", "3:2", "2:3"}
	kreaJobID   = regexp.MustCompile(`^[0-9a-zA-Z-]{8,64}$`)
)

func (k *Krea) api() string {
	if k.API != "" {
		return k.API
	}
	return kreaAPI
}

func (k *Krea) Name() string { return "krea" }

func (k *Krea) Tools() []Tool {
	return []Tool{
		{Name: "krea_generate_image", Description: "Generate an image with Krea (the Krea 2 model) for this front end: hero art, an illustration, a background, a texture or a poster in exactly the style the concept needs. Takes 10-60 seconds. Returns its /media/ path and size, and shows you the image so you can judge it. At most 4 per run. Read the generated-images skill first.",
			Props: map[string]any{
				"prompt":       map[string]any{"type": "string", "description": "A detailed visual description: subject, style, medium, lighting, composition, palette. No text or logos in the image."},
				"aspect_ratio": map[string]any{"type": "string", "enum": kreaAspects},
			}},
	}
}

func (k *Krea) Call(ctx context.Context, tool string, input json.RawMessage) (Output, error) {
	if tool != "krea_generate_image" {
		return Output{}, Userf("Unknown tool %q", tool)
	}
	var in struct {
		Prompt string `json:"prompt"`
		Aspect string `json:"aspect_ratio"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Output{}, Userf("Invalid input: %v", err)
	}
	prompt := strings.TrimSpace(in.Prompt)
	if len(prompt) < 10 || len(prompt) > 2000 {
		return Output{}, Userf("Give a descriptive prompt (10-2000 characters).")
	}
	if !slices.Contains(kreaAspects, in.Aspect) {
		in.Aspect = "1:1"
	}
	if err := Spend(ctx, "generated images", kreaPerRun); err != nil {
		return Output{}, err
	}
	auth := map[string]string{"Authorization": "Bearer " + k.Token}

	// submit
	body, _ := json.Marshal(map[string]any{"prompt": prompt, "aspect_ratio": in.Aspect, "resolution": "1K"})
	var job struct {
		JobID  string `json:"job_id"`
		Status string `json:"status"`
	}
	if err := postJSON(ctx, k.api()+kreaModel, auth, body, &job); err != nil {
		return Output{}, Userf("Krea didn't accept the request (%v). Rephrase, or do without a generated image.", err)
	}
	if !kreaJobID.MatchString(job.JobID) {
		return Output{}, Userf("Krea returned no job. Try again, or do without.")
	}

	// wait for it
	poll := k.Poll
	if poll == 0 {
		poll = 2 * time.Second
	}
	deadline := time.Now().Add(kreaWait)
	var res struct {
		Status string `json:"status"`
		Result struct {
			URLs []string `json:"urls"`
		} `json:"result"`
	}
	for {
		if err := getJSON(ctx, k.api()+"/jobs/"+url.PathEscape(job.JobID), auth, &res); err != nil {
			return Output{}, Userf("Checking the Krea job failed (%v).", err)
		}
		if res.Status == "completed" || res.Status == "failed" || res.Status == "cancelled" {
			break
		}
		if time.Now().After(deadline) {
			return Output{}, Userf("Krea took too long. Try a simpler prompt, or do without.")
		}
		select {
		case <-ctx.Done():
			return Output{}, ctx.Err()
		case <-time.After(poll):
		}
	}
	if res.Status != "completed" || len(res.Result.URLs) == 0 {
		return Output{}, Userf("Krea couldn't make that image (%s); it may have declined the prompt. Rephrase it (no real people, brands or logos).", res.Status)
	}
	src, err := url.Parse(res.Result.URLs[0])
	if err != nil || src.Scheme != "https" || !(src.Host == "krea.ai" || strings.HasSuffix(src.Host, ".krea.ai")) {
		return Output{}, Userf("Krea returned an unexpected image location.")
	}

	// copy it in
	img, err := get(ctx, src.String(), nil, kreaMaxBytes)
	if err != nil {
		return Output{}, Userf("Downloading the image failed (%v). Try again.", err)
	}
	ext, ctype := imageType(img)
	if ext == "" {
		return Output{}, Userf("Krea returned something that isn't a PNG, JPEG or WebP.")
	}
	name := kreaBase + strings.ToLower(job.JobID) + ext
	if err := k.Store.Put(ctx, name, img, ctype); err != nil {
		return Output{}, Userf("Saving the image failed (%v).", err)
	}
	AddCost(ctx, kreaImageUSD)
	size := ""
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(img)); err == nil {
		size = fmt.Sprintf(", %d×%d", cfg.Width, cfg.Height)
	}
	out := Output{Text: fmt.Sprintf("Generated /media/%s (%s%s, %s). It's shown below: judge it against the concept and regenerate with a better prompt if it misses (you have %d per run). Use it with its /media/ path; give it real alt text, or alt=\"\" if decorative.",
		name, strings.TrimPrefix(ext, "."), size, MB(int64(len(img))), kreaPerRun)}
	if strings.HasPrefix(k.Public, "https://") {
		out.Images = []string{strings.TrimRight(k.Public, "/") + "/media/" + name}
	}
	return out, nil
}

// imageType sniffs PNG, JPEG or WebP.
func imageType(b []byte) (ext, ctype string) {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return ".png", "image/png"
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return ".jpg", "image/jpeg"
	case len(b) > 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return ".webp", "image/webp"
	}
	return "", ""
}

// postJSON POSTs a JSON body and decodes the response.
func postJSON(ctx context.Context, u string, hdr map[string]string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "benpriddy.com-builder/1 (+https://benpriddy.com)")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(b))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}
	return json.Unmarshal(b, out)
}
