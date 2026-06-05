package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/nodehive/gpu-platform/internal/domain"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrUserExists         = errors.New("user already exists")
	ErrNotFound           = errors.New("not found")
)

// ServiceImpl is the concrete identity service.
type ServiceImpl struct {
	db         *pgxpool.Pool
	jwtSecret  []byte
	sessionTTL time.Duration
}

func NewService(db *pgxpool.Pool, jwtSecret string, sessionTTL time.Duration) Service {
	return &ServiceImpl{db: db, jwtSecret: []byte(jwtSecret), sessionTTL: sessionTTL}
}

type jwtClaims struct {
	UserID string `json:"uid"`
	OrgID  string `json:"oid"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

func (s *ServiceImpl) Login(ctx context.Context, email, password string) (string, domain.User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	// Find user by email (any org — for single-org V1 we match on email alone).
	var u domain.User
	var orgID *uuid.UUID // NULL for pre-onboarding users
	var hash *string     // NULL for OAuth-only users
	err := s.db.QueryRow(ctx,
		`SELECT u.id, u.org_id, u.department_id, u.email, u.password_hash, u.name, u.role, u.status
		   FROM users u WHERE u.email = $1 AND u.status = 'active' LIMIT 1`,
		email).Scan(&u.ID, &orgID, &u.DepartmentID, &u.Email, &hash, &u.Name, &u.Role, &u.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.User{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", domain.User{}, fmt.Errorf("lookup user: %w", err)
	}
	if orgID != nil {
		u.OrgID = *orgID
	}
	// OAuth-only accounts have no password — reject password login for them.
	if hash == nil || *hash == "" {
		return "", domain.User{}, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(*hash), []byte(password)); err != nil {
		return "", domain.User{}, ErrInvalidCredentials
	}
	_, _ = s.db.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, u.ID)
	token, err := s.issueJWT(u)
	return token, u, err
}

// Register provisions a new organization with an admin user, default rate cards,
// and a welcome credit grant, then returns a session for the new admin.
func (s *ServiceImpl) Register(ctx context.Context, orgName, email, name, password string) (string, domain.User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	orgName = strings.TrimSpace(orgName)
	if email == "" || password == "" || orgName == "" {
		return "", domain.User{}, errors.New("organization, email and password are required")
	}
	if len(password) < 8 {
		return "", domain.User{}, errors.New("password must be at least 8 characters")
	}
	// Reject if the email is already in use (login matches on email alone).
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT exists(SELECT 1 FROM users WHERE email=$1)`, email).Scan(&exists); err != nil {
		return "", domain.User{}, fmt.Errorf("check email: %w", err)
	}
	if exists {
		return "", domain.User{}, ErrUserExists
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", domain.User{}, err
	}
	defer tx.Rollback(ctx)

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", domain.User{}, err
	}
	if name == "" {
		name = "Admin"
	}
	orgID, err := provisionOrgTx(ctx, tx, orgName)
	if err != nil {
		return "", domain.User{}, err
	}
	var u domain.User
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (org_id, email, password_hash, name, role, auth_provider, email_verified)
		 VALUES ($1, $2, $3, $4, 'admin', 'password', false)
		 RETURNING id, org_id, email, name, role, status`,
		orgID, email, string(hash), name).Scan(&u.ID, &u.OrgID, &u.Email, &u.Name, &u.Role, &u.Status); err != nil {
		return "", domain.User{}, fmt.Errorf("create admin: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", domain.User{}, err
	}
	token, err := s.issueJWT(u)
	return token, u, err
}

// provisionOrgTx creates an organization with default rate cards and a welcome credit
// grant inside an existing transaction, returning the new org id. Shared by Register
// (new account) and CreateOrgForUser (existing pre-onboarding user). createdBy may be nil.
func provisionOrgTx(ctx context.Context, tx pgx.Tx, orgName string) (uuid.UUID, error) {
	slug := slugify(orgName) + "-" + randomToken()[:6]
	var orgID uuid.UUID
	if err := tx.QueryRow(ctx,
		`INSERT INTO organizations (name, slug, settings)
		 VALUES ($1, $2, '{"currency":"USD","default_rate":0.5}'::jsonb)
		 RETURNING id`, orgName, slug).Scan(&orgID); err != nil {
		return uuid.Nil, fmt.Errorf("create org: %w", err)
	}
	for _, rc := range []struct {
		model string
		rate  float64
	}{
		{"NVIDIA RTX 4090", 0.34}, {"NVIDIA RTX 6000 Ada", 0.69},
		{"NVIDIA A100", 0.89}, {"NVIDIA H100", 2.69},
	} {
		if _, err := tx.Exec(ctx,
			`INSERT INTO rate_cards (org_id, gpu_model, rate_per_gpu_hour, currency) VALUES ($1,$2,$3,'USD')`,
			orgID, rc.model, rc.rate); err != nil {
			return uuid.Nil, fmt.Errorf("seed rate cards: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO credit_ledger (org_id, delta, balance, kind, description)
		 VALUES ($1, 50000.0000, 50000.0000, 'grant', 'Welcome credit')`, orgID); err != nil {
		return uuid.Nil, fmt.Errorf("seed credit: %w", err)
	}
	return orgID, nil
}

// UpsertGoogleUser links a Google identity to a user account and returns a session.
// Resolution order (prevents duplicate identities):
//  1. existing user with this google_sub  → log in
//  2. existing user with this email        → link Google to it (requires verified email)
//  3. otherwise                            → create a new pre-onboarding user (org_id NULL)
//
// The returned user may have OrgID == uuid.Nil, meaning onboarding is required.
func (s *ServiceImpl) UpsertGoogleUser(ctx context.Context, sub, email, name, avatar string, emailVerified bool) (string, domain.User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if sub == "" || email == "" {
		return "", domain.User{}, errors.New("google: missing subject or email")
	}
	// Google must assert the email is verified before we trust it for identity linking —
	// otherwise a Google account spoofing a victim's email could hijack their account.
	if !emailVerified {
		return "", domain.User{}, errors.New("google: email is not verified")
	}

	// 1) Match by stable Google subject.
	if u, err := s.userByGoogleSub(ctx, sub); err == nil {
		_, _ = s.db.Exec(ctx,
			`UPDATE users SET last_login_at=now(), avatar_url=$2, email_verified=true WHERE id=$1`, u.ID, avatar)
		u.AvatarURL = avatar
		tok, err := s.issueJWT(u)
		return tok, u, err
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", domain.User{}, err
	}

	// 2) Link to an existing account with the same (verified) email.
	if u, err := s.userByEmail(ctx, email); err == nil {
		if _, err := s.db.Exec(ctx,
			`UPDATE users SET google_sub=$2, email_verified=true, last_login_at=now(),
			    avatar_url=CASE WHEN avatar_url='' THEN $3 ELSE avatar_url END
			  WHERE id=$1`, u.ID, sub, avatar); err != nil {
			return "", domain.User{}, fmt.Errorf("link google: %w", err)
		}
		u.AuthProvider = "google"
		tok, err := s.issueJWT(u)
		return tok, u, err
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", domain.User{}, err
	}

	// 3) Brand-new identity, no org yet (pre-onboarding).
	if name == "" {
		name = email
	}
	var u domain.User
	err := s.db.QueryRow(ctx,
		`INSERT INTO users (org_id, email, password_hash, name, role, google_sub, avatar_url, email_verified, auth_provider)
		 VALUES (NULL, $1, NULL, $2, 'user', $3, $4, true, 'google')
		 RETURNING id, email, name, role, status`,
		email, name, sub, avatar).Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Status)
	if err != nil {
		return "", domain.User{}, fmt.Errorf("create google user: %w", err)
	}
	u.OrgID = uuid.Nil
	u.AvatarURL, u.EmailVerified, u.AuthProvider = avatar, true, "google"
	tok, err := s.issueJWT(u)
	return tok, u, err
}

// CreateOrgForUser provisions a new organization for an existing pre-onboarding user
// and makes them its admin. Fails if the user already belongs to an org (single-org).
func (s *ServiceImpl) CreateOrgForUser(ctx context.Context, userID uuid.UUID, orgName string) (string, domain.User, error) {
	orgName = strings.TrimSpace(orgName)
	if orgName == "" {
		return "", domain.User{}, errors.New("organization name is required")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", domain.User{}, err
	}
	defer tx.Rollback(ctx)

	var existingOrg *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT org_id FROM users WHERE id=$1 AND status='active'`, userID).Scan(&existingOrg); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.User{}, ErrInvalidCredentials
		}
		return "", domain.User{}, err
	}
	if existingOrg != nil {
		return "", domain.User{}, errors.New("user already belongs to an organization")
	}

	orgID, err := provisionOrgTx(ctx, tx, orgName)
	if err != nil {
		return "", domain.User{}, err
	}
	var u domain.User
	if err := tx.QueryRow(ctx,
		`UPDATE users SET org_id=$2, role='admin' WHERE id=$1
		 RETURNING id, org_id, department_id, email, name, role, status`,
		userID, orgID).Scan(&u.ID, &u.OrgID, &u.DepartmentID, &u.Email, &u.Name, &u.Role, &u.Status); err != nil {
		return "", domain.User{}, fmt.Errorf("attach org: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", domain.User{}, err
	}
	tok, err := s.issueJWT(u)
	return tok, u, err
}

// userByGoogleSub / userByEmail are small scoped lookups used by UpsertGoogleUser.
func (s *ServiceImpl) userByGoogleSub(ctx context.Context, sub string) (domain.User, error) {
	return s.scanUserRow(s.db.QueryRow(ctx,
		`SELECT id, org_id, department_id, email, name, role, status, avatar_url, email_verified, auth_provider
		   FROM users WHERE google_sub=$1 AND status='active'`, sub))
}

func (s *ServiceImpl) userByEmail(ctx context.Context, email string) (domain.User, error) {
	return s.scanUserRow(s.db.QueryRow(ctx,
		`SELECT id, org_id, department_id, email, name, role, status, avatar_url, email_verified, auth_provider
		   FROM users WHERE email=$1 AND status='active' LIMIT 1`, email))
}

func (s *ServiceImpl) scanUserRow(row pgx.Row) (domain.User, error) {
	var u domain.User
	var orgID *uuid.UUID
	if err := row.Scan(&u.ID, &orgID, &u.DepartmentID, &u.Email, &u.Name, &u.Role, &u.Status,
		&u.AvatarURL, &u.EmailVerified, &u.AuthProvider); err != nil {
		return domain.User{}, err
	}
	if orgID != nil {
		u.OrgID = *orgID
	}
	return u, nil
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "org"
	}
	return out
}

func (s *ServiceImpl) Authenticate(ctx context.Context, token string) (domain.User, error) {
	claims := &jwtClaims{}
	t, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return s.jwtSecret, nil
	})
	if err != nil || !t.Valid {
		return domain.User{}, errors.New("invalid token")
	}
	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return domain.User{}, errors.New("invalid token claims")
	}
	var u domain.User
	var orgID *uuid.UUID // NULL for pre-onboarding users
	err = s.db.QueryRow(ctx,
		`SELECT id, org_id, department_id, email, name, role, status, avatar_url, email_verified, auth_provider
		   FROM users WHERE id = $1 AND status = 'active'`,
		userID).Scan(&u.ID, &orgID, &u.DepartmentID, &u.Email, &u.Name, &u.Role, &u.Status,
		&u.AvatarURL, &u.EmailVerified, &u.AuthProvider)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, errors.New("user not found")
	}
	if orgID != nil {
		u.OrgID = *orgID
	}
	return u, err
}

func (s *ServiceImpl) CreateUser(ctx context.Context, orgID uuid.UUID, email, name string, role domain.Role) (domain.User, error) {
	tempPw := randomToken()
	hash, err := bcrypt.GenerateFromPassword([]byte(tempPw), bcrypt.DefaultCost)
	if err != nil {
		return domain.User{}, err
	}
	var u domain.User
	err = s.db.QueryRow(ctx,
		`INSERT INTO users (org_id, email, password_hash, name, role)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, org_id, email, name, role, status`,
		orgID, email, string(hash), name, role).Scan(&u.ID, &u.OrgID, &u.Email, &u.Name, &u.Role, &u.Status)
	if err != nil {
		return domain.User{}, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

func (s *ServiceImpl) ListUsers(ctx context.Context, orgID uuid.UUID) ([]domain.User, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, org_id, department_id, email, name, role, status FROM users WHERE org_id = $1 ORDER BY name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.User
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.ID, &u.OrgID, &u.DepartmentID, &u.Email, &u.Name, &u.Role, &u.Status); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ── Enrollment token management ───────────────────────────────────────────────

// IssueEnrollmentTokenDesc is like IssueEnrollmentToken but records a description.
func (s *ServiceImpl) IssueEnrollmentTokenDesc(ctx context.Context, orgID, createdBy uuid.UUID, desc string, ttl time.Duration, maxUses int) (string, error) {
	raw := randomToken()
	_, err := s.db.Exec(ctx,
		`INSERT INTO enrollment_tokens (org_id, token_hash, created_by, description, expires_at, max_uses)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		orgID, hashToken(raw), createdBy, desc, time.Now().Add(ttl), maxUses)
	if err != nil {
		return "", fmt.Errorf("insert token: %w", err)
	}
	return raw, nil
}

// ListEnrollmentTokens returns all tokens for an org with a computed status.
func (s *ServiceImpl) ListEnrollmentTokens(ctx context.Context, orgID uuid.UUID) ([]domain.EnrollmentToken, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, org_id, description, created_by, max_uses, uses, expires_at,
		        last_used_at, revoked_at, created_at
		   FROM enrollment_tokens WHERE org_id=$1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.EnrollmentToken{}
	now := time.Now()
	for rows.Next() {
		var t domain.EnrollmentToken
		if err := rows.Scan(&t.ID, &t.OrgID, &t.Description, &t.CreatedBy, &t.MaxUses,
			&t.Uses, &t.ExpiresAt, &t.LastUsedAt, &t.RevokedAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		switch {
		case t.RevokedAt != nil:
			t.Status = "revoked"
		case now.After(t.ExpiresAt):
			t.Status = "expired"
		case t.Uses >= t.MaxUses:
			t.Status = "exhausted"
		default:
			t.Status = "active"
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeEnrollmentToken marks a token revoked (idempotent).
func (s *ServiceImpl) RevokeEnrollmentToken(ctx context.Context, orgID, tokenID uuid.UUID) error {
	ct, err := s.db.Exec(ctx,
		`UPDATE enrollment_tokens SET revoked_at=now()
		   WHERE id=$1 AND org_id=$2 AND revoked_at IS NULL`, tokenID, orgID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("token not found or already revoked")
	}
	return nil
}

func (s *ServiceImpl) IssueEnrollmentToken(ctx context.Context, orgID, createdBy uuid.UUID, ttl time.Duration, maxUses int) (string, error) {
	raw := randomToken()
	hash := hashToken(raw)
	expires := time.Now().Add(ttl)
	_, err := s.db.Exec(ctx,
		`INSERT INTO enrollment_tokens (org_id, token_hash, created_by, expires_at, max_uses)
		 VALUES ($1, $2, $3, $4, $5)`,
		orgID, hash, createdBy, expires, maxUses)
	if err != nil {
		return "", fmt.Errorf("insert token: %w", err)
	}
	return raw, nil
}

func (s *ServiceImpl) EnrollAgent(ctx context.Context, rawToken, publicKey string, node domain.Node) (uuid.UUID, string, error) {
	return uuid.Nil, "", errors.New("use agentgw enrollment path")
}

func (s *ServiceImpl) BootstrapAdmin(ctx context.Context, spec string) error {
	var count int
	_ = s.db.QueryRow(ctx, `SELECT count(*) FROM users LIMIT 1`).Scan(&count)
	if count > 0 {
		return nil
	}
	// spec format: "email:password"
	var email, password string
	for i, ch := range spec {
		if ch == ':' {
			email = spec[:i]
			password = spec[i+1:]
			break
		}
	}
	if email == "" || password == "" {
		return errors.New("bootstrap spec must be email:password")
	}
	// Get the first org
	var orgID uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT id FROM organizations LIMIT 1`).Scan(&orgID); err != nil {
		return fmt.Errorf("no org found: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx,
		`INSERT INTO users (org_id, email, password_hash, name, role)
		 VALUES ($1, $2, $3, 'Admin', 'admin')
		 ON CONFLICT (org_id, email) DO NOTHING`,
		orgID, email, string(hash))
	return err
}

func (s *ServiceImpl) issueJWT(u domain.User) (string, error) {
	oid := ""
	if u.OrgID != uuid.Nil { // pre-onboarding users have no org claim
		oid = u.OrgID.String()
	}
	claims := &jwtClaims{
		UserID: u.ID.String(),
		OrgID:  oid,
		Role:   string(u.Role),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.sessionTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.jwtSecret)
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}
