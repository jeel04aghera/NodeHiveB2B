//go:build integration

// Phase 2 session-management tests: login creates a session, refresh rotates the token,
// a replayed (rotated-away) token is rejected, sessions can be revoked individually and
// en masse, ownership is isolated across users, Google login opens a session, and
// device info is captured from the User-Agent across multiple devices.
package auth

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/nodehive/gpu-platform/internal/identity"
)

const chromeUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36"
const firefoxUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0"

func TestRefreshRotationAndReplay(t *testing.T) {
	svc, _, ctx := newSvc(t)
	_, u, err := svc.Register(ctx, "Acme", "a@acme.com", "A", "password123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	raw1, sess, err := svc.CreateSession(ctx, u.ID, identity.DeviceInfo{UserAgent: chromeUA, IPAddress: "1.2.3.4"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if sess.Browser != "Chrome" || sess.OS != "macOS" {
		t.Errorf("device parse: got browser=%q os=%q", sess.Browser, sess.OS)
	}

	// Refresh rotates: a fresh access token + a NEW refresh token, same session id.
	access, raw2, _, sess2, err := svc.RefreshSession(ctx, raw1, identity.DeviceInfo{UserAgent: chromeUA})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if access == "" || raw2 == "" || raw2 == raw1 {
		t.Fatalf("expected rotated tokens; access=%q raw2==raw1? %v", access, raw2 == raw1)
	}
	if sess2.ID != sess.ID {
		t.Errorf("rotation should keep the same session id: got %v want %v", sess2.ID, sess.ID)
	}

	// REPLAY: the old refresh token must be rejected (it was rotated away).
	if _, _, _, _, err := svc.RefreshSession(ctx, raw1, identity.DeviceInfo{UserAgent: chromeUA}); !errors.Is(err, identity.ErrSessionInvalid) {
		t.Fatalf("replay of rotated token must fail with ErrSessionInvalid, got %v", err)
	}

	// The new token still works (replay rejection didn't kill the live session).
	if _, _, _, _, err := svc.RefreshSession(ctx, raw2, identity.DeviceInfo{UserAgent: chromeUA}); err != nil {
		t.Fatalf("current token should still refresh: %v", err)
	}
}

func TestRevokeSessionLogsOut(t *testing.T) {
	svc, _, ctx := newSvc(t)
	_, u, _ := svc.Register(ctx, "Beta", "b@beta.com", "B", "password123")

	raw, sess, _ := svc.CreateSession(ctx, u.ID, identity.DeviceInfo{UserAgent: chromeUA})
	if err := svc.RevokeSession(ctx, u.ID, sess.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// A revoked session can no longer refresh.
	if _, _, _, _, err := svc.RefreshSession(ctx, raw, identity.DeviceInfo{}); !errors.Is(err, identity.ErrSessionInvalid) {
		t.Fatalf("revoked session must not refresh, got %v", err)
	}
	// And it disappears from the active list.
	list, _ := svc.ListSessions(ctx, u.ID)
	if len(list) != 0 {
		t.Errorf("expected 0 active sessions after revoke, got %d", len(list))
	}
}

func TestRevokeAllExceptCurrent(t *testing.T) {
	svc, _, ctx := newSvc(t)
	_, u, _ := svc.Register(ctx, "Gamma", "g@gamma.com", "G", "password123")

	_, keep, _ := svc.CreateSession(ctx, u.ID, identity.DeviceInfo{UserAgent: chromeUA})
	_, _, _ = svc.CreateSession(ctx, u.ID, identity.DeviceInfo{UserAgent: firefoxUA})
	_, _, _ = svc.CreateSession(ctx, u.ID, identity.DeviceInfo{UserAgent: firefoxUA})

	if err := svc.RevokeAllSessions(ctx, u.ID, &keep.ID); err != nil {
		t.Fatalf("revoke-all: %v", err)
	}
	list, _ := svc.ListSessions(ctx, u.ID)
	if len(list) != 1 || list[0].ID != keep.ID {
		t.Fatalf("revoke-all-except should leave exactly the kept session, got %d", len(list))
	}

	// With no exception, everything is revoked.
	if err := svc.RevokeAllSessions(ctx, u.ID, nil); err != nil {
		t.Fatalf("revoke-all nil: %v", err)
	}
	if list, _ := svc.ListSessions(ctx, u.ID); len(list) != 0 {
		t.Errorf("expected 0 sessions after full revoke-all, got %d", len(list))
	}
}

func TestSessionOwnershipIsolation(t *testing.T) {
	svc, _, ctx := newSvc(t)
	_, alice, _ := svc.Register(ctx, "AOrg", "alice@x.com", "Alice", "password123")
	_, bob, _ := svc.Register(ctx, "BOrg", "bob@x.com", "Bob", "password123")

	_, aliceSess, _ := svc.CreateSession(ctx, alice.ID, identity.DeviceInfo{UserAgent: chromeUA})

	// Bob cannot revoke Alice's session.
	if err := svc.RevokeSession(ctx, bob.ID, aliceSess.ID); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("cross-user revoke must return ErrNotFound, got %v", err)
	}
	// Alice's session is untouched.
	if list, _ := svc.ListSessions(ctx, alice.ID); len(list) != 1 {
		t.Errorf("Alice's session should survive Bob's revoke attempt, got %d", len(list))
	}
}

func TestMultiDeviceSessions(t *testing.T) {
	svc, _, ctx := newSvc(t)
	_, u, _ := svc.Register(ctx, "Multi", "m@multi.com", "M", "password123")

	_, _, _ = svc.CreateSession(ctx, u.ID, identity.DeviceInfo{UserAgent: chromeUA})
	_, _, _ = svc.CreateSession(ctx, u.ID, identity.DeviceInfo{UserAgent: firefoxUA})

	list, _ := svc.ListSessions(ctx, u.ID)
	if len(list) != 2 {
		t.Fatalf("expected 2 device sessions, got %d", len(list))
	}
	seen := map[string]bool{}
	for _, s := range list {
		seen[s.Browser+"/"+s.OS] = true
	}
	if !seen["Chrome/macOS"] || !seen["Firefox/Windows"] {
		t.Errorf("expected Chrome/macOS and Firefox/Windows devices, got %v", seen)
	}
}

func TestGoogleLoginCreatesSession(t *testing.T) {
	svc, _, ctx := newSvc(t)
	// Pre-onboarding Google user (org_id nil) can still open a session.
	_, gu, err := svc.UpsertGoogleUser(ctx, "sub-sess", "gsess@x.com", "G Sess", "", true)
	if err != nil {
		t.Fatalf("google upsert: %v", err)
	}
	if gu.Onboarded() {
		t.Fatal("precondition: google user should be pre-onboarding")
	}
	raw, sess, err := svc.CreateSession(ctx, gu.ID, identity.DeviceInfo{UserAgent: chromeUA})
	if err != nil {
		t.Fatalf("create session for google user: %v", err)
	}
	if sess.UserID != gu.ID {
		t.Errorf("session user mismatch")
	}
	// And it can refresh.
	if _, _, user, _, err := svc.RefreshSession(ctx, raw, identity.DeviceInfo{UserAgent: chromeUA}); err != nil || user.ID != gu.ID {
		t.Fatalf("google session refresh failed: %v", err)
	}
	_ = uuid.Nil
}

func TestSessionIDByRefresh(t *testing.T) {
	svc, _, ctx := newSvc(t)
	_, u, _ := svc.Register(ctx, "Curr", "c@curr.com", "C", "password123")
	raw, sess, _ := svc.CreateSession(ctx, u.ID, identity.DeviceInfo{UserAgent: chromeUA})

	id, err := svc.SessionIDByRefresh(ctx, raw)
	if err != nil || id != sess.ID {
		t.Fatalf("SessionIDByRefresh: got %v err=%v want %v", id, err, sess.ID)
	}
	// Unknown token resolves to ErrNotFound.
	if _, err := svc.SessionIDByRefresh(ctx, "bogus"); !errors.Is(err, identity.ErrNotFound) {
		t.Errorf("unknown refresh should be ErrNotFound, got %v", err)
	}
}
