package nodes

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/nodehive/gpu-platform/internal/domain"
)

// ListTemplates returns built-in templates (org_id NULL) plus this org's own,
// enabled only, ordered for display.
func (s *Service) ListTemplates(ctx context.Context, orgID uuid.UUID) ([]domain.Template, error) {
	rows, err := s.repo.pool.Query(ctx, `
		SELECT id, org_id, name, description, base_image, software, version, tags,
		       default_expose_ssh, default_expose_jupyter, enabled, created_at
		  FROM templates
		 WHERE enabled = true AND (org_id IS NULL OR org_id = $1)
		 ORDER BY (org_id IS NULL) DESC, name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Template{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTemplate returns one template visible to the org (built-in or owned).
func (s *Service) GetTemplate(ctx context.Context, orgID, id uuid.UUID) (domain.Template, error) {
	row := s.repo.pool.QueryRow(ctx, `
		SELECT id, org_id, name, description, base_image, software, version, tags,
		       default_expose_ssh, default_expose_jupyter, enabled, created_at
		  FROM templates
		 WHERE id = $1 AND (org_id IS NULL OR org_id = $2)`, id, orgID)
	return scanTemplate(row)
}

// CreateTemplate adds a per-org template.
func (s *Service) CreateTemplate(ctx context.Context, orgID uuid.UUID, t domain.Template) (domain.Template, error) {
	software, _ := json.Marshal(t.Software)
	tags, _ := json.Marshal(t.Tags)
	if len(software) == 0 {
		software = []byte("[]")
	}
	if len(tags) == 0 {
		tags = []byte("[]")
	}
	row := s.repo.pool.QueryRow(ctx, `
		INSERT INTO templates (org_id, name, description, base_image, software, version, tags,
		                       default_expose_ssh, default_expose_jupyter, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,true)
		ON CONFLICT (org_id, name) WHERE org_id IS NOT NULL DO UPDATE SET
		  description=EXCLUDED.description, base_image=EXCLUDED.base_image,
		  software=EXCLUDED.software, version=EXCLUDED.version, tags=EXCLUDED.tags,
		  default_expose_ssh=EXCLUDED.default_expose_ssh,
		  default_expose_jupyter=EXCLUDED.default_expose_jupyter, enabled=true
		RETURNING id, org_id, name, description, base_image, software, version, tags,
		          default_expose_ssh, default_expose_jupyter, enabled, created_at`,
		orgID, t.Name, t.Description, t.BaseImage, software, t.Version, tags,
		t.DefaultExposeSSH, t.DefaultExposeJupyter)
	created, err := scanTemplate(row)
	if err != nil {
		return domain.Template{}, fmt.Errorf("create template: %w", err)
	}
	return created, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTemplate(row scanner) (domain.Template, error) {
	var t domain.Template
	var orgID *uuid.UUID
	var software, tags []byte
	if err := row.Scan(&t.ID, &orgID, &t.Name, &t.Description, &t.BaseImage,
		&software, &t.Version, &tags, &t.DefaultExposeSSH, &t.DefaultExposeJupyter,
		&t.Enabled, &t.CreatedAt); err != nil {
		return domain.Template{}, err
	}
	t.OrgID = orgID
	t.BuiltIn = orgID == nil
	_ = json.Unmarshal(software, &t.Software)
	_ = json.Unmarshal(tags, &t.Tags)
	if t.Software == nil {
		t.Software = []domain.TemplateSoftware{}
	}
	if t.Tags == nil {
		t.Tags = []string{}
	}
	return t, nil
}
