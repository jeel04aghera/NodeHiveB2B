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
	// Find user by email (any org — for single-org V1 we match on email alone)
	var u domain.User
	var hash string
	err := s.db.QueryRow(ctx,
		`SELECT u.id, u.org_id, u.department_id, u.email, u.password_hash, u.name, u.role, u.status
		   FROM users u WHERE u.email = $1 AND u.status = 'active' LIMIT 1`,
		email).Scan(&u.ID, &u.OrgID, &u.DepartmentID, &u.Email, &hash, &u.Name, &u.Role, &u.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.User{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", domain.User{}, fmt.Errorf("lookup user: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
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

	// Unique slug derived from the org name + short random suffix.
	slug := slugify(orgName) + "-" + randomToken()[:6]
	var orgID uuid.UUID
	if err := tx.QueryRow(ctx,
		`INSERT INTO organizations (name, slug, settings)
		 VALUES ($1, $2, '{"currency":"USD","default_rate":0.5}'::jsonb)
		 RETURNING id`, orgName, slug).Scan(&orgID); err != nil {
		return "", domain.User{}, fmt.Errorf("create org: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", domain.User{}, err
	}
	if name == "" {
		name = "Admin"
	}
	var u domain.User
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (org_id, email, password_hash, name, role)
		 VALUES ($1, $2, $3, $4, 'admin')
		 RETURNING id, org_id, email, name, role, status`,
		orgID, email, string(hash), name).Scan(&u.ID, &u.OrgID, &u.Email, &u.Name, &u.Role, &u.Status); err != nil {
		return "", domain.User{}, fmt.Errorf("create admin: %w", err)
	}

	// Default rate cards so chargeback has prices once GPUs enroll.
	for _, rc := range []struct {
		model string
		rate  float64
	}{
		{"NVIDIA RTX 4090", 0.34}, {"NVIDIA RTX 6000 Ada", 0.69},
		{"NVIDIA A100", 0.89}, {"NVIDIA H100", 2.69},
	} {
		_, _ = tx.Exec(ctx,
			`INSERT INTO rate_cards (org_id, gpu_model, rate_per_gpu_hour, currency) VALUES ($1,$2,$3,'USD')`,
			orgID, rc.model, rc.rate)
	}

	// Welcome credit grant in INR (mirrors the ledger migration seed).
	_, _ = tx.Exec(ctx,
		`INSERT INTO credit_ledger (org_id, delta, balance, kind, description)
		 VALUES ($1, 50000.0000, 50000.0000, 'grant', 'Welcome credit')`, orgID)

	if err := tx.Commit(ctx); err != nil {
		return "", domain.User{}, err
	}
	token, err := s.issueJWT(u)
	return token, u, err
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
	err = s.db.QueryRow(ctx,
		`SELECT id, org_id, department_id, email, name, role, status FROM users WHERE id = $1 AND status = 'active'`,
		userID).Scan(&u.ID, &u.OrgID, &u.DepartmentID, &u.Email, &u.Name, &u.Role, &u.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, errors.New("user not found")
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
	claims := &jwtClaims{
		UserID: u.ID.String(),
		OrgID:  u.OrgID.String(),
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
