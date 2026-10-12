package server

import (
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/notify"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// "Email me when it's ready" (protocol v1.13). While a visitor's build runs,
// the modal offers to email them when it ends. The address is stored with the
// run only until the email goes out (store.Notifies). The email's link asks
// the browser that opens it whether to move the build there (notify.Link).

const (
	// NotifyLinkTTL is how long an email's link works after the build ends.
	NotifyLinkTTL = 7 * 24 * time.Hour
	// NotifyPerAddressDaily caps emails to one address in 24 hours, so the
	// form can't be used to send someone a stream of them.
	NotifyPerAddressDaily = 5
)

// notifyState is the mailer (nil: the offer isn't shown) and where Ben's
// alerts go ("": none).
type notifyState struct {
	mailer notify.Mailer
	admin  string
}

// WithAdminAlerts sends Ben an email when a visitor builds a site, and when a
// visitor is emailed (with their address). ADMIN_NOTIFY_EMAIL.
func WithAdminAlerts(addr string) Option {
	return func(s *Server) error {
		s.notify.admin = strings.TrimSpace(addr)
		return nil
	}
}

// WithMailer turns on build emails.
func WithMailer(m notify.Mailer) Option {
	return func(s *Server) error {
		s.notify.mailer = m
		return nil
	}
}

// notifyStore is the store's notify side, or nil when emails are off.
func (s *Server) notifyStore() store.Notifies {
	if s.notify.mailer == nil {
		return nil
	}
	n, _ := s.store.(store.Notifies)
	return n
}

func (s *Server) notifyRoutes(mux *http.ServeMux) {
	cop := http.NewCrossOriginProtection()
	post := func(h http.HandlerFunc) http.Handler { return cop.Handler(s.withSession(h)) }
	mux.Handle("POST /build/api/fe/{slug}/notify", post(s.buildNotify))
	mux.Handle("DELETE /build/api/fe/{slug}/notify", post(s.buildNotifyClear))
	mux.HandleFunc("GET /build/open/{token}", s.buildOpen)
	mux.HandleFunc("GET /build/api/claim", s.withSession(s.buildClaim))
	mux.Handle("POST /build/api/claim", post(s.buildClaimAccept))
	mux.Handle("DELETE /build/api/claim", post(s.buildClaimDecline))
	if o, ok := s.notify.mailer.(*notify.Outbox); ok && s.cfg.Dev() {
		// dev and e2e only: what the dev mailer "sent"
		mux.HandleFunc("GET /dev/outbox", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, o.Sent())
		})
	}
}

// runningRun is the front end's newest run if it's still running and alive.
func (s *Server) runningRun(ctx context.Context, id string) (store.Run, bool) {
	runs, err := s.builderStore().Runs(ctx, id, 1)
	if err != nil || len(runs) != 1 || runs[0].Status != store.RunRunning ||
		s.now().Sub(runs[0].StartedAt) >= RunTimeout+time.Minute {
		return store.Run{}, false
	}
	return runs[0], true
}

// buildNotify asks to be emailed when the running build ends: {email}.
func (s *Server) buildNotify(w http.ResponseWriter, r *http.Request) {
	ns := s.notifyStore()
	if ns == nil {
		http.Error(w, "Email isn't available right now.", http.StatusServiceUnavailable)
		return
	}
	f, ok := s.ownedFrontend(w, r)
	if !ok {
		return
	}
	var in struct {
		Email string `json:"email"`
	}
	if err := readJSON(w, r, 4<<10, &in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	addr, err := notify.Address(in.Email)
	if err != nil {
		http.Error(w, "That doesn't look like an email address.", http.StatusBadRequest)
		return
	}
	run, ok := s.runningRun(r.Context(), f.ID)
	if !ok {
		http.Error(w, "There's no build running to email you about.", http.StatusConflict)
		return
	}
	hash := notify.AddressHash(addr)
	if cur, err := ns.RunNotify(r.Context(), run.ID); err != nil || string(cur.AddressHash) != string(hash) {
		n, err := ns.NotifyCount(r.Context(), hash, s.now().Add(-24*time.Hour))
		if err != nil {
			s.fail(w, "build: notify count", err)
			return
		}
		if n >= NotifyPerAddressDaily {
			http.Error(w, "That address has had enough emails from us today. Try another, or check back here.", http.StatusTooManyRequests)
			return
		}
	}
	tok, err := notify.Seal(s.cfg.SigningKey, notify.Link{Frontend: f.ID, Expires: s.now().Add(RunTimeout + NotifyLinkTTL)})
	if err != nil {
		s.fail(w, "build: notify link", err)
		return
	}
	if err := ns.SetRunNotify(r.Context(), store.RunNotify{RunID: run.ID, Email: addr, Link: tok, AddressHash: hash}); err != nil {
		s.fail(w, "build: notify", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"notify": notify.Mask(addr)})
}

// buildNotifyClear withdraws the running build's email request.
func (s *Server) buildNotifyClear(w http.ResponseWriter, r *http.Request) {
	ns := s.notifyStore()
	if ns == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	f, ok := s.ownedFrontend(w, r)
	if !ok {
		return
	}
	if run, ok := s.runningRun(r.Context(), f.ID); ok {
		if err := ns.ClearRunNotify(r.Context(), run.ID); err != nil {
			s.fail(w, "build: notify clear", err)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// claimCookie holds an email link's token between the click and the
// visitor's answer (v1.13): the link itself moves nothing.
const claimCookie = "build_claim"

// buildOpen is an email's link. It moves nothing: it keeps the token for an
// hour and opens the builder, which asks "Move it to this browser?".
func (s *Server) buildOpen(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	tok := r.PathValue("token")
	if _, err := notify.Open(s.cfg.SigningKey, tok, s.now()); err != nil {
		http.Redirect(w, r, "/?build=expired", http.StatusFound)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: claimCookie, Value: tok, Path: "/", MaxAge: 3600,
		HttpOnly: true, Secure: !s.cfg.Dev(), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/?build=claim", http.StatusFound)
}

func (s *Server) clearClaim(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: claimCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: !s.cfg.Dev(), SameSite: http.SameSiteLaxMode})
}

// pendingClaim is the build an email link offers this browser, if the link
// is still good and the build still a visitor's.
func (s *Server) pendingClaim(r *http.Request) (store.FrontendInfo, bool) {
	c, err := r.Cookie(claimCookie)
	v := s.visitorStore()
	if err != nil || v == nil {
		return store.FrontendInfo{}, false
	}
	l, err := notify.Open(s.cfg.SigningKey, c.Value, s.now())
	if err != nil || !frontend.IsPrompted(l.Frontend) {
		return store.FrontendInfo{}, false
	}
	ids, err := v.VisitorFrontendIDs(r.Context())
	if err != nil || !ids[l.Frontend] {
		return store.FrontendInfo{}, false
	}
	f, err := s.builderStore().BuilderFrontend(r.Context(), l.Frontend)
	if err != nil {
		return store.FrontendInfo{}, false
	}
	return f, true
}

// buildClaim is the offer: {expired} or {slug, title, here} (here: this
// browser already has it).
func (s *Server) buildClaim(w http.ResponseWriter, r *http.Request) {
	f, ok := s.pendingClaim(r)
	if !ok {
		s.clearClaim(w)
		writeJSON(w, http.StatusOK, map[string]any{"expired": true})
		return
	}
	_, err := s.visitorStore().OwnedFrontend(r.Context(), f.ID, readSession(r))
	writeJSON(w, http.StatusOK, map[string]any{"slug": strings.TrimPrefix(f.ID, "fe/"), "title": f.Title, "here": err == nil})
}

// buildClaimAccept moves the build to this browser (the browser that had it
// loses it, live view included) and shows its newest version on the site.
func (s *Server) buildClaimAccept(w http.ResponseWriter, r *http.Request) {
	f, ok := s.pendingClaim(r)
	if !ok {
		s.clearClaim(w)
		writeJSON(w, http.StatusGone, map[string]string{"error": "That link has expired."})
		return
	}
	if err := s.visitorStore().TransferFrontend(r.Context(), f.ID, readSession(r)); err != nil {
		s.fail(w, "build: claim", err)
		return
	}
	s.log.Info("build: moved by an email link", "frontend", f.ID)
	s.clearClaim(w)
	if revs, err := s.builderStore().Revisions(r.Context(), f.ID); err == nil && len(revs) > 0 {
		newest := revs[0]
		for _, rv := range revs[1:] {
			if rv.Number > newest.Number {
				newest = rv
			}
		}
		http.SetCookie(w, &http.Cookie{Name: liveCookie, Value: f.ID + ":" + newest.ID, Path: "/",
			HttpOnly: true, Secure: !s.cfg.Dev(), SameSite: http.SameSiteLaxMode})
	}
	writeJSON(w, http.StatusOK, map[string]string{"slug": strings.TrimPrefix(f.ID, "fe/")})
}

// buildClaimDecline forgets the offer ("Not now"); the link still works.
func (s *Server) buildClaimDecline(w http.ResponseWriter, r *http.Request) {
	s.clearClaim(w)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// Run outcomes for the email.
const (
	notifyReady    = "ready"
	notifyFailed   = "failed"
	notifyCanceled = "canceled" // no email: the visitor stopped it
)

// afterVisitorRun runs once a visitor's run has ended: Ben hears about a new
// build, the visitor gets their email if they asked, and Ben hears about that.
func (s *Server) afterVisitorRun(runID int64, outcome, prompt string, f store.FrontendInfo, rev store.Revision, usd float64) {
	if outcome == notifyReady {
		s.alertAdmin(adminBuildAlert(s.cfg.MainOrigin, s.notify.admin, prompt, f, rev, usd))
	}
	if to, sent := s.sendRunNotice(runID, outcome, prompt); sent {
		s.alertAdmin(adminEmailedAlert(s.cfg.MainOrigin, s.notify.admin, prompt, f, outcome, to))
	}
}

// alertAdmin sends Ben an alert, if alerts are on.
func (s *Server) alertAdmin(m notify.Message) {
	if s.notify.mailer == nil || s.notify.admin == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.notify.mailer.Send(ctx, m); err != nil {
		s.log.Error("notify: admin alert", "subject", m.Subject, "err", err)
	}
}

// sendRunNotice emails the visitor who asked about runID, once, if they did,
// and returns their address if it went out.
func (s *Server) sendRunNotice(runID int64, outcome, prompt string) (string, bool) {
	ns := s.notifyStore()
	if ns == nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n, ok, err := ns.TakeRunNotify(ctx, runID, outcome)
	if err != nil {
		s.log.Error("notify: take", "run", runID, "err", err)
		return "", false
	}
	if !ok || outcome == notifyCanceled {
		return "", false
	}
	link := strings.TrimRight(s.cfg.MainOrigin, "/") + "/build/open/" + n.Link
	if err := s.notify.mailer.Send(ctx, runNotice(s.cfg.MainOrigin, n.Email, outcome, link, prompt)); err != nil {
		s.log.Error("notify: send", "run", runID, "outcome", outcome, "err", err)
		return "", false
	}
	s.log.Info("notify: sent", "run", runID, "outcome", outcome)
	return n.Email, true
}

// noticePromptMax caps how much of the visitor's prompt the email quotes.
const noticePromptMax = 280

// runNotice is the email, led by the visitor's own prompt (Ben's call,
// 2026-10-11): quoted as plain text, escaped, at most noticePromptMax
// characters. The HTML is emails/build.html.
func runNotice(origin, to, outcome, link, prompt string) notify.Message {
	origin = strings.TrimRight(origin, "/")
	d := noticeData{Origin: origin, Link: link, Ready: outcome == notifyReady, Prompt: noticePrompt(prompt),
		Note:   "The link works for 7 days. Open it on any device and we'll ask before moving your build to that browser.",
		Footer: "You're getting this because you asked to be emailed when your build finished."}
	if d.Ready {
		d.Subject = "Your version of benpriddy.com is ready"
		d.Preheader = "Your version finished generating. Go check it out!"
		d.Kicker = "Your build \u00b7 Ready"
		d.Heading = "Your version of the site is ready."
		d.Lead = "Only you can see it unless you want to submit it. Click this link to open the session."
		d.CTA = "See your version"
	} else {
		d.Subject = "Your build of benpriddy.com didn't finish"
		d.Preheader = "Something went wrong partway. Anything you made before is still there."
		d.Kicker = "Your build \u00b7 Didn't finish"
		d.Heading = "Your build didn't finish."
		d.Lead = "Something went wrong and it stopped before it was done. Anything you made before is still there, and you can try again."
		d.CTA = "Try again"
	}
	switch n := len([]rune(d.Prompt)); {
	case n > 160:
		d.PromptSize, d.PromptClass = 24, "prompt-xs"
	case n > 80:
		d.PromptSize, d.PromptClass = 32, "prompt-s"
	default:
		d.PromptSize, d.PromptClass = 42, "prompt"
	}
	var htm strings.Builder
	if err := noticeHTML.Execute(&htm, d); err != nil {
		htm.Reset() // can't happen with this fixed template; the text part still goes
	}
	head := d.Heading
	if d.Prompt != "" {
		head = "\u201c" + d.Prompt + "\u201d\n\n" + d.Heading
	}
	text := head + "\n\n" + d.Lead + "\n\n" + d.CTA + ": " + link + "\n\n" + d.Note +
		"\n\n\u2014 benpriddy.com\nBen Priddy \u00b7 Creative technology / AI\n\n" + d.Footer + "\n"
	return notify.Message{To: to, Subject: d.Subject, Text: text, HTML: htm.String()}
}

// noticePrompt is the prompt as the email quotes it: one line, trimmed,
// cut at noticePromptMax with an ellipsis.
func noticePrompt(p string) string {
	p = strings.Join(strings.Fields(p), " ")
	if r := []rune(p); len(r) > noticePromptMax {
		p = strings.TrimSpace(string(r[:noticePromptMax-1])) + "\u2026"
	}
	return p
}

type noticeData struct {
	Prompt, PromptClass                                                        string
	PromptSize                                                                 int
	Origin, Link, Subject, Preheader, Kicker, Heading, Lead, CTA, Note, Footer string
	Ready                                                                      bool
}

//go:embed emails/build.html
var noticeHTMLSrc string

var noticeHTML = template.Must(template.New("build").Parse(noticeHTMLSrc))

// adminBuildAlert tells Ben a visitor built a site.
func adminBuildAlert(origin, to, prompt string, f store.FrontendInfo, rev store.Revision, usd float64) notify.Message {
	origin = strings.TrimRight(origin, "/")
	slug := strings.TrimPrefix(f.ID, "fe/")
	d := noticeData{Origin: origin, Ready: true, Prompt: noticePrompt(prompt),
		Link:      origin + "/admin/builder/fe/" + slug,
		Subject:   "New build: " + f.Title,
		Preheader: "A visitor built a version of the site.",
		Kicker:    "Admin \u00b7 New build",
		Heading:   "A visitor built a site.",
		Lead:      fmt.Sprintf("\u201c%s\u201d, version %d. It cost $%.2f.", f.Title, rev.Number, usd),
		CTA:       "Open it in the admin",
		Footer:    "Sent to you as the admin of benpriddy.com (ADMIN_NOTIFY_EMAIL)."}
	return adminMessage(to, d)
}

// adminEmailedAlert tells Ben a visitor was emailed about their build, and
// who they are.
func adminEmailedAlert(origin, to, prompt string, f store.FrontendInfo, outcome, visitor string) notify.Message {
	origin = strings.TrimRight(origin, "/")
	slug := strings.TrimPrefix(f.ID, "fe/")
	how := "it was ready"
	if outcome != notifyReady {
		how = "it didn't finish"
	}
	d := noticeData{Origin: origin, Ready: true, Prompt: noticePrompt(prompt),
		Link:      origin + "/admin/builder/fe/" + slug,
		Subject:   "Visitor emailed: " + visitor,
		Preheader: visitor + " asked to be emailed about their build.",
		Kicker:    "Admin \u00b7 Visitor emailed",
		Heading:   visitor,
		Lead:      fmt.Sprintf("Asked to be emailed about \u201c%s\u201d, and got the email: %s.", f.Title, how),
		CTA:       "Open it in the admin",
		Footer:    "Sent to you as the admin of benpriddy.com (ADMIN_NOTIFY_EMAIL)."}
	return adminMessage(to, d)
}

func adminMessage(to string, d noticeData) notify.Message {
	d.PromptSize, d.PromptClass = 28, "prompt-s"
	var htm strings.Builder
	noticeHTML.Execute(&htm, d)
	text := d.Heading + "\n\n\u201c" + d.Prompt + "\u201d\n\n" + d.Lead + "\n\n" + d.CTA + ": " + d.Link + "\n"
	return notify.Message{To: to, Subject: d.Subject, Text: text, HTML: htm.String()}
}
