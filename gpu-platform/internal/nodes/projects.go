package nodes

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

// ErrProjectForbidden is returned when a non-member (non-admin) touches a
// restricted project — the client-isolation boundary (Phase 6).
var ErrProjectForbidden = errors.New("you do not have access to this project")

// ProjectRow is the list projection. MemberCount feeds the projects UI; Member
// reports whether the viewer belongs to the project (drives "request access" UX).
type ProjectRow struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Visibility  string     `json:"visibility"`
	ArchivedAt  *time.Time `json:"archived_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	MemberCount int        `json:"member_count"`
	Member      bool       `json:"member"`
}

func (s *Service) ListProjects(ctx context.Context, orgID, viewerID uuid.UUID) ([]ProjectRow, error) {
	rows, err := s.repo.pool.Query(ctx,
		`SELECT p.id, p.name, p.description, p.visibility, p.archived_at, p.created_at,
		        (SELECT count(*) FROM project_members m WHERE m.project_id = p.id),
		        EXISTS (SELECT 1 FROM project_members m WHERE m.project_id = p.id AND m.user_id = $2)
		   FROM projects p WHERE p.org_id=$1 ORDER BY p.archived_at NULLS FIRST, p.name`,
		orgID, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectRow
	for rows.Next() {
		var p ProjectRow
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Visibility, &p.ArchivedAt,
			&p.CreatedAt, &p.MemberCount, &p.Member); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) CreateProject(ctx context.Context, orgID uuid.UUID, name, description string, createdBy uuid.UUID) (domain.Project, error) {
	var p domain.Project
	err := s.repo.pool.QueryRow(ctx,
		`INSERT INTO projects (org_id, name, description, created_by) VALUES ($1,$2,$3,$4)
		 ON CONFLICT (org_id, name) DO UPDATE SET name=EXCLUDED.name
		 RETURNING id, org_id, name, description, visibility, created_by, archived_at, created_at`,
		orgID, strings.TrimSpace(name), strings.TrimSpace(description), createdBy).
		Scan(&p.ID, &p.OrgID, &p.Name, &p.Description, &p.Visibility, &p.CreatedBy, &p.ArchivedAt, &p.CreatedAt)
	if err != nil {
		return domain.Project{}, fmt.Errorf("create project: %w", err)
	}
	return p, nil
}

// EnsureDefaultProject returns the org's Default project, creating it on demand.
// Every workload belongs to a project (Phase 6) — this is the fallback home.
func (s *Service) EnsureDefaultProject(ctx context.Context, orgID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.repo.pool.QueryRow(ctx,
		`INSERT INTO projects (org_id, name, description)
		 VALUES ($1, 'Default', 'Default project for workloads launched without an explicit project')
		 ON CONFLICT (org_id, name) DO UPDATE SET name = EXCLUDED.name
		 RETURNING id`, orgID).Scan(&id)
	return id, err
}

func (s *Service) GetProject(ctx context.Context, orgID, id uuid.UUID) (domain.Project, error) {
	var p domain.Project
	err := s.repo.pool.QueryRow(ctx,
		`SELECT id, org_id, name, description, visibility, created_by, archived_at, created_at
		   FROM projects WHERE id=$1 AND org_id=$2`, id, orgID).
		Scan(&p.ID, &p.OrgID, &p.Name, &p.Description, &p.Visibility, &p.CreatedBy, &p.ArchivedAt, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Project{}, ErrNotFound
	}
	return p, err
}

// UpdateProject patches name/description/visibility (nil = unchanged).
func (s *Service) UpdateProject(ctx context.Context, orgID, id uuid.UUID, name, description, visibility *string) (domain.Project, error) {
	if visibility != nil && *visibility != "open" && *visibility != "restricted" {
		return domain.Project{}, fmt.Errorf("visibility must be 'open' or 'restricted'")
	}
	var p domain.Project
	err := s.repo.pool.QueryRow(ctx,
		`UPDATE projects SET
		    name        = COALESCE(NULLIF(TRIM($3), ''), name),
		    description = COALESCE($4, description),
		    visibility  = COALESCE($5, visibility)
		  WHERE id=$1 AND org_id=$2
		  RETURNING id, org_id, name, description, visibility, created_by, archived_at, created_at`,
		id, orgID, deref(name), description, visibility).
		Scan(&p.ID, &p.OrgID, &p.Name, &p.Description, &p.Visibility, &p.CreatedBy, &p.ArchivedAt, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Project{}, ErrNotFound
	}
	return p, err
}

// SetProjectArchived archives (true) / restores (false) a project. Archived
// projects refuse new launches; existing workloads keep running.
func (s *Service) SetProjectArchived(ctx context.Context, orgID, id uuid.UUID, archived bool) error {
	q := `UPDATE projects SET archived_at = now() WHERE id=$1 AND org_id=$2 AND archived_at IS NULL`
	if !archived {
		q = `UPDATE projects SET archived_at = NULL WHERE id=$1 AND org_id=$2`
	}
	ct, err := s.repo.pool.Exec(ctx, q, id, orgID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── Project members ───────────────────────────────────────────────────────────

func (s *Service) ListProjectMembers(ctx context.Context, orgID, projectID uuid.UUID) ([]domain.ProjectMember, error) {
	rows, err := s.repo.pool.Query(ctx,
		`SELECT m.project_id, m.user_id, u.email, u.name, m.added_by, m.created_at
		   FROM project_members m
		   JOIN projects p ON p.id = m.project_id
		   JOIN users u ON u.id = m.user_id
		  WHERE m.project_id=$1 AND p.org_id=$2
		  ORDER BY m.created_at`, projectID, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ProjectMember, 0)
	for rows.Next() {
		var m domain.ProjectMember
		if err := rows.Scan(&m.ProjectID, &m.UserID, &m.Email, &m.Name, &m.AddedBy, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddProjectMember grants a user access to a project. The INSERT joins both rows
// against the caller's org, so cross-org grants are impossible by construction.
func (s *Service) AddProjectMember(ctx context.Context, orgID, projectID, userID, addedBy uuid.UUID) error {
	ct, err := s.repo.pool.Exec(ctx,
		`INSERT INTO project_members (project_id, user_id, added_by)
		 SELECT p.id, u.id, $4
		   FROM projects p, users u
		  WHERE p.id=$1 AND p.org_id=$2 AND u.id=$3 AND u.org_id=$2
		 ON CONFLICT DO NOTHING`, projectID, orgID, userID, addedBy)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		// Already a member is fine (idempotent); project/user outside the org is not.
		var exists bool
		_ = s.repo.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM project_members WHERE project_id=$1 AND user_id=$2)`,
			projectID, userID).Scan(&exists)
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}

func (s *Service) RemoveProjectMember(ctx context.Context, orgID, projectID, userID uuid.UUID) error {
	ct, err := s.repo.pool.Exec(ctx,
		`DELETE FROM project_members m USING projects p
		  WHERE m.project_id = p.id AND p.id=$1 AND p.org_id=$2 AND m.user_id=$3`,
		projectID, orgID, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AuthorizeProjectUse is the isolation gate for launching into a project: open
// projects admit any org member; restricted projects admit explicit members and
// org admins/owners; archived projects refuse new use entirely.
func (s *Service) AuthorizeProjectUse(ctx context.Context, orgID, projectID, userID uuid.UUID, isAdmin bool) error {
	p, err := s.GetProject(ctx, orgID, projectID)
	if err != nil {
		return err
	}
	if p.ArchivedAt != nil {
		return fmt.Errorf("%w: project is archived", ErrProjectForbidden)
	}
	if p.Visibility != "restricted" || isAdmin {
		return nil
	}
	var member bool
	if err := s.repo.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM project_members WHERE project_id=$1 AND user_id=$2)`,
		projectID, userID).Scan(&member); err != nil {
		return err
	}
	if !member {
		return ErrProjectForbidden
	}
	return nil
}

// CanViewProject is the read-side gate: like AuthorizeProjectUse but without the
// archived check (an archived project's history stays visible to its members).
func (s *Service) CanViewProject(ctx context.Context, orgID, projectID, userID uuid.UUID, isAdmin bool) (bool, error) {
	if isAdmin {
		return true, nil
	}
	var visible bool
	err := s.repo.pool.QueryRow(ctx,
		`SELECT p.visibility <> 'restricted'
		        OR EXISTS (SELECT 1 FROM project_members m WHERE m.project_id = p.id AND m.user_id = $3)
		   FROM projects p WHERE p.id=$1 AND p.org_id=$2`,
		projectID, orgID, userID).Scan(&visible)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return visible, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
