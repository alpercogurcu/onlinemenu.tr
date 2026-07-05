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

// Create inserts an order and its items in the same transaction.
func (r *OrderRepo) Create(ctx context.Context, tx pgx.Tx, o domain.Order) (domain.Order, error) {
	const qOrder = `
		INSERT INTO orders
		    (tenant_id, branch_id, check_id, order_channel, delivery_integrator_id,
		     status, accept_deadline_at, note)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, tenant_id, branch_id, check_id, order_channel,
		          delivery_integrator_id, status, accept_deadline_at,
		          accepted_at, accepted_by, rejected_at, rejected_by,
		          rejection_reason, note, created_at, updated_at`

	row := tx.QueryRow(ctx, qOrder,
		o.TenantID, o.BranchID, o.CheckID, string(o.OrderChannel),
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
		SELECT id, tenant_id, branch_id, check_id, order_channel,
		       delivery_integrator_id, status, accept_deadline_at,
		       accepted_at, accepted_by, rejected_at, rejected_by,
		       rejection_reason, note, created_at, updated_at
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

// GetForUpdate locks the order row (without items) for the duration of the
// caller's transaction, so a status-transition check-then-write sequence is
// race-free against other transactions attempting the same transition.
func (r *OrderRepo) GetForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Order, error) {
	const q = `
		SELECT id, tenant_id, branch_id, check_id, order_channel,
		       delivery_integrator_id, status, accept_deadline_at,
		       accepted_at, accepted_by, rejected_at, rejected_by,
		       rejection_reason, note, created_at, updated_at
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

// ListByCheck returns all orders for a given check, oldest first.
func (r *OrderRepo) ListByCheck(ctx context.Context, tx pgx.Tx, checkID uuid.UUID) ([]domain.Order, error) {
	const q = `
		SELECT id, tenant_id, branch_id, check_id, order_channel,
		       delivery_integrator_id, status, accept_deadline_at,
		       accepted_at, accepted_by, rejected_at, rejected_by,
		       rejection_reason, note, created_at, updated_at
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
		SELECT id, tenant_id, branch_id, check_id, order_channel,
		       delivery_integrator_id, status, accept_deadline_at,
		       accepted_at, accepted_by, rejected_at, rejected_by,
		       rejection_reason, note, created_at, updated_at
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
		RETURNING id, tenant_id, branch_id, check_id, order_channel,
		          delivery_integrator_id, status, accept_deadline_at,
		          accepted_at, accepted_by, rejected_at, rejected_by,
		          rejection_reason, note, created_at, updated_at`

	o, err := scanOrder(tx.QueryRow(ctx, q, id, acceptedBy, string(expectedStatus)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Order{}, ErrInvalidTransition
		}
		return domain.Order{}, fmt.Errorf("pos/repo/order: accept: %w", err)
	}
	return o, nil
}

// Reject transitions an order to rejected, guarded on its expected current status.
func (r *OrderRepo) Reject(ctx context.Context, tx pgx.Tx, id uuid.UUID, rejectedBy uuid.UUID, reason string, expectedStatus domain.OrderStatus) (domain.Order, error) {
	const q = `
		UPDATE orders
		SET status = 'rejected', rejected_at = NOW(), rejected_by = $2,
		    rejection_reason = $3, updated_at = NOW()
		WHERE id = $1 AND status = $4
		RETURNING id, tenant_id, branch_id, check_id, order_channel,
		          delivery_integrator_id, status, accept_deadline_at,
		          accepted_at, accepted_by, rejected_at, rejected_by,
		          rejection_reason, note, created_at, updated_at`

	o, err := scanOrder(tx.QueryRow(ctx, q, id, rejectedBy, reason, string(expectedStatus)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Order{}, ErrInvalidTransition
		}
		return domain.Order{}, fmt.Errorf("pos/repo/order: reject: %w", err)
	}
	return o, nil
}

// AdvanceStatus transitions order through preparing → ready → delivered,
// guarded on its expected current status.
func (r *OrderRepo) AdvanceStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status, expectedStatus domain.OrderStatus) (domain.Order, error) {
	const q = `
		UPDATE orders SET status = $2, updated_at = NOW()
		WHERE id = $1 AND status = $3
		RETURNING id, tenant_id, branch_id, check_id, order_channel,
		          delivery_integrator_id, status, accept_deadline_at,
		          accepted_at, accepted_by, rejected_at, rejected_by,
		          rejection_reason, note, created_at, updated_at`

	o, err := scanOrder(tx.QueryRow(ctx, q, id, string(status), string(expectedStatus)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Order{}, ErrInvalidTransition
		}
		return domain.Order{}, fmt.Errorf("pos/repo/order: advance status: %w", err)
	}
	return o, nil
}

// insertItems bulk-inserts order items and returns them with server IDs.
func (r *OrderRepo) insertItems(ctx context.Context, tx pgx.Tx, orderID, tenantID uuid.UUID, items []domain.OrderItem) ([]domain.OrderItem, error) {
	const q = `
		INSERT INTO order_items
		    (tenant_id, order_id, product_id, product_name, product_price_amount,
		     product_currency, tax_rate_bps, quantity, unit_price_amount, note)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, tenant_id, order_id, product_id, product_name,
		          product_price_amount, product_currency, tax_rate_bps,
		          quantity, unit_price_amount, note, created_at`

	out := make([]domain.OrderItem, 0, len(items))
	for _, item := range items {
		var oi domain.OrderItem
		err := tx.QueryRow(ctx, q,
			tenantID, orderID, item.ProductID, item.ProductName,
			item.ProductPriceAmount, item.ProductCurrency, item.TaxRateBPS,
			item.Quantity, item.UnitPriceAmount, item.Note,
		).Scan(
			&oi.ID, &oi.TenantID, &oi.OrderID, &oi.ProductID,
			&oi.ProductName, &oi.ProductPriceAmount, &oi.ProductCurrency,
			&oi.TaxRateBPS, &oi.Quantity, &oi.UnitPriceAmount, &oi.Note, &oi.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("pos/repo/order: insert item: %w", err)
		}
		out = append(out, oi)
	}
	return out, nil
}

func (r *OrderRepo) loadItems(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]domain.OrderItem, error) {
	const q = `
		SELECT id, tenant_id, order_id, product_id, product_name,
		       product_price_amount, product_currency, tax_rate_bps,
		       quantity, unit_price_amount, note, created_at
		FROM order_items WHERE order_id = $1 ORDER BY created_at`

	rows, err := tx.Query(ctx, q, orderID)
	if err != nil {
		return nil, fmt.Errorf("pos/repo/order: load items: %w", err)
	}
	defer rows.Close()

	var items []domain.OrderItem
	for rows.Next() {
		var oi domain.OrderItem
		if err := rows.Scan(
			&oi.ID, &oi.TenantID, &oi.OrderID, &oi.ProductID,
			&oi.ProductName, &oi.ProductPriceAmount, &oi.ProductCurrency,
			&oi.TaxRateBPS, &oi.Quantity, &oi.UnitPriceAmount, &oi.Note, &oi.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("pos/repo/order: load items scan: %w", err)
		}
		items = append(items, oi)
	}
	return items, rows.Err()
}

// scanOrder reads one order row (no items).
func scanOrder(s interface {
	Scan(...any) error
}) (domain.Order, error) {
	var o domain.Order
	var channel, status string
	if err := s.Scan(
		&o.ID, &o.TenantID, &o.BranchID, &o.CheckID, &channel,
		&o.DeliveryIntegratorID, &status, &o.AcceptDeadlineAt,
		&o.AcceptedAt, &o.AcceptedBy, &o.RejectedAt, &o.RejectedBy,
		&o.RejectionReason, &o.Note, &o.CreatedAt, &o.UpdatedAt,
	); err != nil {
		return domain.Order{}, err
	}
	o.OrderChannel = domain.OrderChannel(channel)
	o.Status = domain.OrderStatus(status)
	return o, nil
}
