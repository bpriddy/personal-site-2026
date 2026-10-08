// Package notify sends the builder's "your build is ready" emails and makes
// the links in them (protocol v1.13).
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Message is one plain email.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Mailer sends email.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// SendGrid sends through SendGrid's v3 mail API.
type SendGrid struct {
	Key      string // SENDGRID_API_KEY
	From     string // a verified sender, e.g. info@lanterns.build
	FromName string
	Endpoint string       // empty: https://api.sendgrid.com/v3/mail/send
	Client   *http.Client // nil: a client with a 20s timeout
}

func (s *SendGrid) Send(ctx context.Context, m Message) error {
	type addr struct {
		Email string `json:"email"`
		Name  string `json:"name,omitempty"`
	}
	type content struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	body := map[string]any{
		"personalizations": []map[string]any{{"to": []addr{{Email: m.To}}}},
		"from":             addr{Email: s.From, Name: s.FromName},
		"subject":          m.Subject,
		"content":          []content{{"text/plain", m.Text}, {"text/html", m.HTML}},
		// transactional: no click or open tracking rewriting the link
		"tracking_settings": map[string]any{
			"click_tracking": map[string]bool{"enable": false, "enable_text": false},
			"open_tracking":  map[string]bool{"enable": false},
		},
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := s.Endpoint
	if url == "" {
		url = "https://api.sendgrid.com/v3/mail/send"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.Key)
	req.Header.Set("Content-Type", "application/json")
	c := s.Client
	if c == nil {
		c = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return fmt.Errorf("sendgrid: %s: %s", res.Status, bytes.TrimSpace(msg))
	}
	return nil
}

// Outbox is the dev mailer: it logs and keeps what it "sent" (APP_ENV=dev,
// and in tests). Never used in prod.
type Outbox struct {
	Log *slog.Logger
	mu  sync.Mutex
	out []Message
}

func (o *Outbox) Send(_ context.Context, m Message) error {
	if m.To == "" {
		return errors.New("outbox: no recipient")
	}
	o.mu.Lock()
	o.out = append(o.out, m)
	o.mu.Unlock()
	if o.Log != nil {
		o.Log.Info("notify: dev outbox", "to", m.To, "subject", m.Subject)
	}
	return nil
}

// Sent returns what was sent, oldest first.
func (o *Outbox) Sent() []Message {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]Message(nil), o.out...)
}
