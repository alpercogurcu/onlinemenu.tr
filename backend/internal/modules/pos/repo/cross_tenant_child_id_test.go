package repo_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/pos/domain"
	"onlinemenu.tr/internal/modules/pos/repo"
)

// ---------------------------------------------------------------------------
// Cross-tenant mutation through a CHILD entity id (docs/lessons-from-b2b.md
// item 2: "Özellikle child entity ID'siyle yapılan mutasyonlar (b2b'deki
// delik: order item ID'siyle tenant'sız update)").
//
// OrderRepo.MoveItemsToOrder is this repo's structurally identical statement:
//
//	UPDATE order_items SET order_id = $2 WHERE id = ANY($1::uuid[])
//
// — no tenant_id predicate anywhere in it. In b2b that exact shape was the
// leak: the parent was authorized, the child was updated by raw id, and a
// foreign id went straight through. Here it is safe only because the
// statement always runs inside WithTenantTx and order_items carries FORCE ROW
// LEVEL SECURITY (migrations/pos/000001, lines 88-93). That is an invariant
// held by two things that live in different files from this query, so it
// needs a test that fails if either one moves.
//
// The existing *_RLSIsolation tests all assert on READS. These assert on the
// WRITE, which is the half b2b got wrong.
// ---------------------------------------------------------------------------

// TestOrderRepo_MoveItemsToOrder_ForeignTenantItemID_MovesNothing is the
// direct regression: tenant B, acting entirely within its own valid tenant
// transaction, names tenant A's order_item id and gets zero rows — and A's
// line is still exactly where it was.
func TestOrderRepo_MoveItemsToOrder_ForeignTenantItemID_MovesNothing(t *testing.T) {
	ctx := context.Background()
	orderRepo := repo.NewOrderRepo()

	victim := newTestOrder(t, ctx, tenantA, testItem("Baklava", 1))
	require.Len(t, victim.Items, 1)
	victimItemID := victim.Items[0].ID
	require.NotEqual(t, uuid.Nil, victimItemID)

	attacker := newTestOrder(t, ctx, tenantB, testItem("Sütlaç", 1))

	var affected int64
	err := sharedPool.WithTenantTx(ctx, tenantB, func(tx pgx.Tx) error {
		var err error
		affected, err = orderRepo.MoveItemsToOrder(ctx, tx, []uuid.UUID{victimItemID}, attacker.ID)
		return err
	})
	require.NoError(t, err, "the statement itself must not error — RLS filters rows, it does not raise")
	assert.Zero(t, affected,
		"tenant B moved %d of tenant A's order_items by raw child id — order_items has no "+
			"tenant_id predicate in the UPDATE, so FORCE RLS is the only thing standing between "+
			"this query and b2b's leak", affected)

	// The victim line must still belong to its original order, and must still
	// be visible to its own tenant.
	var reread domain.Order
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		reread, err = orderRepo.GetByID(ctx, tx, victim.ID)
		return err
	})
	require.NoError(t, err)
	require.Len(t, reread.Items, 1, "tenant A's order lost a line to a foreign tenant's move")
	assert.Equal(t, victimItemID, reread.Items[0].ID)
}

// TestOrderRepo_ItemOwners_ForeignTenantItemID_ResolvesNothing pins the guard
// the service layer relies on before it ever calls MoveItemsToOrder:
// CheckService.MoveItems compares len(owners) against len(requested ids) and
// refuses the whole batch on a mismatch. If ItemOwners could resolve a
// foreign id, that comparison would pass and the move would be authorized on
// a check the caller does not own.
func TestOrderRepo_ItemOwners_ForeignTenantItemID_ResolvesNothing(t *testing.T) {
	ctx := context.Background()
	orderRepo := repo.NewOrderRepo()

	victim := newTestOrder(t, ctx, tenantA, testItem("Künefe", 1))
	require.Len(t, victim.Items, 1)
	victimItemID := victim.Items[0].ID

	own := newTestOrder(t, ctx, tenantB, testItem("Kadayıf", 1))
	require.Len(t, own.Items, 1)
	ownItemID := own.Items[0].ID

	var owners []repo.ItemOwner
	err := sharedPool.WithTenantTx(ctx, tenantB, func(tx pgx.Tx) error {
		var err error
		owners, err = orderRepo.ItemOwners(ctx, tx, []uuid.UUID{ownItemID, victimItemID})
		return err
	})
	require.NoError(t, err)

	require.Lenf(t, owners, 1,
		"ItemOwners resolved %d ids for tenant B; a foreign order_item id must be absent, "+
			"which is what makes the service layer's len() comparison a real authorization check",
		len(owners))
	assert.Equal(t, ownItemID, owners[0].ItemID)
}
