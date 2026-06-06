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

var (
	ErrAlreadyMember     = errors.New("user already belongs to an organization")
	ErrInvitationInvalid = errors.New("invitation is invalid, expired, or already used")
	ErrJoinCodeInvalid   = errors.New("join code is invalid, expired, revoked, or exhausted")
	ErrForbidden         = errors.New("not permitted")
	ErrLastOwner         = errors.New("organization must have at least one owner")
)

const invitationTTL = 7 * 24 * time.Hour

// RegisterPending creates a password account with NO organization, used by invitees who
// need an account before accepting an invite / join code (existing Register always makes
// a new org, which would conflict with single-active-org). Mirrors Register's validation.
func (s *ServiceImpl) RegisterPending(ctx context.Context, email, name, password string) (string, domain.User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || password == "" {
		return "", domain.User{}, errors.New("email and password are required")
	}
	if len(password) < 8 {
		return "", domain.User{}, errors.New("password must be at least 8 characters")
	}
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT exists(SELECT 1 FROM users WHERE email=$1)`, email).Scan(&exists); err != nil {
		return "", domain.User{}, fmt.Errorf("check email: %w", err)
	}
	if exists {
		return "", domain.User{}, ErrUserExists
	}
	if name == "" {
		name = email
	}
	hash, err := bcryptHash(password)
	if err != nil {
		return "", domain.User{}, err
	}
	var u domain.User
	err = s.db.QueryRow(ctx,
		`INSERT INTO users (org_id, email, password_hash, name, role, auth_provider, email_verified)
		 VALUES (NULL, $1, $2, $3, 'member', 'password', false)
		 RETURNING id, email, name, role, status`,
		email, hash, name).Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Status)
	if err != nil {
		return "", domain.User{}, fmt.Errorf("create pending user: %w", err)
	}
	u.OrgID = uuid.Nil
	u.AuthProvider = "password"
	tok, err := s.issueJWT(u)
	return tok, u, err
}

// ── Members ────────────────────────────────────────────────────────────────────

func (s *ServiceImpl) ListMembers(ctx context.Context, orgID uuid.UUID) ([]domain.Member, error) {
	rows, err := s.db.Query(ctx,
		`SELECT m.id, m.org_id, m.user_id, u.email, u.name, m.role, u.avatar_url,
		        m.invited_by, m.created_at, u.last_login_at
		   FROM organization_members m
		   JOIN users u ON u.id = m.user_id
		  WHERE m.org_id = $1
		  ORDER BY (m.role='owner') DESC, (m.role='admin') DESC, u.name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Member{}
	for rows.Next() {
		var m domain.Member
		if err := rows.Scan(&m.ID, &m.OrgID, &m.UserID, &m.Email, &m.Name, &m.Role,
			&m.AvatarURL, &m.InvitedBy, &m.CreatedAt, &m.LastLoginAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ChangeMemberRole enforces the role hierarchy:
//   - you cannot change your own role (prevents self-lockout / self-promotion);
//   - granting or removing 'owner' requires the actor to be an owner;
//   - the last owner cannot be demoted;
//   - admins may only move members between {member, admin} for non-owner targets.
func (s *ServiceImpl) ChangeMemberRole(ctx context.Context, orgID, actorID uuid.UUID, actorRole domain.Role, targetUserID uuid.UUID, newRole domain.Role) error {
	newRole = newRole.Normalize()
	if newRole != domain.RoleOwner && newRole != domain.RoleAdmin && newRole != domain.RoleMember {
		return fmt.Errorf("invalid role %q", newRole)
	}
	if actorID == targetUserID {
		return fmt.Errorf("%w: you cannot change your own role", ErrForbidden)
	}
	current, err := s.memberRole(ctx, orgID, targetUserID)
	if err != nil {
		return err
	}
	if (newRole == domain.RoleOwner || current == domain.RoleOwner) && actorRole != domain.RoleOwner {
		return fmt.Errorf("%w: only an owner can grant or remove the owner role", ErrForbidden)
	}
	if current == domain.RoleOwner && newRole != domain.RoleOwner {
		if n, err := s.ownerCount(ctx, orgID); err != nil {
			return err
		} else if n < 2 {
			return ErrLastOwner
		}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`UPDATE organization_members SET role=$3 WHERE org_id=$1 AND user_id=$2`, orgID, targetUserID, string(newRole)); err != nil {
		return err
	}
	// Mirror onto users.role since the active org's role IS users.role (single-org).
	if _, err := tx.Exec(ctx,
		`UPDATE users SET role=$3 WHERE id=$2 AND org_id=$1`, orgID, targetUserID, string(newRole)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RemoveMember removes a user from the org; they revert to pre-onboarding (org_id NULL)
// and all their sessions are revoked. Cannot remove yourself or the last owner here.
func (s *ServiceImpl) RemoveMember(ctx context.Context, orgID, actorID uuid.UUID, actorRole domain.Role, targetUserID uuid.UUID) error {
	if actorID == targetUserID {
		return fmt.Errorf("%w: you cannot remove yourself", ErrForbidden)
	}
	current, err := s.memberRole(ctx, orgID, targetUserID)
	if err != nil {
		return err
	}
	if current == domain.RoleOwner {
		if actorRole != domain.RoleOwner {
			return fmt.Errorf("%w: only an owner can remove an owner", ErrForbidden)
		}
		if n, err := s.ownerCount(ctx, orgID); err != nil {
			return err
		} else if n < 2 {
			return ErrLastOwner
		}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM organization_members WHERE org_id=$1 AND user_id=$2`, orgID, targetUserID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET org_id=NULL, role='member' WHERE id=$1 AND org_id=$2`, targetUserID, orgID); err != nil {
		return err
	}
	// Kick the removed user out of all sessions (their access token also dead-ends at the
	// requireOrg gate once org_id is NULL).
	if _, err := tx.Exec(ctx,
		`UPDATE user_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, targetUserID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *ServiceImpl) memberRole(ctx context.Context, orgID, userID uuid.UUID) (domain.Role, error) {
	var role string
	err := s.db.QueryRow(ctx, `SELECT role FROM organization_members WHERE org_id=$1 AND user_id=$2`, orgID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return domain.Role(role), err
}

// LeaveOrg removes the caller from their org (reverting to pre-onboarding), revoking every
// session except the current one, and returns a fresh org-less token. The last owner can't
// leave (would orphan the org) — they must transfer ownership or delete the org first.
func (s *ServiceImpl) LeaveOrg(ctx context.Context, orgID, userID uuid.UUID, role domain.Role, exceptSessionID *uuid.UUID) (string, domain.User, error) {
	if role.Normalize() == domain.RoleOwner {
		if n, err := s.ownerCount(ctx, orgID); err != nil {
			return "", domain.User{}, err
		} else if n < 2 {
			return "", domain.User{}, ErrLastOwner
		}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", domain.User{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM organization_members WHERE org_id=$1 AND user_id=$2`, orgID, userID); err != nil {
		return "", domain.User{}, err
	}
	var u domain.User
	if err := tx.QueryRow(ctx,
		`UPDATE users SET org_id=NULL, role='member' WHERE id=$1 AND org_id=$2
		 RETURNING id, org_id, department_id, email, name, role, status, avatar_url, email_verified, auth_provider`,
		userID, orgID).
		Scan(&u.ID, &u.OrgID, &u.DepartmentID, &u.Email, &u.Name, &u.Role, &u.Status,
			&u.AvatarURL, &u.EmailVerified, &u.AuthProvider); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.User{}, ErrNotFound // not actually a member of this org
		}
		return "", domain.User{}, err
	}
	// Keep the current device signed in (now in onboarding state); revoke the rest.
	if _, err := tx.Exec(ctx,
		`UPDATE user_sessions SET revoked_at=now()
		   WHERE user_id=$1 AND revoked_at IS NULL AND ($2::uuid IS NULL OR id <> $2)`,
		userID, exceptSessionID); err != nil {
		return "", domain.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", domain.User{}, err
	}
	u.OrgID = uuid.Nil
	tok, err := s.issueJWT(u)
	return tok, u, err
}

// TransferOwnership promotes targetUserID to owner and demotes the acting owner to admin,
// atomically. Only an owner may call it; the target must already be a member. After the
// transfer exactly one owner remains (the standard single-owner org).
func (s *ServiceImpl) TransferOwnership(ctx context.Context, orgID, actorID uuid.UUID, actorRole domain.Role, targetUserID uuid.UUID) error {
	if actorRole.Normalize() != domain.RoleOwner {
		return fmt.Errorf("%w: only the owner can transfer ownership", ErrForbidden)
	}
	if actorID == targetUserID {
		return fmt.Errorf("%w: you are already the owner", ErrForbidden)
	}
	if _, err := s.memberRole(ctx, orgID, targetUserID); err != nil {
		return err // ErrNotFound if the target isn't a member of this org
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Demote the acting owner, promote the target. Mirror onto users.role.
	for _, op := range []struct {
		uid  uuid.UUID
		role string
	}{{actorID, "admin"}, {targetUserID, "owner"}} {
		if _, err := tx.Exec(ctx, `UPDATE organization_members SET role=$3 WHERE org_id=$1 AND user_id=$2`, orgID, op.uid, op.role); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET role=$3 WHERE id=$2 AND org_id=$1`, orgID, op.uid, op.role); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// UpdateInvitationDelivery records the async email-send outcome (advisory observability).
func (s *ServiceImpl) UpdateInvitationDelivery(ctx context.Context, invID uuid.UUID, status, errMsg string) error {
	var deliveredAt any
	if status == "sent" {
		deliveredAt = time.Now()
	}
	_, err := s.db.Exec(ctx,
		`UPDATE organization_invitations SET delivery_status=$2, delivery_error=$3, delivered_at=$4 WHERE id=$1`,
		invID, status, errMsg, deliveredAt)
	return err
}

// OrgName returns an organization's display name (for invite emails).
func (s *ServiceImpl) OrgName(ctx context.Context, orgID uuid.UUID) (string, error) {
	var name string
	err := s.db.QueryRow(ctx, `SELECT name FROM organizations WHERE id=$1`, orgID).Scan(&name)
	return name, err
}

func (s *ServiceImpl) ownerCount(ctx context.Context, orgID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM organization_members WHERE org_id=$1 AND role='owner'`, orgID).Scan(&n)
	return n, err
}

// ── Invitations ─────────────────────────────────────────────────────────────────

func (s *ServiceImpl) CreateInvitation(ctx context.Context, orgID, invitedBy uuid.UUID, email string, role domain.Role) (string, domain.Invitation, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return "", domain.Invitation{}, errors.New("email is required")
	}
	role = role.Normalize()
	if role != domain.RoleAdmin && role != domain.RoleMember {
		role = domain.RoleMember // cannot invite an owner; default to member
	}
	// Already a member of this org?
	var member bool
	if err := s.db.QueryRow(ctx,
		`SELECT exists(SELECT 1 FROM organization_members m JOIN users u ON u.id=m.user_id
		               WHERE m.org_id=$1 AND u.email=$2)`, orgID, email).Scan(&member); err != nil {
		return "", domain.Invitation{}, err
	}
	if member {
		return "", domain.Invitation{}, ErrAlreadyMember
	}
	// Supersede any existing pending invite for this email so the partial-unique index holds.
	if _, err := s.db.Exec(ctx,
		`UPDATE organization_invitations SET revoked_at=now()
		   WHERE org_id=$1 AND email=$2 AND accepted_at IS NULL AND revoked_at IS NULL`, orgID, email); err != nil {
		return "", domain.Invitation{}, err
	}
	raw := randomToken()
	var inv domain.Invitation
	err := s.db.QueryRow(ctx,
		`INSERT INTO organization_invitations (org_id, email, role, token_hash, invited_by, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 RETURNING `+inviteCols,
		orgID, email, string(role), hashToken(raw), invitedBy, time.Now().Add(invitationTTL)).
		Scan(scanInvite(&inv)...)
	if err != nil {
		return "", domain.Invitation{}, fmt.Errorf("create invitation: %w", err)
	}
	inv.Status = invitationStatus(inv)
	return raw, inv, nil
}

// inviteCols / scanInvite keep the invitation projection in one place (incl. delivery fields).
const inviteCols = `id, org_id, email, role, invited_by, created_at, expires_at,
	accepted_at, revoked_at, delivery_status, delivery_error, delivered_at`

func scanInvite(inv *domain.Invitation) []any {
	return []any{&inv.ID, &inv.OrgID, &inv.Email, &inv.Role, &inv.InvitedBy, &inv.CreatedAt,
		&inv.ExpiresAt, &inv.AcceptedAt, &inv.RevokedAt, &inv.DeliveryStatus, &inv.DeliveryError, &inv.DeliveredAt}
}

func (s *ServiceImpl) ListInvitations(ctx context.Context, orgID uuid.UUID) ([]domain.Invitation, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+inviteCols+` FROM organization_invitations WHERE org_id=$1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Invitation{}
	for rows.Next() {
		var inv domain.Invitation
		if err := rows.Scan(scanInvite(&inv)...); err != nil {
			return nil, err
		}
		inv.Status = invitationStatus(inv)
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (s *ServiceImpl) RevokeInvitation(ctx context.Context, orgID, invID uuid.UUID) error {
	ct, err := s.db.Exec(ctx,
		`UPDATE organization_invitations SET revoked_at=now()
		   WHERE id=$1 AND org_id=$2 AND accepted_at IS NULL AND revoked_at IS NULL`, invID, orgID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResendInvitation rotates the token and extends the expiry of a still-pending invite,
// returning a fresh raw token to share again.
func (s *ServiceImpl) ResendInvitation(ctx context.Context, orgID, invID uuid.UUID) (string, domain.Invitation, error) {
	raw := randomToken()
	var inv domain.Invitation
	err := s.db.QueryRow(ctx,
		`UPDATE organization_invitations
		    SET token_hash=$3, expires_at=$4,
		        delivery_status='pending', delivery_error='', delivered_at=NULL
		  WHERE id=$1 AND org_id=$2 AND accepted_at IS NULL AND revoked_at IS NULL
		RETURNING `+inviteCols,
		invID, orgID, hashToken(raw), time.Now().Add(invitationTTL)).
		Scan(scanInvite(&inv)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.Invitation{}, ErrNotFound
	}
	if err != nil {
		return "", domain.Invitation{}, err
	}
	inv.Status = invitationStatus(inv)
	return raw, inv, nil
}

// InvitationByToken previews a pending invite for the public accept page.
func (s *ServiceImpl) InvitationByToken(ctx context.Context, rawToken string) (domain.Invitation, string, error) {
	if strings.TrimSpace(rawToken) == "" {
		return domain.Invitation{}, "", ErrInvitationInvalid
	}
	var inv domain.Invitation
	var orgName string
	err := s.db.QueryRow(ctx,
		`SELECT i.id, i.org_id, i.email, i.role, i.invited_by, i.created_at, i.expires_at,
		        i.accepted_at, i.revoked_at, i.delivery_status, i.delivery_error, i.delivered_at, o.name
		   FROM organization_invitations i JOIN organizations o ON o.id=i.org_id
		  WHERE i.token_hash=$1`, hashToken(rawToken)).
		Scan(&inv.ID, &inv.OrgID, &inv.Email, &inv.Role, &inv.InvitedBy, &inv.CreatedAt,
			&inv.ExpiresAt, &inv.AcceptedAt, &inv.RevokedAt, &inv.DeliveryStatus, &inv.DeliveryError, &inv.DeliveredAt, &orgName)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invitation{}, "", ErrInvitationInvalid
	}
	if err != nil {
		return domain.Invitation{}, "", err
	}
	inv.Status = invitationStatus(inv)
	return inv, orgName, nil
}

// AcceptInvitation joins the inviting org. The accepting user must be pre-onboarding and
// their email must match the invite (the token alone can't be used by another identity).
func (s *ServiceImpl) AcceptInvitation(ctx context.Context, userID uuid.UUID, rawToken string) (string, domain.User, error) {
	// Load the user (email + current org).
	var email string
	var orgID *uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT email, org_id FROM users WHERE id=$1 AND status='active'`, userID).
		Scan(&email, &orgID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.User{}, ErrInvalidCredentials
		}
		return "", domain.User{}, err
	}
	if orgID != nil {
		return "", domain.User{}, ErrAlreadyMember
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", domain.User{}, err
	}
	defer tx.Rollback(ctx)

	// Claim the invite atomically (guards double-accept / races).
	var inv domain.Invitation
	err = tx.QueryRow(ctx,
		`UPDATE organization_invitations SET accepted_at=now()
		  WHERE token_hash=$1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at>now()
		RETURNING id, org_id, email, role, invited_by`,
		hashToken(rawToken)).Scan(&inv.ID, &inv.OrgID, &inv.Email, &inv.Role, &inv.InvitedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.User{}, ErrInvitationInvalid
	}
	if err != nil {
		return "", domain.User{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(email), string(inv.Email)) {
		// Email mismatch — roll back the accept (defer Rollback) and refuse.
		return "", domain.User{}, fmt.Errorf("%w: this invitation was sent to a different email", ErrForbidden)
	}

	user, err := s.joinOrgTx(ctx, tx, userID, inv.OrgID, inv.Role, inv.InvitedBy)
	if err != nil {
		return "", domain.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", domain.User{}, err
	}
	tok, err := s.issueJWT(user)
	return tok, user, err
}

// ── Join codes ──────────────────────────────────────────────────────────────────

func (s *ServiceImpl) CreateJoinCode(ctx context.Context, orgID, createdBy uuid.UUID, desc string, ttl time.Duration, maxUses int) (string, error) {
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	if maxUses < 0 {
		maxUses = 0
	}
	raw := randomToken()[:12] // short, shareable
	_, err := s.db.Exec(ctx,
		`INSERT INTO organization_join_codes (org_id, code_hash, description, created_by, max_uses, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		orgID, hashToken(raw), strings.TrimSpace(desc), createdBy, maxUses, time.Now().Add(ttl))
	if err != nil {
		return "", fmt.Errorf("create join code: %w", err)
	}
	return raw, nil
}

func (s *ServiceImpl) ListJoinCodes(ctx context.Context, orgID uuid.UUID) ([]domain.JoinCode, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, org_id, description, created_by, max_uses, uses, expires_at, revoked_at, created_at
		   FROM organization_join_codes WHERE org_id=$1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.JoinCode{}
	now := time.Now()
	for rows.Next() {
		var c domain.JoinCode
		if err := rows.Scan(&c.ID, &c.OrgID, &c.Description, &c.CreatedBy, &c.MaxUses,
			&c.Uses, &c.ExpiresAt, &c.RevokedAt, &c.CreatedAt); err != nil {
			return nil, err
		}
		switch {
		case c.RevokedAt != nil:
			c.Status = "revoked"
		case now.After(c.ExpiresAt):
			c.Status = "expired"
		case c.MaxUses > 0 && c.Uses >= c.MaxUses:
			c.Status = "exhausted"
		default:
			c.Status = "active"
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *ServiceImpl) RevokeJoinCode(ctx context.Context, orgID, codeID uuid.UUID) error {
	ct, err := s.db.Exec(ctx,
		`UPDATE organization_join_codes SET revoked_at=now()
		   WHERE id=$1 AND org_id=$2 AND revoked_at IS NULL`, codeID, orgID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// JoinViaCode self-joins an org as a member. The use is claimed atomically so max_uses
// can't be exceeded under concurrency. The joining user must be pre-onboarding.
func (s *ServiceImpl) JoinViaCode(ctx context.Context, userID uuid.UUID, rawCode string) (string, domain.User, error) {
	rawCode = strings.TrimSpace(rawCode)
	if rawCode == "" {
		return "", domain.User{}, ErrJoinCodeInvalid
	}
	var orgID *uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT org_id FROM users WHERE id=$1 AND status='active'`, userID).Scan(&orgID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.User{}, ErrInvalidCredentials
		}
		return "", domain.User{}, err
	}
	if orgID != nil {
		return "", domain.User{}, ErrAlreadyMember
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", domain.User{}, err
	}
	defer tx.Rollback(ctx)

	var targetOrg uuid.UUID
	err = tx.QueryRow(ctx,
		`UPDATE organization_join_codes SET uses=uses+1
		  WHERE code_hash=$1 AND revoked_at IS NULL AND expires_at>now()
		    AND (max_uses=0 OR uses<max_uses)
		RETURNING org_id`, hashToken(rawCode)).Scan(&targetOrg)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.User{}, ErrJoinCodeInvalid
	}
	if err != nil {
		return "", domain.User{}, err
	}

	user, err := s.joinOrgTx(ctx, tx, userID, targetOrg, domain.RoleMember, nil)
	if err != nil {
		return "", domain.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", domain.User{}, err
	}
	tok, err := s.issueJWT(user)
	return tok, user, err
}

// joinOrgTx attaches a pre-onboarding user to an existing org with the given role inside a
// transaction: sets users.org_id/role and upserts the membership row. Returns the user.
func (s *ServiceImpl) joinOrgTx(ctx context.Context, tx pgx.Tx, userID, orgID uuid.UUID, role domain.Role, invitedBy *uuid.UUID) (domain.User, error) {
	role = role.Normalize()
	var u domain.User
	if err := tx.QueryRow(ctx,
		`UPDATE users SET org_id=$2, role=$3 WHERE id=$1 AND org_id IS NULL
		 RETURNING id, org_id, department_id, email, name, role, status, avatar_url, email_verified, auth_provider`,
		userID, orgID, string(role)).
		Scan(&u.ID, &u.OrgID, &u.DepartmentID, &u.Email, &u.Name, &u.Role, &u.Status,
			&u.AvatarURL, &u.EmailVerified, &u.AuthProvider); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, ErrAlreadyMember // raced into an org between checks
		}
		return domain.User{}, fmt.Errorf("attach user to org: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO organization_members (org_id, user_id, role, invited_by)
		 VALUES ($1,$2,$3,$4)
		 ON CONFLICT (org_id, user_id) DO UPDATE SET role=EXCLUDED.role`,
		orgID, userID, string(role), invitedBy); err != nil {
		return domain.User{}, fmt.Errorf("create membership: %w", err)
	}
	return u, nil
}

func invitationStatus(inv domain.Invitation) string {
	switch {
	case inv.RevokedAt != nil:
		return "revoked"
	case inv.AcceptedAt != nil:
		return "accepted"
	case time.Now().After(inv.ExpiresAt):
		return "expired"
	default:
		return "pending"
	}
}
