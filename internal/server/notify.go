package server

import (
	"context"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/notify"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// "Email me when it's ready" (protocol v1.13). While a visitor's build runs,
// the modal offers to email them when it ends. The address is stored with the
// run only until the email goes out (store.Notifies); the email's link opens
// the build on any device for a week (notify.Link).

const (
	// NotifyLinkTTL is how long an email's link works after the build ends.
	NotifyLinkTTL = 7 * 24 * time.Hour
	// NotifyPerAddressDaily caps emails to one address in 24 hours, so the
	// form can't be used to send someone a stream of them.
	NotifyPerAddressDaily = 5
)

// notifyState is the mailer (nil: the offer isn't shown).
type notifyState struct {
	mailer notify.Mailer
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
	sid, err := r.Cookie(sidCookie)
	if err != nil || !sidPattern.MatchString(sid.Value) {
		buildNotFound(w)
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
	tok, err := notify.Seal(s.cfg.SigningKey, notify.Link{SID: sid.Value, Frontend: f.ID,
		Expires: s.now().Add(RunTimeout + NotifyLinkTTL)})
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

// buildOpen is an email's link: it signs this browser in as the visitor who
// asked (their sid), shows their newest version live, and opens the modal.
func (s *Server) buildOpen(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	l, err := notify.Open(s.cfg.SigningKey, r.PathValue("token"), s.now())
	if err != nil || !sidPattern.MatchString(l.SID) {
		http.Redirect(w, r, "/?build=expired", http.StatusFound)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sidCookie, Value: l.SID, Path: "/", MaxAge: int(sidMaxAge / time.Second),
		HttpOnly: true, Secure: !s.cfg.Dev(), SameSite: http.SameSiteLaxMode})
	if v := s.visitorStore(); v != nil && frontend.IsPrompted(l.Frontend) {
		if f, err := v.OwnedFrontend(r.Context(), l.Frontend, hashSID(l.SID)); err == nil {
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
		}
	}
	http.Redirect(w, r, "/?build=1", http.StatusFound)
}

// Run outcomes for the email.
const (
	notifyReady    = "ready"
	notifyFailed   = "failed"
	notifyCanceled = "canceled" // no email: the visitor stopped it
)

// sendRunNotice emails the visitor who asked about runID, once, if they did.
func (s *Server) sendRunNotice(runID int64, outcome string) {
	ns := s.notifyStore()
	if ns == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n, ok, err := ns.TakeRunNotify(ctx, runID, outcome)
	if err != nil {
		s.log.Error("notify: take", "run", runID, "err", err)
		return
	}
	if !ok || outcome == notifyCanceled {
		return
	}
	link := strings.TrimRight(s.cfg.MainOrigin, "/") + "/build/open/" + n.Link
	if err := s.notify.mailer.Send(ctx, runNotice(n.Email, outcome, link)); err != nil {
		s.log.Error("notify: send", "run", runID, "outcome", outcome, "err", err)
		return
	}
	s.log.Info("notify: sent", "run", runID, "outcome", outcome)
}

// runNotice is the email. It carries nothing the visitor typed: an address
// someone else entered gets a plain, harmless note.
func runNotice(to, outcome, link string) notify.Message {
	const footer = "You're getting this because you asked to be emailed when your build of benpriddy.com finished. We don't keep your address."
	var subject, lead, cta string
	if outcome == notifyReady {
		subject = "Your version of benpriddy.com is ready"
		lead = "Your build is done. Only you can see it until you send it to Ben."
		cta = "See your version"
	} else {
		subject = "Your build of benpriddy.com didn't finish"
		lead = "Something went wrong and your build stopped before it was done. Anything you made before is still there."
		cta = "Try again"
	}
	text := lead + "\n\n" + cta + ": " + link + "\n\nThe link opens it in any browser for the next 7 days.\n\n— benpriddy.com\n\n" + footer + "\n"
	esc := html.EscapeString
	htm := `<!doctype html><html><body style="margin:0;padding:32px 20px;background:#FAFAF8;font:16px/1.5 -apple-system,system-ui,sans-serif;color:#17130f">` +
		`<div style="max-width:520px;margin:0 auto">` +
		`<p style="margin:0 0 8px;font:12px/1.4 ui-monospace,Menlo,monospace;letter-spacing:.12em;text-transform:uppercase;color:#7a7066">benpriddy.com</p>` +
		`<h1 style="margin:0 0 16px;font-size:24px;line-height:1.2">` + esc(subject) + `</h1>` +
		`<p style="margin:0 0 24px">` + esc(lead) + `</p>` +
		`<p style="margin:0 0 24px"><a href="` + esc(link) + `" style="display:inline-block;padding:12px 20px;background:#17130f;color:#FAFAF8;text-decoration:none;border-radius:4px;font-weight:600">` + esc(cta) + `</a></p>` +
		`<p style="margin:0 0 32px;color:#5b5248;font-size:14px">The link opens it in any browser for the next 7 days.</p>` +
		`<p style="margin:0;color:#7a7066;font-size:12px">` + esc(footer) + `</p>` +
		`</div></body></html>`
	return notify.Message{To: to, Subject: subject, Text: text, HTML: htm}
}
