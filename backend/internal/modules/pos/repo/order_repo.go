package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/pos/domain"
)

// OrderRepo manages order and order_item persistence.
type OrderRepo struct{}

func NewOrderRepo() *OrderRepo { return &OrderRepo{} }

// orderColumns is the single projection every order query selects, in the
// exact order scanOrder reads them. Shared rather than repeated per query
// because scanOrder takes ...any: a drifting column list compiles fine and
// only fails at runtime.
const orderColumns = `id, tenant_id, branch_id, check_id, order_channel, source,
		       delivery_integrator_id, status, accept_deadline_at,
		       accepted_at, accepted_by, rejected_at, rejected_by,
		       rejection_reason, note, created_at, updated_at`

// orderItemColumns plays the same role as orderColumns for order_items, in
// the exact order scanOrderItem reads them.
// modifier_ids is projected as text[] rather than uuid[] because every pool
// here runs under pgx.QueryExecModeSimpleProtocol (see uuidStrings): the
// driver cannot resolve a uuid[] element OID without a round-trip, so the
// array travels as text both ways and scanOrderItem parses it.
const orderItemColumns = `id, tenant_id, order_id, product_id, product_name,
		          product_price_amount, product_currency, tax_rate_bps,
		          quantity, unit_price_amount, note, modifier_ids::text[], created_at`

// uuidStrings renders ids for an `= ANY($n::uuid[])` parameter. They travel
// as []string rather than []uuid.UUID because every pool here runs under
// pgx.QueryExecModeSimpleProtocol (pgBouncer transaction-mode safety,
// ADR-SEC-001/002 — see platform/db.go), which cannot resolve an array
// element's OID for a non-driver-native slice type and fails with "cannot
// find encode plan". Same pattern as CheckRepo.TotalsByCheckIDs.
func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

// Create inserts an order and its items in the same transaction.
//
// Source is normalized to SourcePOS when empty: the INSERT names the column
// explicitly, so a zero-value Go string would be written as an empty string and violate the
// column CHECK instead of falling back to the column DEFAULT.
func (r *OrderRepo) Create(ctx context.Context, tx pgx.Tx, o domain.Order) (domain.Order, error) {
	const qOrder = `
		INSERT INTO orders
		    (tenant_id, branch_id, check_id, order_channel, source, delivery_integrator_id,
		     status, accept_deadline_at, note)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING ` + orderColumns

	source := o.Source
	if source == "" {
		source = domain.SourcePOS
	}

	row := tx.QueryRow(ctx, qOrder,
		o.TenantID, o.BranchID, o.CheckID, string(o.OrderChannel), string(source),
		o.DeliveryIntegratorID, string(o.Status), o.AcceptDeadlineAt, o.Note,
	)
	created, err := scanOrder(row)
	if err != nil {
		return domain.Order{}, fmt.Errorf("pos/repo/order: create order: %w", err)
	}

	items, err := r.insertItems(ctx, tx, created.ID, created.TenantID, o.Items)
	if err != nil {
		return domain.Order{}, err
	}
	created.Items = items
	return created, nil
}

// GetByID returns an order with its items.
func (r *OrderRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Order, error) {
	const q = `
		SELECT ` + orderColumns + `
		FROM orders WHERE id = $1`

	o, err := scanOrder(tx.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Order{}, ErrNotFound
		}
		return domain.Order{}, fmt.Errorf("pos/repo/order: get by id: %w", err)
	}

	items, err := r.loadItems(ctx, tx, o.ID)
	if err != nil {
		return domain.Order{}, err
	}
	o.Items = items
	return o, nil
}

// GetHeader returns an order row without items and without a lock.
func (r *OrderRepo) GetHeader(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Order, error) {
	const q = `
		SELECT ` + orderColumns + `
		FROM orders WHERE id = $1`

	o, err := scanOrder(tx.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Order{}, ErrNotFound
		}
		return domain.Order{}, fmt.Errorf("pos/repo/order: get header: %w", err)
	}
	return o, nil
}

// GetForUpdate locks the order row (without items) for the duration of the
// caller's transaction, so a status-transition check-then-write sequence is
// race-free against other transactions attempting the same transition.
func (r *OrderRepo) GetForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Order, error) {
	const q = `
		SELECT ` + orderColumns + `
		FROM orders WHERE id = $1 FOR UPDATE`

	o, err := scanOrder(tx.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Order{}, ErrNotFound
		}
		return domain.Order{}, fmt.Errorf("pos/repo/order: get for update: %w", err)
	}
	return o, nil
}

// ListByIDs returns the orders matching the given ids, with their items,
// oldest first — two queries total regardless of how many ids are asked for
// (one for the orders, one for every item of all of them), so a client
// rendering hundreds of tickets never needs one GET /orders/{id} round-trip
// per ticket (N+1). Deliberately not ordered by the input slice: callers key
// the result by id, and created_at matches every other order listing here.
//
// An id that does not exist, or is not visible under the current tenant
// context (RLS), is simply absent from the result; callers must treat a
// short result as a partial answer, not an error — the same contract as
// CheckRepo.TableLabelsByCheckIDs.
func (r *OrderRepo) ListByIDs(ctx context.Context, tx pgx.Tx, orderIDs []uuid.UUID) ([]domain.Order, error) {
	if len(orderIDs) == 0 {
		return nil, nil
	}

	const q = `
		SELECT ` + orderColumns + `
		FROM orders WHERE id = ANY($1::uuid[]) ORDER BY created_at`

	rows, err := tx.Query(ctx, q, uuidStrings(orderIDs))
	if err != nil {
		return nil, fmt.Errorf("pos/repo/order: list by ids: %w", err)
	}
	defer rows.Close()

	var orders []domain.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("pos/repo/order: list by ids scan: %w", err)
		}
		orders = append(orders, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return nil, nil
	}

	found := make([]uuid.UUID, len(orders))
	for i, o := range orders {
		found[i] = o.ID
	}
	// Items are fetched for the ids that actually came back, not the ids that
	// were asked for: RLS may have filtered some out, and asking for those
	// again would only widen the array parameter for no rows.
	byOrder, err := r.itemsByOrderIDs(ctx, tx, found)
	if err != nil {
		return nil, err
	}
	for i := range orders {
		orders[i].Items = byOrder[orders[i].ID]
	}
	return orders, nil
}

// ListByCheck returns all orders for a given check, oldest first.
func (r *OrderRepo) ListByCheck(ctx context.Context, tx pgx.Tx, checkID uuid.UUID) ([]domain.Order, error) {
	const q = `
		SELECT ` + orderColumns + `
		FROM orders WHERE check_id = $1 ORDER BY created_at`

	rows, err := tx.Query(ctx, q, checkID)
	if err != nil {
		return nil, fmt.Errorf("pos/repo/order: list by check: %w", err)
	}
	defer rows.Close()

	var orders []domain.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("pos/repo/order: list by check scan: %w", err)
		}
		orders = append(orders, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range orders {
		items, err := r.loadItems(ctx, tx, orders[i].ID)
		if err != nil {
			return nil, err
		}
		orders[i].Items = items
	}
	return orders, nil
}

// ListActiveByBranch returns all orders for a branch whose status is still
// "live" for the kitchen (domain.KitchenActiveOrderStatuses:
// pending/accepted/preparing/ready) — used to build the WebSocket snapshot
// sent to a newly (re)connected kitchen display so it can rebuild state
// without having missed any NATS events during a disconnect. "ready" orders
// are included so a reconnect does not drop orders that are cooked and
// waiting for pickup/delivery — see domain.KitchenActiveOrderStatuses's doc
// comment.
func (r *OrderRepo) ListActiveByBranch(ctx context.Context, tx pgx.Tx, branchID uuid.UUID) ([]domain.Order, error) {
	const q = `
		SELECT ` + orderColumns + `
		FROM orders
		WHERE branch_id = $1 AND status = ANY($2)
		ORDER BY created_at`

	statuses := make([]string, len(domain.KitchenActiveOrderStatuses))
	for i, s := range domain.KitchenActiveOrderStatuses {
		statuses[i] = string(s)
	}

	rows, err := tx.Query(ctx, q, branchID, statuses)
	if err != nil {
		return nil, fmt.Errorf("pos/repo/order: list active by branch: %w", err)
	}
	defer rows.Close()

	var orders []domain.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("pos/repo/order: list active by branch scan: %w", err)
		}
		orders = append(orders, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Items are not loaded here: the kitchen WS snapshot only needs routing
	// fields (status, check_id) — clients fetch full item detail via the
	// existing REST GET /orders/{id} (DATA-002: no event/snapshot payload
	// enrichment beyond what is already immutable).
	return orders, nil
}

// Accept transitions an order to accepted, guarded on its expected current
// status. Returns ErrInvalidTransition if the row's status no longer matches
// expectedStatus (see GetForUpdate — the guard is defense-in-depth, since the
// row lock already serializes concurrent transitions within one transaction).
func (r *OrderRepo) Accept(ctx context.Context, tx pgx.Tx, id uuid.UUID, acceptedBy uuid.UUID, expectedStatus domain.OrderStatus) (domain.Order, error) {
	const q = `
		UPDATE orders
		SET status = 'accepted', accepted_at = NOW(), accepted_by = $2, updated_at = NOW()
		WHERE id = $1 AND status = $3
		RETURNING ` + orderColumns

	o, err := scanOrder(tx.QueryRow(ctx, q, id, acceptedBy, string(expectedStatus)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Order{}, ErrInvalidTransition
		}
		return domain.Order{}, fmt.Errorf("pos/repo/order: accept: %w", err)
	}
	return r.withItems(ctx, tx, o)
}

// Reject transitions an order to rejected, guarded on its expected current status.
func (r *OrderRepo) Reject(ctx context.Context, tx pgx.Tx, id uuid.UUID, rejectedBy uuid.UUID, reason string, expectedStatus domain.OrderStatus) (domain.Order, error) {
	const q = `
		UPDATE orders
		SET status = 'rejected', rejected_at = NOW(), rejected_by = $2,
		    rejection_reason = $3, updated_at = NOW()
		WHERE id = $1 AND status = $4
		RETURNING ` + orderColumns

	o, err := scanOrder(tx.QueryRow(ctx, q, id, rejectedBy, reason, string(expectedStatus)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Order{}, ErrInvalidTransition
		}
		return domain.Order{}, fmt.Errorf("pos/repo/order: reject: %w", err)
	}
	return r.withItems(ctx, tx, o)
}

// AdvanceStatus transitions order through preparing → ready → delivered,
// guarded on its expected current status.
func (r *OrderRepo) AdvanceStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status, expectedStatus domain.OrderStatus) (domain.Order, error) {
	const q = `
		UPDATE orders SET status = $2, updated_at = NOW()
		WHERE id = $1 AND status = $3
		RETURNING ` + orderColumns

	o, err := scanOrder(tx.QueryRow(ctx, q, id, string(status), string(expectedStatus)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Order{}, ErrInvalidTransition
		}
		return domain.Order{}, fmt.Errorf("pos/repo/order: advance status: %w", err)
	}
	return r.withItems(ctx, tx, o)
}

// CancelActiveByCheck cancels every order of a check that is still live for
// the kitchen (domain.KitchenActiveOrderStatuses) and returns the ids it
// touched, so the caller can record one cancellation event per order.
// Delivered orders are left alone: that food was served.
func (r *OrderRepo) CancelActiveByCheck(ctx context.Context, tx pgx.Tx, checkID uuid.UUID) ([]uuid.UUID, error) {
	const q = `
		UPDATE orders SET status = 'cancelled', updated_at = NOW()
		WHERE check_id = $1 AND status = ANY($2)
		RETURNING id`

	statuses := make([]string, len(domain.KitchenActiveOrderStatuses))
	for i, s := range domain.KitchenActiveOrderStatuses {
		statuses[i] = string(s)
	}

	rows, err := tx.Query(ctx, q, checkID, statuses)
	if err != nil {
		return nil, fmt.Errorf("pos/repo/order: cancel active by check: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("pos/repo/order: cancel active by check scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pos/repo/order: cancel active by check: %w", err)
	}
	return ids, nil
}

// withItems attaches o's items so a transition's response carries the same
// shape as GET /orders/{id}; the UPDATE ... RETURNING only yields the row.
func (r *OrderRepo) withItems(ctx context.Context, tx pgx.Tx, o domain.Order) (domain.Order, error) {
	items, err := r.loadItems(ctx, tx, o.ID)
	if err != nil {
		return domain.Order{}, err
	}
	o.Items = items
	return o, nil
}

// insertItems bulk-inserts order items and returns them with server IDs.
func (r *OrderRepo) insertItems(ctx context.Context, tx pgx.Tx, orderID, tenantID uuid.UUID, items []domain.OrderItem) ([]domain.OrderItem, error) {
	const q = `
		INSERT INTO order_items
		    (tenant_id, order_id, product_id, product_name, product_price_amount,
		     product_currency, tax_rate_bps, quantity, unit_price_amount, note,
		     modifier_ids)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::uuid[])
		RETURNING ` + orderItemColumns

	out := make([]domain.OrderItem, 0, len(items))
	for _, item := range items {
		row := tx.QueryRow(ctx, q,
			tenantID, orderID, item.ProductID, item.ProductName,
			item.ProductPriceAmount, item.ProductCurrency, item.TaxRateBPS,
			item.Quantity, item.UnitPriceAmount, item.Note,
			uuidStrings(item.ModifierIDs),
		)
		oi, err := scanOrderItem(row)
		if err != nil {
			return nil, fmt.Errorf("pos/repo/order: insert item: %w", err)
		}
		out = append(out, oi)
	}
	return out, nil
}

// itemsByOrderIDs loads the items of many orders in one query, grouped by
// order id. An order with no items has no key in the returned map; callers
// must treat a missing key as "no items", not an error.
func (r *OrderRepo) itemsByOrderIDs(ctx context.Context, tx pgx.Tx, orderIDs []uuid.UUID) (map[uuid.UUID][]domain.OrderItem, error) {
	byOrder := make(map[uuid.UUID][]domain.OrderItem, len(orderIDs))
	if len(orderIDs) == 0 {
		return byOrder, nil
	}

	const q = `
		SELECT ` + orderItemColumns + `
		FROM order_items WHERE order_id = ANY($1::uuid[])
		ORDER BY order_id, created_at`

	rows, err := tx.Query(ctx, q, uuidStrings(orderIDs))
	if err != nil {
		return nil, fmt.Errorf("pos/repo/order: items by order ids: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		oi, err := scanOrderItem(rows)
		if err != nil {
			return nil, fmt.Errorf("pos/repo/order: items by order ids scan: %w", err)
		}
		byOrder[oi.OrderID] = append(byOrder[oi.OrderID], oi)
	}
	return byOrder, rows.Err()
}

func (r *OrderRepo) loadItems(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]domain.OrderItem, error) {
	const q = `
		SELECT ` + orderItemColumns + `
		FROM order_items WHERE order_id = $1 ORDER BY created_at`

	rows, err := tx.Query(ctx, q, orderID)
	if err != nil {
		return nil, fmt.Errorf("pos/repo/order: load items: %w", err)
	}
	defer rows.Close()

	var items []domain.OrderItem
	for rows.Next() {
		oi, err := scanOrderItem(rows)
		if err != nil {
			return nil, fmt.Errorf("pos/repo/order: load items scan: %w", err)
		}
		items = append(items, oi)
	}
	return items, rows.Err()
}

// scanOrderItem reads one order_items row projected as orderItemColumns.
func scanOrderItem(s interface {
	Scan(...any) error
}) (domain.OrderItem, error) {
	var oi domain.OrderItem
	var modifierIDs []string
	if err := s.Scan(
		&oi.ID, &oi.TenantID, &oi.OrderID, &oi.ProductID,
		&oi.ProductName, &oi.ProductPriceAmount, &oi.ProductCurrency,
		&oi.TaxRateBPS, &oi.Quantity, &oi.UnitPriceAmount, &oi.Note,
		&modifierIDs, &oi.CreatedAt,
	); err != nil {
		return domain.OrderItem{}, err
	}
	for _, raw := range modifierIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			// The column is uuid[]; a value that does not parse means the
			// projection drifted, not that a client sent junk.
			return domain.OrderItem{}, fmt.Errorf("pos/repo/order: scan modifier id %q: %w", raw, err)
		}
		oi.ModifierIDs = append(oi.ModifierIDs, id)
	}
	return oi, nil
}

// scanOrder reads one order row (no items).
func scanOrder(s interface {
	Scan(...any) error
}) (domain.Order, error) {
	var o domain.Order
	var channel, source, status string
	if err := s.Scan(
		&o.ID, &o.TenantID, &o.BranchID, &o.CheckID, &channel, &source,
		&o.DeliveryIntegratorID, &status, &o.AcceptDeadlineAt,
		&o.AcceptedAt, &o.AcceptedBy, &o.RejectedAt, &o.RejectedBy,
		&o.RejectionReason, &o.Note, &o.CreatedAt, &o.UpdatedAt,
	); err != nil {
		return domain.Order{}, err
	}
	o.OrderChannel = domain.OrderChannel(channel)
	o.Source = domain.Source(source)
	o.Status = domain.OrderStatus(status)
	return o, nil
}

// ReassignToCheck re-points every order of sourceCheckID onto targetCheckID
// and returns the ids it touched, so the caller can record one event per
// moved order (docs/pos-ux-spec.md §3c birleştirme).
//
// It moves ALL of the source's orders, including rejected and cancelled ones:
// the source check is about to become unreachable (status 'merged'), and
// leaving its audit trail behind on a row nobody will look at again would
// hide why a ticket was rejected. Their items still do not count towards the
// target's bill — CheckRepo.GetTotal excludes domain.InactiveOrderStatuses
// wherever the order hangs.
//
// branch_id is deliberately not rewritten: CheckService.Merge refuses a
// cross-branch merge, so the two checks already share one.
func (r *OrderRepo) ReassignToCheck(ctx context.Context, tx pgx.Tx, sourceCheckID, targetCheckID uuid.UUID) ([]uuid.UUID, error) {
	const q = `
		UPDATE orders SET check_id = $2, updated_at = NOW()
		WHERE check_id = $1
		RETURNING id`

	rows, err := tx.Query(ctx, q, sourceCheckID, targetCheckID)
	if err != nil {
		return nil, fmt.Errorf("pos/repo/order: reassign to check: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("pos/repo/order: reassign to check scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pos/repo/order: reassign to check: %w", err)
	}
	return ids, nil
}

// ItemOwner answers "which order, which check and which order status does
// this order_item currently belong to", plus what it is worth. It exists so
// the move-items path can verify ownership and price the move in one query
// instead of loading whole orders.
type ItemOwner struct {
	ItemID          uuid.UUID
	OrderID         uuid.UUID
	CheckID         *uuid.UUID
	OrderStatus     domain.OrderStatus
	Quantity        int
	UnitPriceAmount int64
}

// ItemOwners resolves the given order_item ids to their owning order.
//
// Ids that do not exist, or that RLS hides, are simply absent from the
// result: the caller compares the returned length against what it asked for
// and reports which id was rejected, which is a better error than "not
// found" for a batch request. The rows are locked FOR UPDATE OF oi so a
// concurrent second move of the same line blocks rather than double-moving
// it.
func (r *OrderRepo) ItemOwners(ctx context.Context, tx pgx.Tx, itemIDs []uuid.UUID) ([]ItemOwner, error) {
	if len(itemIDs) == 0 {
		return nil, nil
	}

	const q = `
		SELECT oi.id, oi.order_id, o.check_id, o.status, oi.quantity, oi.unit_price_amount
		FROM order_items oi
		JOIN orders o ON o.id = oi.order_id
		WHERE oi.id = ANY($1::uuid[])
		ORDER BY oi.id
		FOR UPDATE OF oi`

	rows, err := tx.Query(ctx, q, uuidStrings(itemIDs))
	if err != nil {
		return nil, fmt.Errorf("pos/repo/order: item owners: %w", err)
	}
	defer rows.Close()

	var out []ItemOwner
	for rows.Next() {
		var owner ItemOwner
		var status string
		if err := rows.Scan(&owner.ItemID, &owner.OrderID, &owner.CheckID, &status,
			&owner.Quantity, &owner.UnitPriceAmount); err != nil {
			return nil, fmt.Errorf("pos/repo/order: item owners scan: %w", err)
		}
		owner.OrderStatus = domain.OrderStatus(status)
		out = append(out, owner)
	}
	return out, rows.Err()
}

// MoveItemsToOrder re-points order_items at another order and returns how
// many rows it touched. The caller has already verified — under the row lock
// ItemOwners takes — that every id belongs to the source check.
func (r *OrderRepo) MoveItemsToOrder(ctx context.Context, tx pgx.Tx, itemIDs []uuid.UUID, targetOrderID uuid.UUID) (int64, error) {
	if len(itemIDs) == 0 {
		return 0, nil
	}

	tag, err := tx.Exec(ctx, `
		UPDATE order_items SET order_id = $2
		WHERE id = ANY($1::uuid[])
	`, uuidStrings(itemIDs), targetOrderID)
	if err != nil {
		return 0, fmt.Errorf("pos/repo/order: move items to order: %w", err)
	}
	return tag.RowsAffected(), nil
}

// EmptyOrderIDs reports which of the given orders have no items left, so the
// caller can cancel the husks a move left behind.
func (r *OrderRepo) EmptyOrderIDs(ctx context.Context, tx pgx.Tx, orderIDs []uuid.UUID) ([]uuid.UUID, error) {
	if len(orderIDs) == 0 {
		return nil, nil
	}

	const q = `
		SELECT o.id
		FROM orders o
		WHERE o.id = ANY($1::uuid[])
		  AND NOT EXISTS (SELECT 1 FROM order_items oi WHERE oi.order_id = o.id)`

	rows, err := tx.Query(ctx, q, uuidStrings(orderIDs))
	if err != nil {
		return nil, fmt.Errorf("pos/repo/order: empty order ids: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("pos/repo/order: empty order ids scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CancelByIDs cancels the given orders, skipping any that are no longer live
// for the kitchen (domain.KitchenActiveOrderStatuses), and returns the ids it
// actually changed.
//
// The status filter is what keeps the order state machine honest: a
// 'delivered' order emptied by a move must not be dragged backwards into
// 'cancelled' (domain.TransitionOrderStatus forbids that edge), and an empty
// order contributes 0 to the bill either way.
func (r *OrderRepo) CancelByIDs(ctx context.Context, tx pgx.Tx, orderIDs []uuid.UUID) ([]uuid.UUID, error) {
	if len(orderIDs) == 0 {
		return nil, nil
	}

	statuses := make([]string, len(domain.KitchenActiveOrderStatuses))
	for i, s := range domain.KitchenActiveOrderStatuses {
		statuses[i] = string(s)
	}

	const q = `
		UPDATE orders SET status = 'cancelled', updated_at = NOW()
		WHERE id = ANY($1::uuid[]) AND status = ANY($2)
		RETURNING id`

	rows, err := tx.Query(ctx, q, uuidStrings(orderIDs), statuses)
	if err != nil {
		return nil, fmt.Errorf("pos/repo/order: cancel by ids: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("pos/repo/order: cancel by ids scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
