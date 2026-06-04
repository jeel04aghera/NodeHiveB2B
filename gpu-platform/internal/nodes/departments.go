package nodes

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/nodehive/gpu-platform/internal/domain"
)

// DepartmentView is a department plus live rollups for the org-structure UI.
type DepartmentView struct {
	domain.Department
	UserCount     int `json:"user_count"`
	WorkloadCount int `json:"workload_count"` // active (pending/running) workloads
}

func (s *Service) ListDepartments(ctx context.Context, orgID uuid.UUID) ([]DepartmentView, error) {
	rows, err := s.repo.pool.Query(ctx, `
		SELECT d.id, d.org_id, d.name, d.description, d.created_at,
		       (SELECT count(*) FROM users u WHERE u.department_id = d.id),
		       (SELECT count(*) FROM workloads w WHERE w.department_id = d.id
		          AND w.status IN ('pending','running'))
		  FROM departments d
		 WHERE d.org_id = $1
		 ORDER BY d.name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DepartmentView{}
	for rows.Next() {
		var d DepartmentView
		if err := rows.Scan(&d.ID, &d.OrgID, &d.Name, &d.Description, &d.CreatedAt,
			&d.UserCount, &d.WorkloadCount); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Service) CreateDepartment(ctx context.Context, orgID uuid.UUID, name, description string) (domain.Department, error) {
	var d domain.Department
	err := s.repo.pool.QueryRow(ctx,
		`INSERT INTO departments (org_id, name, description) VALUES ($1,$2,$3)
		 ON CONFLICT (org_id, name) DO UPDATE SET description=EXCLUDED.description
		 RETURNING id, org_id, name, description, created_at`,
		orgID, name, description).Scan(&d.ID, &d.OrgID, &d.Name, &d.Description, &d.CreatedAt)
	if err != nil {
		return domain.Department{}, fmt.Errorf("create department: %w", err)
	}
	return d, nil
}

// AssignUserDepartment sets a user's department (admin action).
func (s *Service) AssignUserDepartment(ctx context.Context, orgID, userID uuid.UUID, deptID *uuid.UUID) error {
	ct, err := s.repo.pool.Exec(ctx,
		`UPDATE users SET department_id=$1 WHERE id=$2 AND org_id=$3`, deptID, userID, orgID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}
