package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nodehive/gpu-platform/internal/domain"
)

// ErrSessionInvalid is returned when a refresh token is unknown, expired, revoked, or
// has already been rotated away (replay). Callers should treat it as "re-authenticate".
var ErrSessionInvalid = errors.New("invalid or expired session")

// IssueAccessToken mints a fresh short-lived access token for a user (no session row).
func (s *ServiceImpl) IssueAccessToken(_ context.Context, u domain.User) (string, error) {
	return s.issueJWT(u)
}

// CreateSession opens a refresh-token session and returns the RAW refresh token. Only
// the SHA-256 hash is persisted (same discipline as enrollment tokens) — the raw value
// lives solely in the caller's HttpOnly cookie.
func (s *ServiceImpl) CreateSession(ctx context.Context, userID uuid.UUID, dev DeviceInfo) (string, domain.Session, error) {
	raw := randomToken()
	hash := hashToken(raw)
	expires := time.Now().Add(s.refreshTokenTTL())
	dev = dev.normalized()

	var sess domain.Session
	err := s.db.QueryRow(ctx,
		`INSERT INTO user_sessions
		   (user_id, refresh_token_hash, device_name, browser, os, ip_address, user_agent, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 RETURNING id, user_id, device_name, browser, os, ip_address, user_agent,
		           created_at, last_active_at, expires_at, revoked_at`,
		userID, hash, dev.DeviceName, dev.Browser, dev.OS, dev.IPAddress, dev.UserAgent, expires).
		Scan(&sess.ID, &sess.UserID, &sess.DeviceName, &sess.Browser, &sess.OS, &sess.IPAddress,
			&sess.UserAgent, &sess.CreatedAt, &sess.LastActiveAt, &sess.ExpiresAt, &sess.RevokedAt)
	if err != nil {
		return "", domain.Session{}, fmt.Errorf("create session: %w", err)
	}
	return raw, sess, nil
}

// RefreshSession validates a raw refresh token and rotates it in place: a new random
// refresh token replaces the stored hash (the presented token is immediately invalidated)
// and a fresh access token is minted. Rotation, last-activity tracking and device info
// are all updated atomically. A token that matches no ACTIVE session (unknown, expired,
// revoked, or already rotated) is rejected with ErrSessionInvalid.
func (s *ServiceImpl) RefreshSession(ctx context.Context, rawRefresh string, dev DeviceInfo) (string, string, domain.User, domain.Session, error) {
	if strings.TrimSpace(rawRefresh) == "" {
		return "", "", domain.User{}, domain.Session{}, ErrSessionInvalid
	}
	dev = dev.normalized()
	newRaw := randomToken()

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", "", domain.User{}, domain.Session{}, err
	}
	defer tx.Rollback(ctx)

	// Atomically rotate: only an active (non-revoked, unexpired) row matching the
	// presented hash is updated. The UPDATE ... RETURNING makes the swap a single
	// statement so two concurrent refreshes can't both succeed on the same token.
	var sess domain.Session
	err = tx.QueryRow(ctx,
		`UPDATE user_sessions
		    SET refresh_token_hash = $2,
		        last_active_at     = now(),
		        ip_address         = $3,
		        user_agent         = $4
		  WHERE refresh_token_hash = $1
		    AND revoked_at IS NULL
		    AND expires_at > now()
		RETURNING id, user_id, device_name, browser, os, ip_address, user_agent,
		          created_at, last_active_at, expires_at, revoked_at`,
		hashToken(rawRefresh), hashToken(newRaw), dev.IPAddress, dev.UserAgent).
		Scan(&sess.ID, &sess.UserID, &sess.DeviceName, &sess.Browser, &sess.OS, &sess.IPAddress,
			&sess.UserAgent, &sess.CreatedAt, &sess.LastActiveAt, &sess.ExpiresAt, &sess.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", domain.User{}, domain.Session{}, ErrSessionInvalid
	}
	if err != nil {
		return "", "", domain.User{}, domain.Session{}, fmt.Errorf("rotate session: %w", err)
	}

	user, err := s.userByID(ctx, tx, sess.UserID)
	if err != nil {
		// User disappeared or was disabled since the session was created.
		return "", "", domain.User{}, domain.Session{}, ErrSessionInvalid
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", domain.User{}, domain.Session{}, err
	}

	access, err := s.issueJWT(user)
	if err != nil {
		return "", "", domain.User{}, domain.Session{}, err
	}
	return access, newRaw, user, sess, nil
}

// ListSessions returns a user's active sessions, newest activity first.
func (s *ServiceImpl) ListSessions(ctx context.Context, userID uuid.UUID) ([]domain.Session, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, user_id, device_name, browser, os, ip_address, user_agent,
		        created_at, last_active_at, expires_at, revoked_at
		   FROM user_sessions
		  WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()
		  ORDER BY last_active_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Session{}
	for rows.Next() {
		var sess domain.Session
		if err := rows.Scan(&sess.ID, &sess.UserID, &sess.DeviceName, &sess.Browser, &sess.OS,
			&sess.IPAddress, &sess.UserAgent, &sess.CreatedAt, &sess.LastActiveAt,
			&sess.ExpiresAt, &sess.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// RevokeSession revokes one session, scoped to its owner. A user can only revoke their
// own sessions — a mismatched user_id affects zero rows and returns ErrNotFound.
func (s *ServiceImpl) RevokeSession(ctx context.Context, userID, sessionID uuid.UUID) error {
	ct, err := s.db.Exec(ctx,
		`UPDATE user_sessions SET revoked_at = now()
		   WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, sessionID, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeAllSessions revokes every active session for a user. When exceptSessionID is
// non-nil that session is kept active (so "log out everywhere else" leaves the caller in).
func (s *ServiceImpl) RevokeAllSessions(ctx context.Context, userID uuid.UUID, exceptSessionID *uuid.UUID) error {
	if exceptSessionID != nil {
		_, err := s.db.Exec(ctx,
			`UPDATE user_sessions SET revoked_at = now()
			   WHERE user_id = $1 AND revoked_at IS NULL AND id <> $2`, userID, *exceptSessionID)
		return err
	}
	_, err := s.db.Exec(ctx,
		`UPDATE user_sessions SET revoked_at = now()
		   WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}

// RevokeSessionByRefresh revokes the session a raw refresh token belongs to (logout).
// Unknown/already-revoked tokens are a no-op (idempotent).
func (s *ServiceImpl) RevokeSessionByRefresh(ctx context.Context, rawRefresh string) error {
	if strings.TrimSpace(rawRefresh) == "" {
		return nil
	}
	_, err := s.db.Exec(ctx,
		`UPDATE user_sessions SET revoked_at = now()
		   WHERE refresh_token_hash = $1 AND revoked_at IS NULL`, hashToken(rawRefresh))
	return err
}

// SessionIDByRefresh resolves the active session id for a raw refresh token, used to mark
// the "current" session in the list. Returns ErrNotFound when the token matches none.
func (s *ServiceImpl) SessionIDByRefresh(ctx context.Context, rawRefresh string) (uuid.UUID, error) {
	if strings.TrimSpace(rawRefresh) == "" {
		return uuid.Nil, ErrNotFound
	}
	var id uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT id FROM user_sessions
		  WHERE refresh_token_hash = $1 AND revoked_at IS NULL AND expires_at > now()`,
		hashToken(rawRefresh)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

// userByID loads an active user within a transaction (used by RefreshSession so a
// disabled/deleted user can't refresh). Mirrors Authenticate's projection.
func (s *ServiceImpl) userByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.User, error) {
	var u domain.User
	var orgID *uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT id, org_id, department_id, email, name, role, status, avatar_url, email_verified, auth_provider
		   FROM users WHERE id = $1 AND status = 'active'`, id).
		Scan(&u.ID, &orgID, &u.DepartmentID, &u.Email, &u.Name, &u.Role, &u.Status,
			&u.AvatarURL, &u.EmailVerified, &u.AuthProvider)
	if err != nil {
		return domain.User{}, err
	}
	if orgID != nil {
		u.OrgID = *orgID
	}
	return u, nil
}

// normalized fills DeviceName/Browser/OS from the User-Agent when not already set, and
// trims oversized fields so a hostile UA can't bloat the row.
func (d DeviceInfo) normalized() DeviceInfo {
	if d.Browser == "" || d.OS == "" || d.DeviceName == "" {
		b, os, name := parseUserAgent(d.UserAgent)
		if d.Browser == "" {
			d.Browser = b
		}
		if d.OS == "" {
			d.OS = os
		}
		if d.DeviceName == "" {
			d.DeviceName = name
		}
	}
	d.UserAgent = clip(d.UserAgent, 512)
	d.DeviceName = clip(d.DeviceName, 120)
	d.Browser = clip(d.Browser, 60)
	d.OS = clip(d.OS, 60)
	d.IPAddress = clip(d.IPAddress, 64)
	return d
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// parseUserAgent is a tiny, dependency-free UA classifier — enough to label a device
// in the sessions UI (browser / OS / a friendly device name). Not exhaustive.
func parseUserAgent(ua string) (browser, os, device string) {
	l := strings.ToLower(ua)
	switch {
	case strings.Contains(l, "edg/"):
		browser = "Edge"
	case strings.Contains(l, "opr/") || strings.Contains(l, "opera"):
		browser = "Opera"
	case strings.Contains(l, "chrome/") && !strings.Contains(l, "chromium"):
		browser = "Chrome"
	case strings.Contains(l, "chromium"):
		browser = "Chromium"
	case strings.Contains(l, "firefox/"):
		browser = "Firefox"
	case strings.Contains(l, "safari/") && strings.Contains(l, "version/"):
		browser = "Safari"
	case strings.Contains(l, "curl/"):
		browser = "curl"
	case ua == "":
		browser = "Unknown"
	default:
		browser = "Unknown"
	}
	switch {
	case strings.Contains(l, "windows"):
		os = "Windows"
	case strings.Contains(l, "iphone"):
		os = "iOS"
	case strings.Contains(l, "ipad"):
		os = "iPadOS"
	case strings.Contains(l, "mac os x") || strings.Contains(l, "macintosh"):
		os = "macOS"
	case strings.Contains(l, "android"):
		os = "Android"
	case strings.Contains(l, "linux"):
		os = "Linux"
	default:
		os = "Unknown"
	}
	switch {
	case os == "Unknown" && browser == "Unknown":
		device = "Unknown device"
	case browser == "Unknown":
		device = os
	default:
		device = browser + " on " + os
	}
	return browser, os, device
}
