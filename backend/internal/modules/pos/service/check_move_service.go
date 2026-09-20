package service

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/platform/auth"
)

// ErrSameCheck is returned when a merge or a kalem taşıma names one check as
// both source and target. It is a caller bug, not a state conflict, so the
// HTTP layer answers 422 rather than 409.
var ErrSameCheck = errors.New("pos/service/check: source and target are the same check")

// ErrCheckPaymentsPresent is returned when the SOURCE check of a merge
// already carries money (completed or awaiting a fiscal result).
//
// Moving payment rows onto another check would break the fiscal trail: the
// ÖKC registered that sale against amounts derived from the source's items,
// and the settlement/close guard (payment SaleReader.TotalPaidForCheck) is
// keyed by check id. Re-homing that is a data-integrity project, not a
// sprint item — the cashier is told to close the paid adisyon first.
//
// It is distinct from ErrCheckHasPayments (cancel) so the POS can say
// "önce ödemesi olan adisyonu kapatın" instead of "void the payment".
var ErrCheckPaymentsPresent = errors.New("pos/service/check: source check has payments")

// ErrItemAlreadyPaid is returned when moving lines off a check that has
// already collected money would leave that check overpaid — i.e. what stays
// behind no longer covers what was paid. Moving the lines anyway would strand
// the difference on a check the cashier can no longer reduce.
var ErrItemAlreadyPaid = errors.New("pos/service/check: moved items are already covered by a payment")

// ErrOrderItemNotFound is returned when a requested order_item id does not
// exist, is not visible to the tenant, or belongs to a different check than
// the one named in the request.
//
// The three cases share one error on purpose: telling a caller apart "this id
// exists but is on someone else's adisyon" from "this id does not exist"
// leaks the existence of rows they may not read.
var ErrOrderItemNotFound = errors.New("pos/service/check: order item does not belong to this check")

// Transfer moves an open check to another table (docs/pos-ux-spec.md §3c masa
// taşıma).
//
// The table statuses move in the SAME transaction as the check row: source to
// empty, target to occupied. The client cannot do this itself — POST
// /tables/{id}/status is gated by pos.table.manage, which a cashier does not
// hold — and splitting it across two requests would leave the floor plan
// showing two occupied tables for one adisyon whenever the second call failed.
//
// Lock order is check → tables, matching Close/Cancel and the guest path. The
// two table rows are locked in ascending id order (see lockTables) so two
// cashiers swapping tables in opposite directions cannot deadlock.
func (s *CheckService) Transfer(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, checkID, tableID uuid.UUID) (domain.Check, error) {
	var moved domain.Check
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.checkRepo.GetForUpdate(ctx, tx, checkID)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, current.BranchID); err != nil {
			return err
		}
		if current.Status != domain.CheckStatusOpen {
			return pub.ErrCheckNotOpen
		}
		if current.TableID != nil && *current.TableID == tableID {
			// Already there. Returning the check unchanged keeps a retried
			// request (a double tap on a touchscreen) from being reported as
			// a conflict the cashier cannot act on.
			moved = current
			return nil
		}

		ids := []uuid.UUID{tableID}
		if current.TableID != nil {
			ids = append(ids, *current.TableID)
		}
		tables, err := lockTables(ctx, tx, s.tableRepo, ids)
		if err != nil {
			return err
		}

		target := tables[tableID]
		if target.BranchID != current.BranchID {
			// The spec names this check_branch_mismatch rather than the
			// table-shaped error: from the cashier's side the adisyon and the
			// table they picked belong to different branches, and the adisyon
			// is the thing they are holding.
			return pub.ErrCheckBranchMismatch
		}
		if err := staffTableGuard(target); err != nil {
			return err
		}
		if _, err := s.tableRepo.UpdateStatus(ctx, tx, target.ID, domain.TableStatusOccupied, target.Status); err != nil {
			if errors.Is(err, repo.ErrInvalidTransition) {
				return pub.ErrTableOccupied
			}
			return err
		}
		if current.TableID != nil {
			// Tolerant of a no-op for the same reason releaseTableToCleaning
			// is: staff may have reset the floor plan by hand, and the move
			// must not fail because of it.
			if _, err := s.tableRepo.UpdateStatusIfCurrent(ctx, tx, *current.TableID,
				domain.TableStatusEmpty, domain.TableStatusOccupied); err != nil {
				return err
			}
		}

		moved, err = s.checkRepo.UpdateTable(ctx, tx, checkID, &tableID, target.Name, domain.CheckStatusOpen)
		if err != nil {
			return err
		}

		if err := repo.InsertOutbox(ctx, tx, tenantID, "check", checkID.String(), "check.transferred", map[string]any{
			"tenant_id":     tenantID,
			"check_id":      checkID,
			"branch_id":     current.BranchID,
			"from_table_id": current.TableID,
			"to_table_id":   tableID,
			"from_table":    current.TableLabel,
			"to_table":      target.Name,
			"moved_by":      principal.PersonID,
		}); err != nil {
			return err
		}
		// One event per live order so the kitchen display relabels its
		// tickets: ws.Hub re-reads the order and its check's table_label on
		// every pos.order.*.v1 message, so the cook never plates a dish for
		// the table the ticket was opened at.
		return s.announceLiveOrdersMoved(ctx, tx, tenantID, checkID, "check_transferred")
	})
	if err != nil {
		return domain.Check{}, wrapErr(err, "pos/service/check: transfer: %w")
	}
	return moved, nil
}

// Merge folds the source check into the target (docs/pos-ux-spec.md §3c
// adisyon birleştirme). checkID is the target — the adisyon that survives.
//
// The source keeps its row and moves to domain.CheckStatusMerged, NOT to
// cancelled: the day-end report counts cancellations, and reporting every
// table merge as a cancelled sale would make the shift manager's figures
// unreconcilable (see pos/000008).
//
// A source carrying any payment at all is refused with ErrCheckPaymentsPresent
// before anything moves — see that error's doc comment.
func (s *CheckService) Merge(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, checkID, sourceCheckID uuid.UUID) (domain.Check, error) {
	if checkID == sourceCheckID {
		return domain.Check{}, ErrSameCheck
	}

	// Read outside the write transaction, exactly as Close/Cancel do:
	// SaleReader opens its own transaction on another pooled connection, and
	// calling it while holding two check row locks risks pool starvation.
	// It carries the same documented TOCTOU window those two do.
	paid, pending, err := s.collectedTotals(ctx, tenantID, sourceCheckID)
	if err != nil {
		return domain.Check{}, fmt.Errorf("pos/service/check: merge: %w", err)
	}

	var target domain.Check
	err = s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		checks, err := lockChecks(ctx, tx, s.checkRepo, []uuid.UUID{checkID, sourceCheckID})
		if err != nil {
			return err
		}
		target = checks[checkID]
		source := checks[sourceCheckID]

		if err := requireBranch(ctx, principal, target.BranchID); err != nil {
			return err
		}
		if source.BranchID != target.BranchID {
			return pub.ErrCheckBranchMismatch
		}
		if target.Status != domain.CheckStatusOpen || source.Status != domain.CheckStatusOpen {
			return pub.ErrCheckNotOpen
		}
		if paid+pending > 0 {
			return ErrCheckPaymentsPresent
		}

		movedOrders, err := s.orderRepo.ReassignToCheck(ctx, tx, source.ID, target.ID)
		if err != nil {
			return err
		}
		if _, err := s.checkRepo.MarkMerged(ctx, tx, source.ID, target.ID, domain.CheckStatusOpen); err != nil {
			return err
		}
		if source.TableID != nil {
			if _, err := s.tableRepo.UpdateStatusIfCurrent(ctx, tx, *source.TableID,
				domain.TableStatusEmpty, domain.TableStatusOccupied); err != nil {
				return err
			}
		}

		if err := repo.InsertOutbox(ctx, tx, tenantID, "check", source.ID.String(), "check.merged", map[string]any{
			"tenant_id":       tenantID,
			"check_id":        source.ID,
			"target_check_id": target.ID,
			"branch_id":       target.BranchID,
			"from_table":      source.TableLabel,
			"to_table":        target.TableLabel,
			"order_ids":       movedOrders,
			"merged_by":       principal.PersonID,
		}); err != nil {
			return err
		}
		return s.announceOrderIDsMoved(ctx, tx, tenantID, movedOrders, target.ID, "check_merged")
	})
	if err != nil {
		return domain.Check{}, wrapErr(err, "pos/service/check: merge: %w")
	}
	return target, nil
}

// MoveItems moves selected order lines from one check to another
// (docs/pos-ux-spec.md §3c kalem taşıma) and returns the TARGET check.
//
// The moved lines land on newly created orders on the target, one per
// distinct source-order status. The spec says "yeni bir order"; splitting by
// status is the one deviation, and it is what keeps the kitchen honest: a
// single new order would have to pick one status for lines that may be
// pending and delivered at once — 'pending' re-sends served food to the
// kitchen, anything else drops a ticket the cook still owes. Callers see no
// difference: the response is the target check, not the orders.
//
// A source check with money already collected is guarded by
// ErrItemAlreadyPaid: what stays behind must still cover what was paid.
func (s *CheckService) MoveItems(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, checkID, targetCheckID uuid.UUID, itemIDs []uuid.UUID) (domain.Check, error) {
	if checkID == targetCheckID {
		return domain.Check{}, ErrSameCheck
	}
	if len(itemIDs) == 0 {
		return domain.Check{}, fmt.Errorf("pos/service/check: move items: %w", ErrOrderItemNotFound)
	}

	paid, pending, err := s.collectedTotals(ctx, tenantID, checkID)
	if err != nil {
		return domain.Check{}, fmt.Errorf("pos/service/check: move items: %w", err)
	}

	var target domain.Check
	err = s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		checks, err := lockChecks(ctx, tx, s.checkRepo, []uuid.UUID{checkID, targetCheckID})
		if err != nil {
			return err
		}
		source := checks[checkID]
		target = checks[targetCheckID]

		if err := requireBranch(ctx, principal, source.BranchID); err != nil {
			return err
		}
		if source.BranchID != target.BranchID {
			return pub.ErrCheckBranchMismatch
		}
		if source.Status != domain.CheckStatusOpen || target.Status != domain.CheckStatusOpen {
			return pub.ErrCheckNotOpen
		}

		owners, err := s.orderRepo.ItemOwners(ctx, tx, itemIDs)
		if err != nil {
			return err
		}
		if err := assertItemsBelongToCheck(owners, itemIDs, source.ID); err != nil {
			return err
		}

		if paid+pending > 0 {
			sourceTotal, err := s.checkRepo.GetTotal(ctx, tx, source.ID)
			if err != nil {
				return err
			}
			if sourceTotal-movedAmount(owners) < paid+pending {
				return ErrItemAlreadyPaid
			}
		}

		createdOrders, err := s.rehomeItems(ctx, tx, tenantID, owners, source, target)
		if err != nil {
			return err
		}
		emptied, err := s.cancelEmptiedOrders(ctx, tx, tenantID, sourceOrderIDs(owners), source.ID, principal.PersonID)
		if err != nil {
			return err
		}

		if err := repo.InsertOutbox(ctx, tx, tenantID, "check", source.ID.String(), "check.items_moved", map[string]any{
			"tenant_id":         tenantID,
			"check_id":          source.ID,
			"target_check_id":   target.ID,
			"branch_id":         source.BranchID,
			"from_table":        source.TableLabel,
			"to_table":          target.TableLabel,
			"order_item_ids":    itemIDs,
			"created_order_ids": createdOrders,
			"emptied_order_ids": emptied,
			"moved_by":          principal.PersonID,
		}); err != nil {
			return err
		}
		return s.announceOrderIDsMoved(ctx, tx, tenantID, createdOrders, target.ID, "items_moved")
	})
	if err != nil {
		return domain.Check{}, wrapErr(err, "pos/service/check: move items: %w")
	}
	return target, nil
}

// collectedTotals reports how much money a check already carries: completed
// plus fiscal-pending, both in kuruş. Merge and MoveItems read it the same
// way Close/Cancel do, and outside their write transaction for the same
// pool-starvation reason.
func (s *CheckService) collectedTotals(ctx context.Context, tenantID, checkID uuid.UUID) (paid, pending int64, err error) {
	paid, err = s.saleReader.TotalPaidForCheck(ctx, tenantID, checkID)
	if err != nil {
		return 0, 0, fmt.Errorf("read payment total: %w", err)
	}
	pending, err = s.saleReader.PendingTotalForCheck(ctx, tenantID, checkID)
	if err != nil {
		return 0, 0, fmt.Errorf("read pending payment total: %w", err)
	}
	return paid, pending, nil
}

// rehomeItems creates one order on the target check per distinct source-order
// status and re-points the moved lines onto it. It returns the created order
// ids in a deterministic order (by status) so the event payload is stable.
func (s *CheckService) rehomeItems(
	ctx context.Context,
	tx pgx.Tx,
	tenantID uuid.UUID,
	owners []repo.ItemOwner,
	source, target domain.Check,
) ([]uuid.UUID, error) {
	byStatus := make(map[domain.OrderStatus][]uuid.UUID)
	for _, o := range owners {
		byStatus[o.OrderStatus] = append(byStatus[o.OrderStatus], o.ItemID)
	}
	statuses := make([]string, 0, len(byStatus))
	for st := range byStatus {
		statuses = append(statuses, string(st))
	}
	sort.Strings(statuses)

	created := make([]uuid.UUID, 0, len(statuses))
	for _, st := range statuses {
		status := domain.OrderStatus(st)
		order, err := s.orderRepo.Create(ctx, tx, domain.Order{
			TenantID:     tenantID,
			BranchID:     target.BranchID,
			CheckID:      &target.ID,
			OrderChannel: domain.OrderChannelDineIn,
			Source:       domain.SourcePOS,
			Status:       status,
			Note:         "Taşındı: " + source.TableLabel,
		})
		if err != nil {
			return nil, err
		}
		items := byStatus[status]
		affected, err := s.orderRepo.MoveItemsToOrder(ctx, tx, items, order.ID)
		if err != nil {
			return nil, err
		}
		if affected != int64(len(items)) {
			// ItemOwners locked every row FOR UPDATE inside this same
			// transaction, so a short UPDATE cannot be a lost race — it means
			// the ids and the lock disagree. Fail rather than commit a
			// partial move that would silently bill two checks.
			return nil, fmt.Errorf("pos/service/check: move items: moved %d of %d lines", affected, len(items))
		}
		created = append(created, order.ID)
	}
	return created, nil
}

// cancelEmptiedOrders cancels the source orders a move left with no lines,
// emitting one order.status_changed per cancelled order (DATA-002: a new
// event, never a payload rewrite). Orders already in a terminal status are
// skipped by OrderRepo.CancelByIDs — see its doc comment.
func (s *CheckService) cancelEmptiedOrders(
	ctx context.Context,
	tx pgx.Tx,
	tenantID uuid.UUID,
	sourceOrders []uuid.UUID,
	checkID, cancelledBy uuid.UUID,
) ([]uuid.UUID, error) {
	empty, err := s.orderRepo.EmptyOrderIDs(ctx, tx, sourceOrders)
	if err != nil {
		return nil, err
	}
	cancelled, err := s.orderRepo.CancelByIDs(ctx, tx, empty)
	if err != nil {
		return nil, err
	}
	for _, id := range cancelled {
		if err := repo.InsertOutbox(ctx, tx, tenantID, "order", id.String(), "order.status_changed", map[string]any{
			"tenant_id":    tenantID,
			"order_id":     id,
			"check_id":     checkID,
			"status":       domain.OrderStatusCancelled,
			"cancelled_by": cancelledBy,
			"reason":       "items_moved",
		}); err != nil {
			return nil, err
		}
	}
	return cancelled, nil
}

// announceOrdersMoved emits one order.moved event per live order of a check,
// so the kitchen display re-reads the ticket (and with it the new table
// label). ws.Hub normalizes every pos.order.*.v1 subject that is not
// "placed" to an order.status_changed WS message, so no hub change is needed.
func (s *CheckService) announceLiveOrdersMoved(ctx context.Context, tx pgx.Tx, tenantID, checkID uuid.UUID, reason string) error {
	orders, err := s.orderRepo.ListByCheck(ctx, tx, checkID)
	if err != nil {
		return err
	}
	ids := make([]uuid.UUID, 0, len(orders))
	for _, o := range orders {
		if isKitchenActive(o.Status) {
			ids = append(ids, o.ID)
		}
	}
	return s.announceOrderIDsMoved(ctx, tx, tenantID, ids, checkID, reason)
}

func (s *CheckService) announceOrderIDsMoved(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, orderIDs []uuid.UUID, checkID uuid.UUID, reason string) error {
	for _, id := range orderIDs {
		if err := repo.InsertOutbox(ctx, tx, tenantID, "order", id.String(), "order.moved", map[string]any{
			"tenant_id": tenantID,
			"order_id":  id,
			"check_id":  checkID,
			"reason":    reason,
		}); err != nil {
			return err
		}
	}
	return nil
}

func isKitchenActive(s domain.OrderStatus) bool {
	for _, active := range domain.KitchenActiveOrderStatuses {
		if active == s {
			return true
		}
	}
	return false
}

// lockChecks locks several check rows in ascending id order.
//
// Merge and MoveItems are the first paths in this module that hold two check
// locks at once, so they set the convention: without a deterministic order,
// two cashiers merging A→B and B→A at the same moment each hold one row and
// wait for the other's. Ascending uuid is arbitrary but total, which is all a
// deadlock-free order needs to be.
func lockChecks(ctx context.Context, tx pgx.Tx, checkRepo *repo.CheckRepo, ids []uuid.UUID) (map[uuid.UUID]domain.Check, error) {
	out := make(map[uuid.UUID]domain.Check, len(ids))
	for _, id := range sortedIDs(ids) {
		c, err := checkRepo.GetForUpdate(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, nil
}

// lockTables is lockChecks for table rows, and exists for the same reason:
// Transfer holds the source and the target table at once, and two cashiers
// swapping two tables in opposite directions would otherwise deadlock.
// repo.ErrNotFound is translated to pub.ErrTableNotFound because a table id is
// the only row these callers look up by a client-supplied id.
func lockTables(ctx context.Context, tx pgx.Tx, tableRepo *repo.TableRepo, ids []uuid.UUID) (map[uuid.UUID]domain.Table, error) {
	out := make(map[uuid.UUID]domain.Table, len(ids))
	for _, id := range sortedIDs(ids) {
		t, err := tableRepo.GetTableForUpdate(ctx, tx, id)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return nil, pub.ErrTableNotFound
			}
			return nil, err
		}
		out[id] = t
	}
	return out, nil
}

func sortedIDs(ids []uuid.UUID) []uuid.UUID {
	out := append([]uuid.UUID(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// assertItemsBelongToCheck rejects the whole request unless every requested
// line was found and sits on the named check. Partial moves are not offered:
// a cashier who selected four lines and got three moved has no way to tell
// which one stayed.
func assertItemsBelongToCheck(owners []repo.ItemOwner, requested []uuid.UUID, checkID uuid.UUID) error {
	if len(owners) != len(requested) {
		return ErrOrderItemNotFound
	}
	for _, o := range owners {
		if o.CheckID == nil || *o.CheckID != checkID {
			return ErrOrderItemNotFound
		}
	}
	return nil
}

// movedAmount is what the moved lines are worth (kuruş), counting only lines
// that currently contribute to the bill — CheckRepo.GetTotal excludes
// rejected/cancelled orders, so including them here would overstate what the
// source check loses and refuse a legitimate move.
func movedAmount(owners []repo.ItemOwner) int64 {
	var total int64
	for _, o := range owners {
		if isInactive(o.OrderStatus) {
			continue
		}
		total += int64(o.Quantity) * o.UnitPriceAmount
	}
	return total
}

func isInactive(s domain.OrderStatus) bool {
	for _, inactive := range domain.InactiveOrderStatuses {
		if inactive == s {
			return true
		}
	}
	return false
}

// sourceOrderIDs lists the distinct orders the moved lines came from,
// preserving first-seen order so the event payload is stable.
func sourceOrderIDs(owners []repo.ItemOwner) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(owners))
	out := make([]uuid.UUID, 0, len(owners))
	for _, o := range owners {
		if _, dup := seen[o.OrderID]; dup {
			continue
		}
		seen[o.OrderID] = struct{}{}
		out = append(out, o.OrderID)
	}
	return out
}
