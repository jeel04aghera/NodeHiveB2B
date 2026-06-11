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

// ErrAuthTokenInvalid covers unknown, expired and already-used verify/reset tokens —
// one error so a guessing caller can't distinguish the cases.
var ErrAuthTokenInvalid = errors.New("invalid or expired token")

// ErrAlreadyVerified is returned when requesting verification for a verified email.
var ErrAlreadyVerified = errors.New("email is already verified")

const (
	verifyTokenTTL = 24 * time.Hour
	resetTokenTTL  = time.Hour
)

// mintAuthToken stores a hashed single-use token, invalidating any earlier
// outstanding tokens of the same kind (the newest link is the only valid one).
func (s *ServiceImpl) mintAuthToken(ctx context.Context, userID uuid.UUID, kind string, ttl time.Duration) (string, error) {
	raw := randomToken()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`UPDATE auth_tokens SET used_at = now() WHERE user_id=$1 AND kind=$2 AND used_at IS NULL`,
		userID, kind); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO auth_tokens (user_id, kind, token_hash, expires_at) VALUES ($1,$2,$3,$4)`,
		userID, kind, hashToken(raw), time.Now().Add(ttl)); err != nil {
		return "", err
	}
	return raw, tx.Commit(ctx)
}

// consumeAuthToken atomically claims a token (single use, unexpired) and returns its user.
func (s *ServiceImpl) consumeAuthToken(ctx context.Context, raw, kind string) (uuid.UUID, error) {
	var userID uuid.UUID
	err := s.db.QueryRow(ctx,
		`UPDATE auth_tokens SET used_at = now()
		  WHERE token_hash=$1 AND kind=$2 AND used_at IS NULL AND expires_at > now()
		  RETURNING user_id`, hashToken(raw), kind).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrAuthTokenInvalid
	}
	return userID, err
}

func (s *ServiceImpl) userSnapshot(ctx context.Context, id uuid.UUID) (domain.User, error) {
	var u domain.User
	var orgID *uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT id, org_id, email, name, role, status, email_verified, auth_provider
		   FROM users WHERE id=$1 AND status='active'`, id).
		Scan(&u.ID, &orgID, &u.Email, &u.Name, &u.Role, &u.Status, &u.EmailVerified, &u.AuthProvider)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrNotFound
	}
	if orgID != nil {
		u.OrgID = *orgID
	}
	return u, err
}

// RequestEmailVerification mints a verify token for an unverified account.
func (s *ServiceImpl) RequestEmailVerification(ctx context.Context, userID uuid.UUID) (string, domain.User, error) {
	u, err := s.userSnapshot(ctx, userID)
	if err != nil {
		return "", domain.User{}, err
	}
	if u.EmailVerified {
		return "", domain.User{}, ErrAlreadyVerified
	}
	raw, err := s.mintAuthToken(ctx, u.ID, "verify_email", verifyTokenTTL)
	return raw, u, err
}

// ConfirmEmailVerification consumes a verify token and flips email_verified.
func (s *ServiceImpl) ConfirmEmailVerification(ctx context.Context, raw string) (domain.User, error) {
	userID, err := s.consumeAuthToken(ctx, raw, "verify_email")
	if err != nil {
		return domain.User{}, err
	}
	if _, err := s.db.Exec(ctx,
		`UPDATE users SET email_verified = true WHERE id=$1`, userID); err != nil {
		return domain.User{}, err
	}
	return s.userSnapshot(ctx, userID)
}

// RequestPasswordReset mints a reset token for the account with that email.
// Unknown email → ErrNotFound; the HTTP layer responds identically either way.
func (s *ServiceImpl) RequestPasswordReset(ctx context.Context, email string) (string, domain.User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	var userID uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT id FROM users WHERE email=$1 AND status='active' LIMIT 1`, email).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.User{}, ErrNotFound
	}
	if err != nil {
		return "", domain.User{}, err
	}
	u, err := s.userSnapshot(ctx, userID)
	if err != nil {
		return "", domain.User{}, err
	}
	raw, err := s.mintAuthToken(ctx, userID, "password_reset", resetTokenTTL)
	return raw, u, err
}

// ConfirmPasswordReset consumes a reset token, sets the new password, and revokes
// every session — anyone holding the old credential or a session is logged out.
// Completing a reset also proves control of the mailbox, so the email is marked
// verified as a side effect (same evidence as the verify flow).
func (s *ServiceImpl) ConfirmPasswordReset(ctx context.Context, raw, newPassword string) (domain.User, error) {
	if len(newPassword) < 8 {
		return domain.User{}, fmt.Errorf("password must be at least 8 characters")
	}
	userID, err := s.consumeAuthToken(ctx, raw, "password_reset")
	if err != nil {
		return domain.User{}, err
	}
	hash, err := bcryptHash(newPassword)
	if err != nil {
		return domain.User{}, err
	}
	if _, err := s.db.Exec(ctx,
		`UPDATE users SET password_hash=$2, email_verified=true WHERE id=$1`, userID, hash); err != nil {
		return domain.User{}, err
	}
	if err := s.RevokeAllSessions(ctx, userID, nil); err != nil {
		return domain.User{}, err
	}
	return s.userSnapshot(ctx, userID)
}
