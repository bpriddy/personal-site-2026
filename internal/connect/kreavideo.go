package connect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"
)

// KreaVideo generates short video clips with Krea (Seedance 2.5 or MiniMax
// H3) and copies the MP4 into the media store. Video costs dollars a clip, so
// it is offered only on Ben's own builder runs (BenOnly: not visitors', not
// the observer's), at most 2 clips a run, under a daily spend cap.
type KreaVideo struct {
	Token string
	Store Store
	// Public is the user-content origin: a start frame given as a /media/
	// path is passed to Krea as a public URL there.
	Public string
	// DailyCap is the most estimated spend (USD) per UTC day; 0 means 15.
	DailyCap float64
	API      string        // API base (tests point it at a fake)
	Poll     time.Duration // polling interval (tests shorten it)
	Now      func() time.Time

	mu    sync.Mutex
	day   string
	spent float64
}

const (
	kreaVideoPerRun   = 2
	kreaVideoMaxBytes = 30 << 20
	kreaVideoWait     = 9 * time.Minute
	kreaVideoBase     = "assets/videos/krea/"
)

// the offered models: endpoint, estimated USD per second (Krea's published
// rates, rounded up), and allowed durations
var kreaVideoModels = map[string]struct {
	path        string
	perSecond   float64
	minS, maxS  int
	resolution  bool // takes a resolution (we send 720p)
	description string
}{
	"seedance-2.5": {"/generate/video/bytedance/seedance-2-5", 0.32, 4, 12, true, "ByteDance Seedance 2.5: the strongest overall; rich motion, cinematic camera, 720p"},
	"minimax-h3":   {"/generate/video/minimax/hailuo-3", 0.14, 5, 10, false, "MiniMax H3: frame animation, faithful to a start frame, cheaper"},
}

var kreaVideoAspects = []string{"16:9", "21:9", "4:3", "1:1", "3:4", "9:16"}

func (k *KreaVideo) api() string {
	if k.API != "" {
		return k.API
	}
	return kreaAPI
}

func (k *KreaVideo) Name() string { return "kreavideo" }

// BenOnly: offered only on Ben's own runs.
func (k *KreaVideo) BenOnly() bool { return true }

func (k *KreaVideo) Tools() []Tool {
	return []Tool{
		{Name: "krea_generate_video", Description: "Generate a short silent-looping video clip with Krea for this front end (hero motion, an atmospheric loop, a living background). Costs real money (about $0.70-$4 a clip), so use it only when motion is central to the concept; at most 2 per run. Usually: make a still with krea_generate_image first, check it, then animate it by passing its /media/ path as start_image. Takes 1-5 minutes. Returns the MP4's /media/ path. Read the generated-video skill first.",
			Props: map[string]any{
				"prompt":       map[string]any{"type": "string", "description": "What moves and how: subject motion, camera move, pace, atmosphere. Style is set by the start image when you give one."},
				"model":        map[string]any{"type": "string", "enum": []string{"seedance-2.5", "minimax-h3"}},
				"aspect_ratio": map[string]any{"type": "string", "enum": kreaVideoAspects},
				"seconds":      map[string]any{"type": "integer", "description": "Clip length: 5 to 10 (seedance-2.5 allows 4-12). Short loops are best."},
				"start_image":  map[string]any{"type": "string", "description": "A /media/ image path to animate from (e.g. one you just generated), or \"\" for none"},
			}},
	}
}

// estimate is the expected cost of a clip.
func (k *KreaVideo) estimate(model string, seconds int) float64 {
	return math.Ceil(kreaVideoModels[model].perSecond*float64(seconds)*100) / 100
}

// reserve takes est from today's budget, or refuses.
func (k *KreaVideo) reserve(est float64) error {
	now := time.Now
	if k.Now != nil {
		now = k.Now
	}
	cap := k.DailyCap
	if cap == 0 {
		cap = 15
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if d := now().UTC().Format("2006-01-02"); d != k.day {
		k.day, k.spent = d, 0
	}
	if k.spent+est > cap {
		return Userf("Today's video budget is used up ($%.2f of $%.0f). Build the motion in code (CSS, canvas or WebGPU) instead.", k.spent, cap)
	}
	k.spent += est
	return nil
}

// refund gives back a reservation for a clip that didn't happen.
func (k *KreaVideo) refund(est float64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.spent = max(0, k.spent-est)
}

func (k *KreaVideo) Call(ctx context.Context, tool string, input json.RawMessage) (Output, error) {
	if tool != "krea_generate_video" {
		return Output{}, Userf("Unknown tool %q", tool)
	}
	var in struct {
		Prompt     string `json:"prompt"`
		Model      string `json:"model"`
		Aspect     string `json:"aspect_ratio"`
		Seconds    int    `json:"seconds"`
		StartImage string `json:"start_image"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Output{}, Userf("Invalid input: %v", err)
	}
	m, ok := kreaVideoModels[in.Model]
	if !ok {
		return Output{}, Userf("model must be seedance-2.5 or minimax-h3.")
	}
	prompt := strings.TrimSpace(in.Prompt)
	if len(prompt) < 10 || len(prompt) > 3000 {
		return Output{}, Userf("Give a descriptive prompt (10-3000 characters).")
	}
	in.Seconds = min(max(in.Seconds, m.minS), m.maxS)
	aspect := "16:9"
	for _, a := range kreaVideoAspects {
		if a == in.Aspect {
			aspect = a
		}
	}
	body := map[string]any{"prompt": prompt, "aspect_ratio": aspect, "duration": in.Seconds}
	if m.resolution {
		body["resolution"] = "720p"
	}
	if s := strings.TrimSpace(in.StartImage); s != "" {
		if !strings.HasPrefix(s, "/media/") || !strings.HasPrefix(k.Public, "https://") {
			return Output{}, Userf("start_image must be a /media/ image path (or \"\").")
		}
		name := strings.TrimPrefix(s, "/media/")
		if ok, err := k.Store.Exists(ctx, name); err != nil || !ok {
			return Output{}, Userf("No image at %s.", s)
		}
		body["start_image"] = strings.TrimRight(k.Public, "/") + s
	}
	if err := Spend(ctx, "video clips", kreaVideoPerRun); err != nil {
		return Output{}, err
	}
	est := k.estimate(in.Model, in.Seconds)
	if err := k.reserve(est); err != nil {
		return Output{}, err
	}
	out, err := k.generate(ctx, m.path, body)
	if err != nil {
		k.refund(est) // nothing to pay for (best effort: a failed job usually isn't billed)
		return Output{}, err
	}
	out.Text = fmt.Sprintf("%s\nModel %s, %ds, %s; estimated cost $%.2f.", out.Text, in.Model, in.Seconds, aspect, est)
	return out, nil
}

func (k *KreaVideo) generate(ctx context.Context, path string, body map[string]any) (Output, error) {
	auth := map[string]string{"Authorization": "Bearer " + k.Token}
	b, _ := json.Marshal(body)
	var job struct {
		JobID string `json:"job_id"`
	}
	if err := postJSON(ctx, k.api()+path, auth, b, &job); err != nil {
		return Output{}, Userf("Krea didn't accept the video request (%v).", err)
	}
	if !kreaJobID.MatchString(job.JobID) {
		return Output{}, Userf("Krea returned no job.")
	}
	poll := k.Poll
	if poll == 0 {
		poll = 5 * time.Second
	}
	deadline := time.Now().Add(kreaVideoWait)
	var res struct {
		Status string `json:"status"`
		Result struct {
			URLs []string `json:"urls"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	for {
		if err := getJSON(ctx, k.api()+"/jobs/"+url.PathEscape(job.JobID), auth, &res); err != nil {
			return Output{}, Userf("Checking the Krea video job failed (%v).", err)
		}
		if res.Status == "completed" || res.Status == "failed" || res.Status == "cancelled" {
			break
		}
		if time.Now().After(deadline) {
			return Output{}, Userf("The video took too long (job %s). Do without it.", job.JobID)
		}
		select {
		case <-ctx.Done():
			return Output{}, ctx.Err()
		case <-time.After(poll):
		}
	}
	if res.Status != "completed" || len(res.Result.URLs) == 0 {
		why := res.Status
		if res.Error != nil && res.Error.Message != "" {
			why += ": " + res.Error.Message
		}
		return Output{}, Userf("Krea couldn't make that video (%s). Rephrase it (no real people, brands or logos).", why)
	}
	src, err := url.Parse(res.Result.URLs[0])
	if err != nil || src.Scheme != "https" || !(src.Host == "krea.ai" || strings.HasSuffix(src.Host, ".krea.ai")) {
		return Output{}, Userf("Krea returned an unexpected video location (%s).", src.Host)
	}
	mp4, err := get(ctx, src.String(), nil, kreaVideoMaxBytes)
	if err != nil {
		return Output{}, Userf("Downloading the video failed (%v).", err)
	}
	if len(mp4) < 12 || !bytes.Equal(mp4[4:8], []byte("ftyp")) {
		return Output{}, Userf("Krea returned something that isn't an MP4.")
	}
	name := kreaVideoBase + strings.ToLower(job.JobID) + ".mp4"
	if err := k.Store.Put(ctx, name, mp4, "video/mp4"); err != nil {
		return Output{}, Userf("Saving the video failed (%v).", err)
	}
	return Output{Text: fmt.Sprintf("Generated /media/%s (%s). You can't watch it, so use it as described in the generated-video skill: muted, looping, playsinline, with the start image (if any) as its poster.", name, MB(int64(len(mp4))))}, nil
}
