package ops

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrOverbooked = errors.New("not enough capacity for that GPU type in the requested window")

type Reservation struct {
	ID          string    `json:"id"`
	GPUModel    string    `json:"gpu_model"`
	GPUCount    int       `json:"gpu_count"`
	StartAt     time.Time `json:"start_at"`
	EndAt       time.Time `json:"end_at"`
	Status      string    `json:"status"` // upcoming | active | expired | cancelled
	ProjectName string    `json:"project_name,omitempty"`
	OwnerEmail  string    `json:"owner_email,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type CreateReservationReq struct {
	GPUModel  string     `json:"gpu_model"`
	GPUCount  int        `json:"gpu_count"`
	StartAt   time.Time  `json:"start_at"`
	EndAt     time.Time  `json:"end_at"`
	ProjectID *uuid.UUID `json:"project_id,omitempty"`
}

// liveStatus computes a reservation's status from its window (stored status only
// distinguishes cancelled).
func liveStatus(stored string, start, end time.Time) string {
	if stored == "cancelled" {
		return "cancelled"
	}
	now := time.Now()
	switch {
	case now.After(end):
		return "expired"
	case now.Before(start):
		return "upcoming"
	default:
		return "active"
	}
}

// totalGPUs returns how many GPUs of a model the org owns (idle+in_use).
func (s *Service) totalGPUs(ctx context.Context, orgID uuid.UUID, model string) int {
	var n int
	_ = s.db.QueryRow(ctx,
		`SELECT count(*) FROM gpus WHERE org_id=$1 AND ($2='any' OR model ILIKE '%'||$2||'%')`,
		orgID, model).Scan(&n)
	return n
}

// peakReserved returns the maximum GPUs of a model reserved at any instant that
// overlaps [start,end), across non-cancelled reservations (optionally excluding one).
func (s *Service) peakReserved(ctx context.Context, orgID uuid.UUID, model string, start, end time.Time, exclude *uuid.UUID) int {
	// Conservative approximation: sum counts of all reservations overlapping the
	// window. (Exact instant-peak would need a sweep; sum is safe — never under-books.)
	q := `SELECT coalesce(sum(gpu_count),0) FROM reservations
	       WHERE org_id=$1 AND status<>'cancelled' AND gpu_model=$2
	         AND start_at < $4 AND end_at > $3`
	args := []any{orgID, model, start, end}
	if exclude != nil {
		q += ` AND id <> $5`
		args = append(args, *exclude)
	}
	var n int
	_ = s.db.QueryRow(ctx, q, args...).Scan(&n)
	return n
}

func (s *Service) CreateReservation(ctx context.Context, orgID, userID uuid.UUID, req CreateReservationReq) (Reservation, error) {
	if req.GPUCount <= 0 || !req.EndAt.After(req.StartAt) {
		return Reservation{}, errors.New("invalid reservation parameters")
	}
	total := s.totalGPUs(ctx, orgID, req.GPUModel)
	already := s.peakReserved(ctx, orgID, req.GPUModel, req.StartAt, req.EndAt, nil)
	if already+req.GPUCount > total {
		return Reservation{}, ErrOverbooked
	}
	var id string
	var created time.Time
	err := s.db.QueryRow(ctx,
		`INSERT INTO reservations (org_id, project_id, user_id, gpu_model, gpu_count, start_at, end_at, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,'upcoming') RETURNING id, created_at`,
		orgID, req.ProjectID, userID, req.GPUModel, req.GPUCount, req.StartAt, req.EndAt).Scan(&id, &created)
	if err != nil {
		return Reservation{}, err
	}
	return Reservation{
		ID: id, GPUModel: req.GPUModel, GPUCount: req.GPUCount,
		StartAt: req.StartAt, EndAt: req.EndAt,
		Status: liveStatus("upcoming", req.StartAt, req.EndAt), CreatedAt: created,
	}, nil
}

func (s *Service) ListReservations(ctx context.Context, orgID uuid.UUID) ([]Reservation, error) {
	rows, err := s.db.Query(ctx,
		`SELECT r.id, r.gpu_model, r.gpu_count, r.start_at, r.end_at, r.status,
		        coalesce(p.name,''), coalesce(u.email,''), r.created_at
		   FROM reservations r
		   LEFT JOIN projects p ON p.id=r.project_id
		   LEFT JOIN users u ON u.id=r.user_id
		  WHERE r.org_id=$1 ORDER BY r.start_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Reservation{}
	for rows.Next() {
		var r Reservation
		var stored string
		if err := rows.Scan(&r.ID, &r.GPUModel, &r.GPUCount, &r.StartAt, &r.EndAt, &stored,
			&r.ProjectName, &r.OwnerEmail, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Status = liveStatus(stored, r.StartAt, r.EndAt)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) CancelReservation(ctx context.Context, orgID, id uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE reservations SET status='cancelled' WHERE id=$1 AND org_id=$2`, id, orgID)
	return err
}

// ReservedNow returns GPUs reserved at this instant (for the Capacity page).
func (s *Service) ReservedNow(ctx context.Context, orgID uuid.UUID) int {
	now := time.Now()
	var n int
	_ = s.db.QueryRow(ctx,
		`SELECT coalesce(sum(gpu_count),0) FROM reservations
		  WHERE org_id=$1 AND status<>'cancelled' AND start_at <= $2 AND end_at > $2`,
		orgID, now).Scan(&n)
	return n
}
