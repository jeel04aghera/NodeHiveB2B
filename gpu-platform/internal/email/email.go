// Package email sends transactional email (currently just organization invitations) via
// Resend, with a console fallback for development. The Sender is intentionally tiny so the
// HTTP layer can fire-and-forget without coupling to a provider.
package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"time"
)

// Message is a single outbound email.
type Message struct {
	To      string
	Subject string
	HTML    string
	Text    string
}

// Sender delivers a Message. Enabled() reports whether a real provider is configured;
// when false, callers should keep surfacing the raw invite URL (dev behavior).
type Sender interface {
	Send(ctx context.Context, m Message) error
	Enabled() bool
}

// NewSender returns a ResendSender when an API key + from-address are configured, otherwise
// a ConsoleSender that logs the email (dev / provider-disabled).
func NewSender(apiKey, from string, log *slog.Logger) Sender {
	if apiKey != "" && from != "" {
		return &ResendSender{apiKey: apiKey, from: from, client: &http.Client{Timeout: 15 * time.Second}}
	}
	return &ConsoleSender{log: log}
}

// ── Console (dev / disabled) ─────────────────────────────────────────────────

type ConsoleSender struct{ log *slog.Logger }

func (c *ConsoleSender) Enabled() bool { return false }
func (c *ConsoleSender) Send(_ context.Context, m Message) error {
	if c.log != nil {
		c.log.Info("email (provider disabled — not sent)", "to", m.To, "subject", m.Subject, "text", m.Text)
	}
	return nil
}

// ── Resend (production) ──────────────────────────────────────────────────────

type ResendSender struct {
	apiKey string
	from   string
	client *http.Client
}

func (r *ResendSender) Enabled() bool { return true }

func (r *ResendSender) Send(ctx context.Context, m Message) error {
	body, _ := json.Marshal(map[string]any{
		"from": r.from, "to": []string{m.To}, "subject": m.Subject, "html": m.HTML, "text": m.Text,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("resend: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		return fmt.Errorf("resend: status %d: %s", resp.StatusCode, buf.String())
	}
	return nil
}

// BuildInviteEmail renders the invitation email (org name + role + accept URL). Pure
// function so it can be unit-tested without a provider.
func BuildInviteEmail(to, orgName, role, acceptURL string) Message {
	safeOrg := html.EscapeString(orgName)
	safeRole := html.EscapeString(role)
	safeURL := html.EscapeString(acceptURL)
	subject := fmt.Sprintf("You're invited to join %s on NodeHive", orgName)
	text := fmt.Sprintf(
		"You've been invited to join %s on NodeHive as %s.\n\nAccept your invitation:\n%s\n\nIf you weren't expecting this, you can ignore this email.",
		orgName, role, acceptURL)
	htmlBody := fmt.Sprintf(`<div style="font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;max-width:480px;margin:0 auto;color:#1a1a1a">
  <h2 style="font-weight:600">Join %s on NodeHive</h2>
  <p>You've been invited to join <strong>%s</strong> as <strong>%s</strong>.</p>
  <p style="margin:24px 0">
    <a href="%s" style="background:#1a1a1a;color:#fff;padding:10px 18px;border-radius:6px;text-decoration:none;display:inline-block">Accept invitation</a>
  </p>
  <p style="color:#666;font-size:13px">Or paste this link into your browser:<br><a href="%s">%s</a></p>
  <p style="color:#999;font-size:12px;margin-top:24px">If you weren't expecting this, you can safely ignore this email.</p>
</div>`, safeOrg, safeOrg, safeRole, safeURL, safeURL, safeURL)
	return Message{To: to, Subject: subject, HTML: htmlBody, Text: text}
}
