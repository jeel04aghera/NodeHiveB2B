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

// APIKeyScheme is the bearer prefix that routes a token down the API-key
// authentication path instead of JWT parsing.
const APIKeyScheme = "nhk_"

// ErrAPIKeyInvalid is returned for unknown, revoked, expired or orphaned keys.
// One error for every failure mode so a probing caller learns nothing.
var ErrAPIKeyInvalid = errors.New("invalid API key")

// ErrServiceAccountExists is returned when an org already has an SA by that name.
var ErrServiceAccountExists = errors.New("a service account with that name already exists")

// APIKeyRequest describes a key to mint. Exactly one of OwnerUserID /
// ServiceAccountID must be set (personal key vs machine key).
type APIKeyRequest struct {
	Name             string
	OwnerUserID      *uuid.UUID
	ServiceAccountID *uuid.UUID
	CreatedBy        uuid.UUID
	TTL              time.Duration // 0 = never expires
}

// ── Service accounts ──────────────────────────────────────────────────────────

func (s *ServiceImpl) CreateServiceAccount(ctx context.Context, orgID, createdBy uuid.UUID, name, description string, role domain.Role) (domain.ServiceAccount, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.ServiceAccount{}, fmt.Errorf("service account name is required")
	}
	// Machine identities are capped at admin: an owner role carries org-lifecycle
	// powers (ownership transfer, deletion) that must stay with a human.
	if role != domain.RoleAdmin {
		role = domain.RoleMember
	}
	var sa domain.ServiceAccount
	err := s.db.QueryRow(ctx,
		`INSERT INTO service_accounts (org_id, name, description, role, created_by)
		 VALUES ($1,$2,$3,$4,$5)
		 RETURNING id, org_id, name, description, role, created_by, created_at, disabled_at`,
		orgID, name, strings.TrimSpace(description), role, createdBy).
		Scan(&sa.ID, &sa.OrgID, &sa.Name, &sa.Description, &sa.Role, &sa.CreatedBy, &sa.CreatedAt, &sa.DisabledAt)
	if err != nil && strings.Contains(err.Error(), "duplicate key") {
		return domain.ServiceAccount{}, ErrServiceAccountExists
	}
	return sa, err
}

func (s *ServiceImpl) ListServiceAccounts(ctx context.Context, orgID uuid.UUID) ([]domain.ServiceAccount, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, org_id, name, description, role, created_by, created_at, disabled_at
		   FROM service_accounts WHERE org_id=$1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ServiceAccount, 0)
	for rows.Next() {
		var sa domain.ServiceAccount
		if err := rows.Scan(&sa.ID, &sa.OrgID, &sa.Name, &sa.Description, &sa.Role,
			&sa.CreatedBy, &sa.CreatedAt, &sa.DisabledAt); err != nil {
			return nil, err
		}
		out = append(out, sa)
	}
	return out, rows.Err()
}

// SetServiceAccountDisabled toggles an SA. Disabling immediately invalidates every
// API key it owns (the key check joins the SA row).
func (s *ServiceImpl) SetServiceAccountDisabled(ctx context.Context, orgID, id uuid.UUID, disabled bool) error {
	q := `UPDATE service_accounts SET disabled_at = now() WHERE id=$1 AND org_id=$2 AND disabled_at IS NULL`
	if !disabled {
		q = `UPDATE service_accounts SET disabled_at = NULL WHERE id=$1 AND org_id=$2`
	}
	ct, err := s.db.Exec(ctx, q, id, orgID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── API keys ──────────────────────────────────────────────────────────────────

// CreateAPIKey mints a key and returns the RAW value exactly once. Only the SHA-256
// is stored; the prefix (nhk_ + first 8 chars) is kept for display.
func (s *ServiceImpl) CreateAPIKey(ctx context.Context, orgID uuid.UUID, req APIKeyRequest) (string, domain.APIKey, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return "", domain.APIKey{}, fmt.Errorf("key name is required")
	}
	if (req.OwnerUserID == nil) == (req.ServiceAccountID == nil) {
		return "", domain.APIKey{}, fmt.Errorf("exactly one of user or service account must own the key")
	}
	raw := APIKeyScheme + randomToken()
	prefix := raw[:len(APIKeyScheme)+8]
	var expires *time.Time
	if req.TTL > 0 {
		t := time.Now().Add(req.TTL)
		expires = &t
	}
	var k domain.APIKey
	err := s.db.QueryRow(ctx,
		`INSERT INTO api_keys (org_id, name, prefix, key_hash, user_id, service_account_id, created_by, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 RETURNING id, org_id, name, prefix, user_id, service_account_id, created_by, created_at, expires_at, last_used_at, revoked_at`,
		orgID, req.Name, prefix, hashToken(raw), req.OwnerUserID, req.ServiceAccountID, req.CreatedBy, expires).
		Scan(&k.ID, &k.OrgID, &k.Name, &k.Prefix, &k.UserID, &k.ServiceAccountID,
			&k.CreatedBy, &k.CreatedAt, &k.ExpiresAt, &k.LastUsedAt, &k.RevokedAt)
	if err != nil {
		return "", domain.APIKey{}, fmt.Errorf("create api key: %w", err)
	}
	k.Status = apiKeyStatus(k)
	return raw, k, nil
}

// ListAPIKeys returns an org's keys; ownerUserID narrows to one user's personal keys
// (the member view — admins pass nil to see everything).
func (s *ServiceImpl) ListAPIKeys(ctx context.Context, orgID uuid.UUID, ownerUserID *uuid.UUID) ([]domain.APIKey, error) {
	q := `SELECT id, org_id, name, prefix, user_id, service_account_id, created_by, created_at, expires_at, last_used_at, revoked_at
	        FROM api_keys WHERE org_id=$1`
	args := []any{orgID}
	if ownerUserID != nil {
		q += ` AND user_id=$2`
		args = append(args, *ownerUserID)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.APIKey, 0)
	for rows.Next() {
		var k domain.APIKey
		if err := rows.Scan(&k.ID, &k.OrgID, &k.Name, &k.Prefix, &k.UserID, &k.ServiceAccountID,
			&k.CreatedBy, &k.CreatedAt, &k.ExpiresAt, &k.LastUsedAt, &k.RevokedAt); err != nil {
			return nil, err
		}
		k.Status = apiKeyStatus(k)
		out = append(out, k)
	}
	return out, rows.Err()
}

// RevokeAPIKey tombstones a key (kept for audit/forensics). ownerUserID, when set,
// restricts the revoke to that user's own keys (member self-service); nil = admin.
func (s *ServiceImpl) RevokeAPIKey(ctx context.Context, orgID, keyID uuid.UUID, ownerUserID *uuid.UUID) error {
	q := `UPDATE api_keys SET revoked_at = now() WHERE id=$1 AND org_id=$2 AND revoked_at IS NULL`
	args := []any{keyID, orgID}
	if ownerUserID != nil {
		q += ` AND user_id=$3`
		args = append(args, *ownerUserID)
	}
	ct, err := s.db.Exec(ctx, q, args...)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AuthenticateAPIKey resolves a raw "nhk_…" bearer to a principal. Personal keys act
// as their owner with the owner's CURRENT role (a demoted/removed user's keys lose
// power instantly); service-account keys synthesize an API-only principal. Every
// failure mode returns the same ErrAPIKeyInvalid.
func (s *ServiceImpl) AuthenticateAPIKey(ctx context.Context, raw string) (domain.User, error) {
	if !strings.HasPrefix(raw, APIKeyScheme) {
		return domain.User{}, ErrAPIKeyInvalid
	}
	var (
		keyID, orgID uuid.UUID
		userID, saID *uuid.UUID
		expiresAt    *time.Time
		revokedAt    *time.Time
	)
	err := s.db.QueryRow(ctx,
		`SELECT id, org_id, user_id, service_account_id, expires_at, revoked_at
		   FROM api_keys WHERE key_hash=$1`, hashToken(raw)).
		Scan(&keyID, &orgID, &userID, &saID, &expiresAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrAPIKeyInvalid
	}
	if err != nil {
		return domain.User{}, err
	}
	if revokedAt != nil || (expiresAt != nil && expiresAt.Before(time.Now())) {
		return domain.User{}, ErrAPIKeyInvalid
	}

	var principal domain.User
	switch {
	case userID != nil:
		var u domain.User
		var userOrg *uuid.UUID
		err = s.db.QueryRow(ctx,
			`SELECT id, org_id, department_id, email, name, role, status, email_verified
			   FROM users WHERE id=$1 AND status='active'`, *userID).
			Scan(&u.ID, &userOrg, &u.DepartmentID, &u.Email, &u.Name, &u.Role, &u.Status, &u.EmailVerified)
		if err != nil {
			return domain.User{}, ErrAPIKeyInvalid
		}
		// The owner must still belong to the org the key was minted in — keys of a
		// user who left (or re-homed) the org die with the membership.
		if userOrg == nil || *userOrg != orgID {
			return domain.User{}, ErrAPIKeyInvalid
		}
		u.OrgID = orgID
		principal = u
	case saID != nil:
		var sa domain.ServiceAccount
		err = s.db.QueryRow(ctx,
			`SELECT id, org_id, name, role, disabled_at FROM service_accounts WHERE id=$1`, *saID).
			Scan(&sa.ID, &sa.OrgID, &sa.Name, &sa.Role, &sa.DisabledAt)
		if err != nil || sa.DisabledAt != nil || sa.OrgID != orgID {
			return domain.User{}, ErrAPIKeyInvalid
		}
		principal = domain.User{
			ID: sa.ID, OrgID: sa.OrgID, Name: sa.Name,
			Role: sa.Role.Normalize(), Status: "active",
			AuthProvider: "api_key", IsServiceAccount: true,
		}
	default:
		return domain.User{}, ErrAPIKeyInvalid
	}

	// last_used_at is forensic, not transactional: throttle to one write/minute per
	// key so high-rate API clients don't turn every request into an UPDATE.
	_, _ = s.db.Exec(ctx,
		`UPDATE api_keys SET last_used_at = now()
		  WHERE id=$1 AND (last_used_at IS NULL OR last_used_at < now() - interval '60 seconds')`, keyID)
	return principal, nil
}

func apiKeyStatus(k domain.APIKey) string {
	switch {
	case k.RevokedAt != nil:
		return "revoked"
	case k.ExpiresAt != nil && k.ExpiresAt.Before(time.Now()):
		return "expired"
	default:
		return "active"
	}
}
