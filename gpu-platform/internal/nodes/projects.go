package nodes

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/nodehive/gpu-platform/internal/domain"
)

type ProjectRow struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

func (s *Service) ListProjects(ctx context.Context, orgID uuid.UUID) ([]ProjectRow, error) {
	rows, err := s.repo.pool.Query(ctx,
		`SELECT id, name FROM projects WHERE org_id=$1 ORDER BY name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectRow
	for rows.Next() {
		var p ProjectRow
		if err := rows.Scan(&p.ID, &p.Name); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) CreateProject(ctx context.Context, orgID uuid.UUID, name string) (domain.Project, error) {
	var p domain.Project
	err := s.repo.pool.QueryRow(ctx,
		`INSERT INTO projects (org_id, name) VALUES ($1,$2)
		 ON CONFLICT (org_id, name) DO UPDATE SET name=EXCLUDED.name
		 RETURNING id, org_id, name, created_at`,
		orgID, name).Scan(&p.ID, &p.OrgID, &p.Name, &p.CreatedAt)
	if err != nil {
		return domain.Project{}, fmt.Errorf("create project: %w", err)
	}
	return p, nil
}
