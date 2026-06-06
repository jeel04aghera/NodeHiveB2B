package email

import (
	"context"
	"strings"
	"testing"
)

func TestBuildInviteEmailContainsOrgAndURL(t *testing.T) {
	m := BuildInviteEmail("teammate@acme.com", "Acme AI", "admin", "https://app.example.com/invite?token=abc123")
	if m.To != "teammate@acme.com" {
		t.Errorf("to = %q", m.To)
	}
	for _, want := range []string{"Acme AI"} {
		if !strings.Contains(m.Subject, want) {
			t.Errorf("subject %q missing %q", m.Subject, want)
		}
	}
	// Org name, role and the accept URL must appear in both the text and HTML bodies.
	for _, body := range []string{m.Text, m.HTML} {
		for _, want := range []string{"Acme AI", "admin", "https://app.example.com/invite?token=abc123"} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q:\n%s", want, body)
			}
		}
	}
}

func TestBuildInviteEmailEscapesHTML(t *testing.T) {
	m := BuildInviteEmail("x@x.com", `Ev<il>&"Co`, "member", "https://h/invite?token=t")
	if strings.Contains(m.HTML, "<il>") {
		t.Errorf("org name not HTML-escaped in: %s", m.HTML)
	}
}

func TestNewSenderSelectsProvider(t *testing.T) {
	if NewSender("", "", nil).Enabled() {
		t.Error("no creds => console sender, Enabled() must be false")
	}
	if !NewSender("re_key", "noreply@x.com", nil).Enabled() {
		t.Error("with creds => Resend sender, Enabled() must be true")
	}
	// Console sender never errors (it just logs).
	if err := (&ConsoleSender{}).Send(context.Background(), Message{To: "a@b.com"}); err != nil {
		t.Errorf("console send: %v", err)
	}
}
