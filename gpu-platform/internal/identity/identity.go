// Package identity is the Identity & Auth module.
// Owns tables: organizations, users, enrollment_tokens, agent_credentials.
// Produces events: events.NodeEnrolled. Consumes: none.
// DB access: via Store (sqlc-backed in prod, fake in tests). The Service interface
// is the public seam other modules and the HTTP/gRPC layers depend on.
package identity

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/nodehive/gpu-platform/internal/domain"
)

type Service interface {
	Login(ctx context.Context, email, password string) (token string, user domain.User, err error)
	// Register creates a brand-new organization with an admin user and returns a session.
	Register(ctx context.Context, orgName, email, name, password string) (token string, user domain.User, err error)
	Authenticate(ctx context.Context, token string) (domain.User, error)
	// UpsertGoogleUser logs in / links / creates a user from a verified Google identity.
	// The returned user may have OrgID == uuid.Nil (onboarding required).
	UpsertGoogleUser(ctx context.Context, sub, email, name, avatar string, emailVerified bool) (token string, user domain.User, err error)
	// CreateOrgForUser provisions an org for a pre-onboarding user and makes them admin.
	CreateOrgForUser(ctx context.Context, userID uuid.UUID, orgName string) (token string, user domain.User, err error)
	CreateUser(ctx context.Context, orgID uuid.UUID, email, name string, role domain.Role) (domain.User, error)
	ListUsers(ctx context.Context, orgID uuid.UUID) ([]domain.User, error)

	// Agent trust lifecycle.
	IssueEnrollmentToken(ctx context.Context, orgID, createdBy uuid.UUID, ttl time.Duration, maxUses int) (rawToken string, err error)
	IssueEnrollmentTokenDesc(ctx context.Context, orgID, createdBy uuid.UUID, desc string, ttl time.Duration, maxUses int) (rawToken string, err error)
	ListEnrollmentTokens(ctx context.Context, orgID uuid.UUID) ([]domain.EnrollmentToken, error)
	RevokeEnrollmentToken(ctx context.Context, orgID, tokenID uuid.UUID) error
	EnrollAgent(ctx context.Context, rawToken, publicKey string, node domain.Node) (nodeID uuid.UUID, credential string, err error)

	// BootstrapAdmin creates an admin from "email:password" if the org has no users.
	BootstrapAdmin(ctx context.Context, spec string) error

	// ── Session management (Phase 2) ──────────────────────────────────────────
	// IssueAccessToken mints a short-lived Bearer access token for a user (no session
	// row). Used when rotating a refresh token to hand back a fresh access token.
	IssueAccessToken(ctx context.Context, u domain.User) (string, error)
	// CreateSession opens a refresh-token session for a user and returns the RAW
	// refresh token (caller puts it in an HttpOnly cookie; only its hash is stored).
	CreateSession(ctx context.Context, userID uuid.UUID, dev DeviceInfo) (rawRefresh string, sess domain.Session, err error)
	// RefreshSession validates a raw refresh token, rotates it (old token invalidated),
	// and returns a fresh access token plus the new raw refresh token for the same session.
	RefreshSession(ctx context.Context, rawRefresh string, dev DeviceInfo) (access, newRawRefresh string, user domain.User, sess domain.Session, err error)
	// ListSessions returns a user's active (non-revoked, unexpired) sessions.
	ListSessions(ctx context.Context, userID uuid.UUID) ([]domain.Session, error)
	// RevokeSession revokes one session, scoped to its owner (cannot touch others').
	RevokeSession(ctx context.Context, userID, sessionID uuid.UUID) error
	// RevokeAllSessions revokes every active session for a user, optionally keeping one
	// (the caller's current session) so "log out everywhere else" leaves them signed in.
	RevokeAllSessions(ctx context.Context, userID uuid.UUID, exceptSessionID *uuid.UUID) error
	// RevokeSessionByRefresh revokes the session identified by a raw refresh token (logout).
	RevokeSessionByRefresh(ctx context.Context, rawRefresh string) error
	// SessionIDByRefresh resolves the session id for a raw refresh token (current-session marker).
	SessionIDByRefresh(ctx context.Context, rawRefresh string) (uuid.UUID, error)

	// ── Organization memberships, invitations, join codes (Phase 3) ───────────
	// RegisterPending creates a password account with NO organization (pre-onboarding),
	// for users who will accept an invite or join via code rather than create an org.
	RegisterPending(ctx context.Context, email, name, password string) (token string, user domain.User, err error)

	ListMembers(ctx context.Context, orgID uuid.UUID) ([]domain.Member, error)
	// ChangeMemberRole updates a member's role, enforcing the role hierarchy (see rules in impl).
	ChangeMemberRole(ctx context.Context, orgID, actorID uuid.UUID, actorRole domain.Role, targetUserID uuid.UUID, newRole domain.Role) error
	// RemoveMember removes a user from the org (they revert to pre-onboarding).
	RemoveMember(ctx context.Context, orgID, actorID uuid.UUID, actorRole domain.Role, targetUserID uuid.UUID) error

	// LeaveOrg removes the caller from their org (reverting to pre-onboarding) and revokes
	// every session except the current one. The last owner cannot leave.
	LeaveOrg(ctx context.Context, orgID, userID uuid.UUID, role domain.Role, exceptSessionID *uuid.UUID) (token string, user domain.User, err error)
	// TransferOwnership makes targetUserID the org's owner and demotes the acting owner to admin.
	TransferOwnership(ctx context.Context, orgID, actorID uuid.UUID, actorRole domain.Role, targetUserID uuid.UUID) error

	CreateInvitation(ctx context.Context, orgID, invitedBy uuid.UUID, email string, role domain.Role) (rawToken string, inv domain.Invitation, err error)
	ListInvitations(ctx context.Context, orgID uuid.UUID) ([]domain.Invitation, error)
	RevokeInvitation(ctx context.Context, orgID, invID uuid.UUID) error
	ResendInvitation(ctx context.Context, orgID, invID uuid.UUID) (rawToken string, inv domain.Invitation, err error)
	// UpdateInvitationDelivery records the outcome of the async email send (observability).
	UpdateInvitationDelivery(ctx context.Context, invID uuid.UUID, status, errMsg string) error
	// OrgName returns an organization's display name (for invite emails).
	OrgName(ctx context.Context, orgID uuid.UUID) (string, error)
	// InvitationByToken previews a pending invite (public, for the accept page).
	InvitationByToken(ctx context.Context, rawToken string) (inv domain.Invitation, orgName string, err error)
	// AcceptInvitation joins the inviting org; the user's email must match the invite.
	AcceptInvitation(ctx context.Context, userID uuid.UUID, rawToken string) (token string, user domain.User, err error)

	CreateJoinCode(ctx context.Context, orgID, createdBy uuid.UUID, desc string, ttl time.Duration, maxUses int) (rawCode string, err error)
	ListJoinCodes(ctx context.Context, orgID uuid.UUID) ([]domain.JoinCode, error)
	RevokeJoinCode(ctx context.Context, orgID, codeID uuid.UUID) error
	// JoinViaCode self-joins an org as a member using a shareable code.
	JoinViaCode(ctx context.Context, userID uuid.UUID, rawCode string) (token string, user domain.User, err error)
}

// DeviceInfo describes the client opening or using a session (for device tracking).
type DeviceInfo struct {
	UserAgent  string
	IPAddress  string
	DeviceName string
	Browser    string
	OS         string
}

// Option configures a ServiceImpl at construction (e.g. token TTLs) without breaking
// existing NewService callers.
type Option func(*ServiceImpl)

// WithAccessTTL overrides the short-lived access-token lifetime (default: sessionTTL).
func WithAccessTTL(d time.Duration) Option {
	return func(s *ServiceImpl) {
		if d > 0 {
			s.accessTTL = d
		}
	}
}

// WithRefreshTTL overrides the refresh-token (session) lifetime (default: 30 days).
func WithRefreshTTL(d time.Duration) Option {
	return func(s *ServiceImpl) {
		if d > 0 {
			s.refreshTTL = d
		}
	}
}

type Store interface {
	CreateUser(ctx context.Context, u domain.User) (domain.User, error)
	UserByEmail(ctx context.Context, orgID uuid.UUID, email string) (domain.User, error)
	ListUsers(ctx context.Context, orgID uuid.UUID) ([]domain.User, error)
	CountUsers(ctx context.Context, orgID uuid.UUID) (int, error)
	InsertEnrollmentToken(ctx context.Context, orgID, createdBy uuid.UUID, tokenHash string, expires time.Time, maxUses int) error
	ConsumeEnrollmentToken(ctx context.Context, tokenHash string) (orgID uuid.UUID, err error)
	InsertAgentCredential(ctx context.Context, orgID, nodeID uuid.UUID, publicKey string) error
}
