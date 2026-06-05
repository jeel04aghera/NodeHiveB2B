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
