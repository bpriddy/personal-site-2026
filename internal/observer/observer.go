// Package observer is the site observer (docs/observer.md, "Layer 3"): it
// ingests detections from front ends (content gaps, type breaks, front-end
// failures) and from server-side content checks, heals them with the priority
// of displaying the correct content, and records everything for the admin.
//
// Healing rules:
//   - a gap is filled by generating the value from the item's other content
//     (Claude, structured output), applied immediately and flagged; generated
//     values never overwrite human content;
//   - a type break on a human value needs review (a fix would overwrite it);
//   - a front end that keeps failing is pulled from the rotation (never the
//     default front end); code patches are a later phase, so errors need review.
package observer

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/revfiles"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// Config configures an Observer. Content and Obs are required.
type Config struct {
	Content store.Store
	Obs     store.ObserverStore
	// Model generates values; nil turns healing off (detections are still
	// recorded, and the admin says healing is off).
	Model     Model
	ModelName string // recorded with generated values when the model doesn't report one
	// TypedValues: the content contract serves list, number and bool
	// generated values typed (via GeneratedValues). Until it does, gaps
	// expecting them need review instead of generating a value that would
	// arrive as a string.
	TypedValues bool
	Log         *slog.Logger

	Limits      Limits        // POST /api/observe rate limits; zero means DefaultLimits
	XFFHops     int           // see ClientIP
	CheckEvery  time.Duration // content checks and the stale sweep; zero means 5 minutes
	QueueSize   int           // pending reports; zero means 256 (overflow is dropped)
	GenPerHour  int           // generation budget per instance; zero means 60
	MaxGenItem  int           // generated fields per item; zero means 8
	ErrorWindow time.Duration // front-end error window; zero means 10 minutes
	ErrorLimit  int           // errors in the window that pull a front end; zero means 5
	NotReady    int           // "never ready" reports in the window that pull it; zero means 3

	// Content drift (drift.go): front ends in the rotation that don't read
	// content the site now has are rebuilt by the builder (SetRebuilder).
	Files             revfiles.Store // revision files, for the static scan; nil turns drift checks off
	DriftEvery        time.Duration  // drift check interval; zero means 10 minutes
	DriftDelay        time.Duration  // first check after Start; zero means 30 seconds
	DriftDebounce     time.Duration  // wait after a content change before checking; zero means 3 seconds
	RebuildsPerDay    int            // creative rebuilds per rolling 24 hours, all instances; zero means 6
	RebuildTimeout    time.Duration  // one rebuild; zero means 25 minutes
	Probation         time.Duration  // watch after an observer activation; zero means 30 minutes
	ProbationErrors   int            // errors in probation that roll back; zero means 3
	ProbationNotReady int            // never-ready reports in probation that roll back; zero means 2

	Now func() time.Time // for tests
}

// Observer is safe for concurrent use.
type Observer struct {
	cfg     Config
	content store.Store
	obs     store.ObserverStore
	model   Model
	log     *slog.Logger
	now     func() time.Time
	limiter *limiter

	reports chan Report
	heals   chan healJob

	mu       sync.Mutex
	inflight map[string]bool        // heal jobs queued or running, by field key
	errors   map[string][]errorMark // recent front-end errors, by front end
	budget   bucket                 // generations
	startMu  sync.Mutex
	started  bool

	// content drift (drift.go)
	files     revfiles.Store
	rebuilder Rebuilder             // guarded by mu
	changed   chan struct{}         // content changed; coalesced
	changes   atomic.Int64          // ContentChanged calls (tests)
	driftMu   sync.Mutex            // one drift check (and rebuild) at a time per instance
	probation map[string]*probation // by front end; guarded by mu
	override  frontend.Rotation     // FRONTEND_ROTATION, if set
}

type errorMark struct {
	at       time.Time
	notReady bool
}

type healJob struct {
	id    int64
	force bool
}

func New(cfg Config) *Observer {
	if cfg.Limits == (Limits{}) {
		cfg.Limits = DefaultLimits
	}
	if cfg.CheckEvery == 0 {
		cfg.CheckEvery = 5 * time.Minute
	}
	if cfg.QueueSize == 0 {
		cfg.QueueSize = 256
	}
	if cfg.GenPerHour == 0 {
		cfg.GenPerHour = 60
	}
	if cfg.MaxGenItem == 0 {
		cfg.MaxGenItem = 8
	}
	if cfg.ErrorWindow == 0 {
		cfg.ErrorWindow = 10 * time.Minute
	}
	if cfg.ErrorLimit == 0 {
		cfg.ErrorLimit = 5
	}
	if cfg.NotReady == 0 {
		cfg.NotReady = 3
	}
	if cfg.DriftEvery == 0 {
		cfg.DriftEvery = 10 * time.Minute
	}
	if cfg.DriftDelay == 0 {
		cfg.DriftDelay = 30 * time.Second
	}
	if cfg.DriftDebounce == 0 {
		cfg.DriftDebounce = 3 * time.Second
	}
	if cfg.RebuildsPerDay == 0 {
		cfg.RebuildsPerDay = 6
	}
	if cfg.RebuildTimeout == 0 {
		cfg.RebuildTimeout = 25 * time.Minute
	}
	if cfg.Probation == 0 {
		cfg.Probation = 30 * time.Minute
	}
	if cfg.ProbationErrors == 0 {
		cfg.ProbationErrors = 3
	}
	if cfg.ProbationNotReady == 0 {
		cfg.ProbationNotReady = 2
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	o := &Observer{
		cfg: cfg, content: cfg.Content, obs: cfg.Obs, model: cfg.Model, log: cfg.Log, now: cfg.Now,
		limiter:  newLimiter(cfg.Limits, cfg.Now),
		reports:  make(chan Report, cfg.QueueSize),
		heals:    make(chan healJob, 64),
		inflight: map[string]bool{},
		errors:   map[string][]errorMark{},
		budget:   bucket{tokens: float64(cfg.GenPerHour), last: cfg.Now()},

		files:     cfg.Files,
		changed:   make(chan struct{}, 1),
		probation: map[string]*probation{},
	}
	if rot, ok, _ := frontend.RotationOverride(); ok {
		o.override = rot
	}
	return o
}

// HealingEnabled reports whether a model is configured.
func (o *Observer) HealingEnabled() bool { return o.model != nil }

// Store returns the observer's store (for the admin).
func (o *Observer) Store() store.ObserverStore { return o.obs }

// Start runs the workers until ctx is done: one ingests reports, one heals
// (model calls are serialized, which also bounds cost), and a ticker runs the
// content checks. Calling it twice is a no-op.
func (o *Observer) Start(ctx context.Context) {
	o.startMu.Lock()
	defer o.startMu.Unlock()
	if o.started {
		return
	}
	o.started = true
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case r := <-o.reports:
				o.record(ctx, r, true)
			}
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case j := <-o.heals:
				o.runHeal(ctx, j)
			}
		}
	}()
	go o.driftLoop(ctx)
	go func() {
		o.CheckContent(ctx)
		t := time.NewTicker(o.cfg.CheckEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				o.CheckContent(ctx)
			}
		}
	}()
}

// Submit queues a validated report from ip. It never blocks: a report over
// the rate limit or beyond a full queue is dropped (and false is returned).
func (o *Observer) Submit(ip string, r Report) bool {
	if !o.limiter.allow(ip) {
		return false
	}
	select {
	case o.reports <- r:
		return true
	default:
		o.log.Warn("observer: queue full, report dropped")
		return false
	}
}

// Process records r and heals it synchronously (tests, and the live check).
func (o *Observer) Process(ctx context.Context, r Report) (store.Detection, error) {
	d, err := o.record(ctx, r, false)
	if err != nil || d.ID == 0 {
		return d, err
	}
	if d.Kind == store.KindContentGap || d.Kind == store.KindTypeBreak {
		o.heal(ctx, d.ID, false)
	}
	return o.obs.Detection(ctx, d.ID)
}

// record stores a report as a detection. Reports about content that doesn't
// exist, or front ends that aren't registered, are dropped: they can't be
// healed, and recording them would let anyone fill the admin with noise.
// With async set, healing is queued rather than run.
func (o *Observer) record(ctx context.Context, r Report, async bool) (store.Detection, error) {
	switch r.Kind {
	case store.KindContentGap, store.KindTypeBreak:
		if _, err := o.loadItem(ctx, r.Collection, r.Item); err != nil {
			if err != errNoItem {
				o.log.Error("observer: load item", "err", err)
			}
			return store.Detection{}, err
		}
	case store.KindFrontendError:
		if ok, err := o.registered(ctx, r.Frontend); !ok {
			return store.Detection{}, err
		}
	}
	d, created, err := o.obs.RecordDetection(ctx, store.Detection{
		Kind: r.Kind, Frontend: r.Frontend, Serve: r.Serve, Route: r.Route,
		Collection: r.Collection, Item: r.Item, Field: r.Field, Expect: r.Expect, Got: r.Got,
		Signature: r.Signature(), Sample: r.sample(), Status: store.StatusNew,
	})
	if err != nil {
		o.log.Error("observer: record", "err", err)
		return d, err
	}
	switch d.Kind {
	case store.KindFrontendError:
		o.frontendError(ctx, d, r, created)
	case store.KindContentGap, store.KindTypeBreak:
		if async {
			o.queueHeal(d, false)
		}
	}
	return d, nil
}

// fieldKey identifies the content a heal job writes, so two front ends'
// reports about the same field don't trigger two generations.
func fieldKey(d store.Detection) string {
	return d.Collection + "\x00" + d.Item + "\x00" + d.Field
}

func (o *Observer) queueHeal(d store.Detection, force bool) {
	if d.Status == store.StatusDismissed && !force {
		return
	}
	k := fieldKey(d)
	o.mu.Lock()
	if o.inflight[k] {
		o.mu.Unlock()
		return
	}
	o.inflight[k] = true
	o.mu.Unlock()
	select {
	case o.heals <- healJob{id: d.ID, force: force}:
	default:
		o.mu.Lock()
		delete(o.inflight, k)
		o.mu.Unlock()
		o.log.Warn("observer: heal queue full; the next report retries")
	}
}

func (o *Observer) runHeal(ctx context.Context, j healJob) {
	d, err := o.obs.Detection(ctx, j.id)
	defer func() {
		o.mu.Lock()
		delete(o.inflight, fieldKey(d))
		o.mu.Unlock()
	}()
	if err != nil {
		o.log.Error("observer: heal", "err", err)
		return
	}
	o.heal(ctx, j.id, j.force)
}

// takeBudget spends one generation from the hourly budget.
func (o *Observer) takeBudget() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := float64(o.cfg.GenPerHour)
	return o.budget.take(o.now(), n/3600, n)
}
