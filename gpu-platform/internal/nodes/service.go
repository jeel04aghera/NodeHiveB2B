package nodes

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
)

// Service is the business layer used by both the gRPC gateway and the HTTP API.
type Service struct{ repo *Repo }

func NewService(repo *Repo) *Service { return &Service{repo: repo} }

// Enroll issues a fresh credential, stores only its hash, and registers/updates the
// node. The plaintext credential is returned to the agent exactly once.
func (s *Service) Enroll(ctx context.Context, token, fingerprint string, info NodeInfo) (nodeID string, credential string, err error) {
	credential = randomToken()
	id, _, err := s.repo.Enroll(ctx, hashToken(token), fingerprint, info, hashToken(credential))
	if err != nil {
		return "", "", err
	}
	return id.String(), credential, nil
}

// AuthenticateAgent validates a bearer credential and returns the node identity.
func (s *Service) AuthenticateAgent(ctx context.Context, credential string) (nodeID, orgID uuid.UUID, err error) {
	return s.repo.AuthenticateAgent(ctx, hashToken(credential))
}

func (s *Service) Heartbeat(ctx context.Context, orgID, nodeID uuid.UUID, agentVersion string, gpuCount int, meanUtil float32) error {
	return s.repo.RecordHeartbeat(ctx, orgID, nodeID, agentVersion, gpuCount, meanUtil)
}

func (s *Service) MarkOffline(ctx context.Context, nodeID uuid.UUID) error {
	return s.repo.SetNodeStatus(ctx, nodeID, "offline")
}

func (s *Service) List(ctx context.Context, orgID uuid.UUID) ([]NodeView, error) {
	return s.repo.ListNodes(ctx, orgID)
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (NodeView, error) {
	return s.repo.GetNodeByID(ctx, id)
}

// EnsureDevToken seeds a long-lived enrollment token for the dev org so that
// `make agent` works out of the box.
func (s *Service) EnsureDevToken(ctx context.Context, orgSlug, token string) error {
	orgID, err := s.repo.OrgIDBySlug(ctx, orgSlug)
	if err != nil {
		return err
	}
	return s.repo.EnsureEnrollmentToken(ctx, orgID, hashToken(token), time.Now().Add(10*365*24*time.Hour), 1_000_000)
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
