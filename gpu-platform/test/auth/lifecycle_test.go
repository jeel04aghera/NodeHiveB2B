//go:build integration

// Phase 3.5 tests: leave organization, ownership transfer, leave-then-join, and invitation
// email delivery-status tracking. Reuses ownerOf / joinViaInvite from memberships_test.go.
package auth

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/nodehive/gpu-platform/internal/domain"
	"github.com/nodehive/gpu-platform/internal/identity"
)

func TestMemberCanLeave(t *testing.T) {
	svc, pool, ctx := newSvc(t)
	orgID, ownerID := ownerOf(t, svc, ctx, "Acme", "owner@acme.com")
	memberID := joinViaInvite(t, svc, ctx, orgID, ownerID, "member@acme.com", domain.RoleMember)

	_, user, err := svc.LeaveOrg(ctx, orgID, memberID, domain.RoleMember, nil)
	if err != nil {
		t.Fatalf("member leave: %v", err)
	}
	if user.Onboarded() {
		t.Errorf("after leaving, user must be pre-onboarding, got org %v", user.OrgID)
	}
	// Membership row gone; org now has just the owner.
	if members, _ := svc.ListMembers(ctx, orgID); len(members) != 1 {
		t.Errorf("expected 1 member after leave, got %d", len(members))
	}
	var orgPtr *uuid.UUID
	_ = pool.QueryRow(ctx, `SELECT org_id FROM users WHERE id=$1`, memberID).Scan(&orgPtr)
	if orgPtr != nil {
		t.Errorf("left user should have NULL org_id")
	}
}

func TestAdminCanLeave(t *testing.T) {
	svc, _, ctx := newSvc(t)
	orgID, ownerID := ownerOf(t, svc, ctx, "Acme", "owner@acme.com")
	adminID := joinViaInvite(t, svc, ctx, orgID, ownerID, "admin@acme.com", domain.RoleAdmin)

	if _, user, err := svc.LeaveOrg(ctx, orgID, adminID, domain.RoleAdmin, nil); err != nil || user.Onboarded() {
		t.Fatalf("admin leave failed: err=%v onboarded=%v", err, user.Onboarded())
	}
}

func TestLastOwnerCannotLeave(t *testing.T) {
	svc, _, ctx := newSvc(t)
	orgID, ownerID := ownerOf(t, svc, ctx, "Acme", "owner@acme.com")
	if _, _, err := svc.LeaveOrg(ctx, orgID, ownerID, domain.RoleOwner, nil); !errors.Is(err, identity.ErrLastOwner) {
		t.Fatalf("last owner leaving must fail with ErrLastOwner, got %v", err)
	}
	// With a second owner, the first may leave.
	otherID := joinViaInvite(t, svc, ctx, orgID, ownerID, "co@acme.com", domain.RoleAdmin)
	if err := svc.ChangeMemberRole(ctx, orgID, ownerID, domain.RoleOwner, otherID, domain.RoleOwner); err != nil {
		t.Fatalf("promote 2nd owner: %v", err)
	}
	if _, _, err := svc.LeaveOrg(ctx, orgID, ownerID, domain.RoleOwner, nil); err != nil {
		t.Fatalf("owner leave with a co-owner present should succeed: %v", err)
	}
}

func TestOwnershipTransfer(t *testing.T) {
	svc, _, ctx := newSvc(t)
	orgID, ownerID := ownerOf(t, svc, ctx, "Acme", "owner@acme.com")
	memberID := joinViaInvite(t, svc, ctx, orgID, ownerID, "member@acme.com", domain.RoleMember)

	if err := svc.TransferOwnership(ctx, orgID, ownerID, domain.RoleOwner, memberID); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	members, _ := svc.ListMembers(ctx, orgID)
	roles := map[uuid.UUID]domain.Role{}
	owners := 0
	for _, m := range members {
		roles[m.UserID] = m.Role
		if m.Role == domain.RoleOwner {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("exactly one owner must remain, got %d", owners)
	}
	if roles[memberID] != domain.RoleOwner {
		t.Errorf("target should be owner, got %q", roles[memberID])
	}
	if roles[ownerID] != domain.RoleAdmin {
		t.Errorf("previous owner should be admin, got %q", roles[ownerID])
	}
	// A non-member can't be made owner.
	if err := svc.TransferOwnership(ctx, orgID, memberID, domain.RoleOwner, uuid.New()); !errors.Is(err, identity.ErrNotFound) {
		t.Errorf("transfer to non-member must fail, got %v", err)
	}
}

func TestLeaveThenJoinAnotherOrg(t *testing.T) {
	svc, _, ctx := newSvc(t)
	orgA, ownerA := ownerOf(t, svc, ctx, "Acme", "ownerA@acme.com")
	memberID := joinViaInvite(t, svc, ctx, orgA, ownerA, "mover@x.com", domain.RoleMember)

	// Leave A.
	if _, _, err := svc.LeaveOrg(ctx, orgA, memberID, domain.RoleMember, nil); err != nil {
		t.Fatalf("leave A: %v", err)
	}
	// Join B via code.
	orgB, ownerB := ownerOf(t, svc, ctx, "Beta", "ownerB@beta.com")
	code, _ := svc.CreateJoinCode(ctx, orgB, ownerB, "open", 0, 0)
	_, joined, err := svc.JoinViaCode(ctx, memberID, code)
	if err != nil {
		t.Fatalf("join B after leaving A: %v", err)
	}
	if joined.OrgID != orgB {
		t.Errorf("user should now be in org B, got %v", joined.OrgID)
	}
}

func TestInvitationDeliveryStatusTracking(t *testing.T) {
	svc, _, ctx := newSvc(t)
	orgID, ownerID := ownerOf(t, svc, ctx, "Acme", "owner@acme.com")

	// New invites start 'pending'.
	_, inv, err := svc.CreateInvitation(ctx, orgID, ownerID, "new@acme.com", domain.RoleMember)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if inv.DeliveryStatus != "pending" {
		t.Errorf("new invite delivery should be pending, got %q", inv.DeliveryStatus)
	}

	// Simulate a delivery failure — creation already succeeded, so the invite is still usable.
	if err := svc.UpdateInvitationDelivery(ctx, inv.ID, "failed", "smtp timeout"); err != nil {
		t.Fatalf("update delivery: %v", err)
	}
	list, _ := svc.ListInvitations(ctx, orgID)
	if list[0].DeliveryStatus != "failed" || list[0].DeliveryError != "smtp timeout" {
		t.Errorf("failed delivery not recorded: %+v", list[0])
	}

	// Resend rotates the token AND resets delivery back to pending.
	raw2, inv2, err := svc.ResendInvitation(ctx, orgID, inv.ID)
	if err != nil {
		t.Fatalf("resend: %v", err)
	}
	if inv2.DeliveryStatus != "pending" {
		t.Errorf("resend should reset delivery to pending, got %q", inv2.DeliveryStatus)
	}

	// The (re-sent) invitation is still acceptable despite the earlier delivery failure.
	_, u, _ := svc.RegisterPending(ctx, "new@acme.com", "New", "password123")
	if _, joined, err := svc.AcceptInvitation(ctx, u.ID, raw2); err != nil || joined.OrgID != orgID {
		t.Fatalf("accept after resend failed: %v", err)
	}
}
