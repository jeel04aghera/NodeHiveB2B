//go:build integration

// Phase 3 tests: organization memberships, role hierarchy, email invitations and join
// codes. Verifies single-active-org is preserved (a user already in an org can't join
// another), role-change/removal rules, last-owner protection, and join-code limits.
package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/nodehive/gpu-platform/internal/domain"
	"github.com/nodehive/gpu-platform/internal/identity"
)

// joinViaInvite registers a pre-onboarding user and has them accept an invite into orgID.
func joinViaInvite(t *testing.T, svc identity.Service, ctx context.Context, orgID, ownerID uuid.UUID, email string, role domain.Role) uuid.UUID {
	t.Helper()
	_, u, err := svc.RegisterPending(ctx, email, "", "password123")
	if err != nil {
		t.Fatalf("register pending %s: %v", email, err)
	}
	raw, _, err := svc.CreateInvitation(ctx, orgID, ownerID, email, role)
	if err != nil {
		t.Fatalf("invite %s: %v", email, err)
	}
	_, joined, err := svc.AcceptInvitation(ctx, u.ID, raw)
	if err != nil {
		t.Fatalf("accept %s: %v", email, err)
	}
	if !joined.Onboarded() || joined.OrgID != orgID {
		t.Fatalf("expected %s to join org %v, got %v", email, orgID, joined.OrgID)
	}
	return joined.ID
}

func ownerOf(t *testing.T, svc identity.Service, ctx context.Context, orgName, email string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	_, u, err := svc.Register(ctx, orgName, email, "Owner", "password123")
	if err != nil {
		t.Fatalf("register %s: %v", orgName, err)
	}
	if u.Role != domain.RoleOwner {
		t.Fatalf("org creator must be owner, got %q", u.Role)
	}
	return u.OrgID, u.ID
}

func TestRegisterCreatesOwnerMembership(t *testing.T) {
	svc, pool, ctx := newSvc(t)
	orgID, ownerID := ownerOf(t, svc, ctx, "Acme", "owner@acme.com")

	members, err := svc.ListMembers(ctx, orgID)
	if err != nil || len(members) != 1 {
		t.Fatalf("expected 1 member, got %d err=%v", len(members), err)
	}
	if members[0].UserID != ownerID || members[0].Role != domain.RoleOwner {
		t.Errorf("owner membership wrong: %+v", members[0])
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM organization_members WHERE org_id=$1 AND role='owner'`, orgID).Scan(&n)
	if n != 1 {
		t.Errorf("expected 1 owner row, got %d", n)
	}
}

func TestInviteAcceptFlow(t *testing.T) {
	svc, _, ctx := newSvc(t)
	orgID, ownerID := ownerOf(t, svc, ctx, "Acme", "owner@acme.com")

	// Invite + accept.
	uid := joinViaInvite(t, svc, ctx, orgID, ownerID, "member@acme.com", domain.RoleMember)
	members, _ := svc.ListMembers(ctx, orgID)
	if len(members) != 2 {
		t.Fatalf("expected 2 members after accept, got %d", len(members))
	}

	// Re-inviting an existing member is rejected.
	if _, _, err := svc.CreateInvitation(ctx, orgID, ownerID, "member@acme.com", domain.RoleMember); !errors.Is(err, identity.ErrAlreadyMember) {
		t.Errorf("inviting an existing member should fail, got %v", err)
	}

	// A user already in an org can't accept another org's invite (single active org).
	org2, owner2 := ownerOf(t, svc, ctx, "Beta", "owner@beta.com")
	raw, _, _ := svc.CreateInvitation(ctx, org2, owner2, "member@acme.com", domain.RoleMember)
	if _, _, err := svc.AcceptInvitation(ctx, uid, raw); !errors.Is(err, identity.ErrAlreadyMember) {
		t.Errorf("cross-org accept must fail with ErrAlreadyMember, got %v", err)
	}
}

func TestInviteEmailMismatchRejected(t *testing.T) {
	svc, _, ctx := newSvc(t)
	orgID, ownerID := ownerOf(t, svc, ctx, "Acme", "owner@acme.com")

	// Account exists for a DIFFERENT email than the invite.
	_, u, _ := svc.RegisterPending(ctx, "someone@else.com", "", "password123")
	raw, _, _ := svc.CreateInvitation(ctx, orgID, ownerID, "intended@acme.com", domain.RoleMember)
	if _, _, err := svc.AcceptInvitation(ctx, u.ID, raw); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("accepting an invite for another email must be forbidden, got %v", err)
	}
}

func TestRevokeInvitationBlocksAccept(t *testing.T) {
	svc, _, ctx := newSvc(t)
	orgID, ownerID := ownerOf(t, svc, ctx, "Acme", "owner@acme.com")
	_, u, _ := svc.RegisterPending(ctx, "x@acme.com", "", "password123")
	raw, inv, _ := svc.CreateInvitation(ctx, orgID, ownerID, "x@acme.com", domain.RoleMember)
	if err := svc.RevokeInvitation(ctx, orgID, inv.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := svc.AcceptInvitation(ctx, u.ID, raw); !errors.Is(err, identity.ErrInvitationInvalid) {
		t.Fatalf("revoked invite must not be acceptable, got %v", err)
	}
}

func TestChangeMemberRoleRules(t *testing.T) {
	svc, _, ctx := newSvc(t)
	orgID, ownerID := ownerOf(t, svc, ctx, "Acme", "owner@acme.com")
	adminID := joinViaInvite(t, svc, ctx, orgID, ownerID, "admin@acme.com", domain.RoleAdmin)
	memberID := joinViaInvite(t, svc, ctx, orgID, ownerID, "member@acme.com", domain.RoleMember)

	// Owner can promote a member to admin.
	if err := svc.ChangeMemberRole(ctx, orgID, ownerID, domain.RoleOwner, memberID, domain.RoleAdmin); err != nil {
		t.Fatalf("owner promote member->admin: %v", err)
	}
	// Admin cannot grant owner.
	if err := svc.ChangeMemberRole(ctx, orgID, adminID, domain.RoleAdmin, memberID, domain.RoleOwner); !errors.Is(err, identity.ErrForbidden) {
		t.Errorf("admin granting owner must be forbidden, got %v", err)
	}
	// Nobody can change their own role.
	if err := svc.ChangeMemberRole(ctx, orgID, ownerID, domain.RoleOwner, ownerID, domain.RoleAdmin); !errors.Is(err, identity.ErrForbidden) {
		t.Errorf("self role-change must be forbidden, got %v", err)
	}
	// Cannot demote the last owner.
	if err := svc.ChangeMemberRole(ctx, orgID, ownerID, domain.RoleOwner, ownerID, domain.RoleMember); err == nil {
		t.Errorf("self-demote of last owner should be forbidden")
	}
	// Owner can transfer: promote admin to owner, then there are two owners.
	if err := svc.ChangeMemberRole(ctx, orgID, ownerID, domain.RoleOwner, adminID, domain.RoleOwner); err != nil {
		t.Fatalf("owner transfer: %v", err)
	}
	if err := svc.ChangeMemberRole(ctx, orgID, adminID, domain.RoleOwner, ownerID, domain.RoleAdmin); err != nil {
		t.Fatalf("now-second-owner demoting first owner should work: %v", err)
	}
}

func TestRemoveMemberRules(t *testing.T) {
	svc, pool, ctx := newSvc(t)
	orgID, ownerID := ownerOf(t, svc, ctx, "Acme", "owner@acme.com")
	adminID := joinViaInvite(t, svc, ctx, orgID, ownerID, "admin@acme.com", domain.RoleAdmin)
	memberID := joinViaInvite(t, svc, ctx, orgID, ownerID, "member@acme.com", domain.RoleMember)

	// Admin cannot remove an owner.
	if err := svc.RemoveMember(ctx, orgID, adminID, domain.RoleAdmin, ownerID); !errors.Is(err, identity.ErrForbidden) {
		t.Errorf("admin removing owner must be forbidden, got %v", err)
	}
	// Cannot remove yourself here.
	if err := svc.RemoveMember(ctx, orgID, ownerID, domain.RoleOwner, ownerID); !errors.Is(err, identity.ErrForbidden) {
		t.Errorf("self-removal must be forbidden, got %v", err)
	}
	// Owner removes the member: they revert to pre-onboarding.
	if err := svc.RemoveMember(ctx, orgID, ownerID, domain.RoleOwner, memberID); err != nil {
		t.Fatalf("owner remove member: %v", err)
	}
	var orgPtr *uuid.UUID
	_ = pool.QueryRow(ctx, `SELECT org_id FROM users WHERE id=$1`, memberID).Scan(&orgPtr)
	if orgPtr != nil {
		t.Errorf("removed member should have NULL org_id, got %v", *orgPtr)
	}
	members, _ := svc.ListMembers(ctx, orgID)
	if len(members) != 2 { // owner + admin
		t.Errorf("expected 2 members after removal, got %d", len(members))
	}
	// Last-owner protection: can't remove the only owner.
	if err := svc.RemoveMember(ctx, orgID, ownerID, domain.RoleOwner, ownerID); err == nil {
		t.Error("removing the last owner (self) must fail")
	}
}

func TestJoinCodeFlow(t *testing.T) {
	svc, _, ctx := newSvc(t)
	orgID, ownerID := ownerOf(t, svc, ctx, "Acme", "owner@acme.com")

	raw, err := svc.CreateJoinCode(ctx, orgID, ownerID, "eng", 0, 1) // max 1 use
	if err != nil {
		t.Fatalf("create join code: %v", err)
	}

	// First user joins as a member.
	_, u1, _ := svc.RegisterPending(ctx, "j1@acme.com", "", "password123")
	_, joined, err := svc.JoinViaCode(ctx, u1.ID, raw)
	if err != nil || joined.OrgID != orgID || joined.Role != domain.RoleMember {
		t.Fatalf("join via code: org=%v role=%v err=%v", joined.OrgID, joined.Role, err)
	}

	// Second user exceeds max_uses → rejected.
	_, u2, _ := svc.RegisterPending(ctx, "j2@acme.com", "", "password123")
	if _, _, err := svc.JoinViaCode(ctx, u2.ID, raw); !errors.Is(err, identity.ErrJoinCodeInvalid) {
		t.Fatalf("exhausted code must be invalid, got %v", err)
	}

	// A revoked code can't be used.
	raw2, _ := svc.CreateJoinCode(ctx, orgID, ownerID, "eng2", 0, 0)
	codes, _ := svc.ListJoinCodes(ctx, orgID)
	var id2 uuid.UUID
	for _, c := range codes {
		if c.Description == "eng2" {
			id2 = c.ID
		}
	}
	_ = svc.RevokeJoinCode(ctx, orgID, id2)
	_, u3, _ := svc.RegisterPending(ctx, "j3@acme.com", "", "password123")
	if _, _, err := svc.JoinViaCode(ctx, u3.ID, raw2); !errors.Is(err, identity.ErrJoinCodeInvalid) {
		t.Fatalf("revoked code must be invalid, got %v", err)
	}
}

func TestRegisterPendingHasNoOrg(t *testing.T) {
	svc, _, ctx := newSvc(t)
	_, u, err := svc.RegisterPending(ctx, "pending@x.com", "Pending", "password123")
	if err != nil {
		t.Fatalf("register pending: %v", err)
	}
	if u.Onboarded() {
		t.Errorf("register-pending user must have no org, got %v", u.OrgID)
	}
}
