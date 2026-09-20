package main

import (
	"fmt"
	"time"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
	"onlinemenu.tr/pos-desktop/internal/receipt"
)

// KitchenNoticeItemDTO is one line of a notice (moved items only).
type KitchenNoticeItemDTO struct {
	Name     string `json:"name"`
	Quantity int    `json:"quantity"`
}

// KitchenNoticeDTO is an information slip for the kitchen (docs/pos-ux-spec.md
// §3c): an adisyon changed tables, two were merged, or items moved. The frontend
// keeps it so a failed print can be retried with PrintKitchenNotice.
// Kind is "transfer", "merge" or "move-items".
type KitchenNoticeDTO struct {
	Kind  string                 `json:"kind"`
	From  string                 `json:"from"`
	To    string                 `json:"to"`
	Items []KitchenNoticeItemDTO `json:"items"`
}

// CheckMoveResultDTO is what transfer / merge / move-items answer: the check the
// server returned plus what happened to the kitchen notice. A notice that could
// not be printed never fails the move — the adisyon is already where the cashier
// put it — it is reported here instead so the frontend can offer a reprint.
// Notice is nil when no slip was needed (the kitchen has nothing to act on).
type CheckMoveResultDTO struct {
	Check       CheckDTO          `json:"check"`
	Notice      *KitchenNoticeDTO `json:"notice,omitempty"`
	NoticeError string            `json:"notice_error,omitempty"`
}

// activeOrderStatuses are the statuses the kitchen still works on
// (pos/domain.KitchenActiveOrderStatuses). A delivered, rejected or cancelled
// order is nothing a cook can send to the wrong table.
var activeOrderStatuses = map[string]bool{
	"pending":   true,
	"accepted":  true,
	"preparing": true,
	"ready":     true,
}

func hasActiveOrder(orders []apiclient.Order) bool {
	for _, o := range orders {
		if activeOrderStatuses[o.Status] {
			return true
		}
	}
	return false
}

// checkNotice is the slip for a whole adisyon that moved (transfer) or was
// absorbed (merge). It answers nil when the adisyon has no active order: an
// adisyon that is entirely delivered concerns the kitchen no more.
func checkNotice(kind receipt.NoticeKind, orders []apiclient.Order, from, to string) *KitchenNoticeDTO {
	if !hasActiveOrder(orders) {
		return nil
	}
	return &KitchenNoticeDTO{Kind: string(kind), From: from, To: to, Items: []KitchenNoticeItemDTO{}}
}

// moveItemsNotice is the slip for items that moved to another table. Only items
// of orders the kitchen is still working on are listed — a dish that was
// already delivered is not the cook's business — and nil is answered when none
// of the moved items is such an item.
func moveItemsNotice(orders []apiclient.Order, itemIDs []string, from, to string) *KitchenNoticeDTO {
	moved := make(map[string]bool, len(itemIDs))
	for _, id := range itemIDs {
		moved[id] = true
	}
	items := []KitchenNoticeItemDTO{}
	for _, o := range orders {
		if !activeOrderStatuses[o.Status] {
			continue
		}
		for _, it := range o.Items {
			if moved[it.ID] {
				items = append(items, KitchenNoticeItemDTO{Name: it.ProductName, Quantity: it.Quantity})
			}
		}
	}
	if len(items) == 0 {
		return nil
	}
	return &KitchenNoticeDTO{Kind: string(receipt.NoticeMoveItems), From: from, To: to, Items: items}
}

// moveContext is what a move needs to know BEFORE it happens: afterwards the
// source's table label and (for a merge) its orders no longer point where they
// did.
type moveContext struct {
	label  string
	orders []apiclient.Order
	err    error
}

func (a *App) readMoveContext(checkID string) moveContext {
	check, err := a.api.GetCheck(a.ctx, checkID)
	if err != nil {
		return moveContext{err: err}
	}
	orders, err := a.api.ListCheckOrders(a.ctx, checkID)
	if err != nil {
		return moveContext{err: err}
	}
	return moveContext{label: check.TableLabel, orders: orders}
}

// finishMove wraps the server's answer with the kitchen notice. notice is nil
// when none is needed; ctxErr is set when the pre-move read failed, in which
// case nobody can tell whether the kitchen needs one — say so rather than
// stay silent.
func (a *App) finishMove(check apiclient.Check, notice *KitchenNoticeDTO, ctxErr error) CheckMoveResultDTO {
	res := CheckMoveResultDTO{Check: toCheckDTO(check)}
	if ctxErr != nil {
		res.NoticeError = fmt.Sprintf("mutfak bilgi fişi hazırlanamadı (adisyon bilgisi okunamadı): %v", ctxErr)
		return res
	}
	if notice == nil {
		return res
	}
	res.Notice = notice
	if err := a.printKitchenNotice(*notice); err != nil {
		res.NoticeError = err.Error()
	}
	return res
}

// PrintKitchenNotice prints an information slip on the kitchen printer. It is
// what a failed notice is retried with.
func (a *App) PrintKitchenNotice(n KitchenNoticeDTO) error {
	return a.printKitchenNotice(n)
}

func (a *App) printKitchenNotice(n KitchenNoticeDTO) error {
	if a.kitchenPrinter == nil {
		return fmt.Errorf("print kitchen notice: no kitchen printer available")
	}
	items := make([]receipt.KitchenItem, len(n.Items))
	for i, it := range n.Items {
		items[i] = receipt.KitchenItem{ProductName: it.Name, Quantity: it.Quantity}
	}
	job := receipt.BuildKitchenNotice(a.receiptConfig, receipt.NoticeKind(n.Kind), n.From, n.To, time.Now(), items)
	if err := a.kitchenPrinter.Print(job); err != nil {
		return fmt.Errorf("print kitchen notice: %w", err)
	}
	return nil
}

// TransferCheck moves an open adisyon to another (free) table and tells the
// kitchen, so a dish is not sent to the old table. It answers the moved check
// plus the outcome of the notice (see CheckMoveResultDTO).
func (a *App) TransferCheck(checkID, tableID string) (CheckMoveResultDTO, error) {
	before := a.readMoveContext(checkID)
	c, err := a.api.TransferCheck(a.ctx, checkID, tableID)
	if err != nil {
		return CheckMoveResultDTO{}, err
	}
	return a.finishMove(c, checkNotice(receipt.NoticeTransfer, before.orders, before.label, c.TableLabel), before.err), nil
}

// MergeChecks folds sourceCheckID into targetCheckID (the target survives, the
// source becomes "merged") and tells the kitchen. A source with any payment is
// refused by the server (409 payments_present). It answers the surviving check.
func (a *App) MergeChecks(targetCheckID, sourceCheckID string) (CheckMoveResultDTO, error) {
	before := a.readMoveContext(sourceCheckID)
	c, err := a.api.MergeChecks(a.ctx, targetCheckID, sourceCheckID)
	if err != nil {
		return CheckMoveResultDTO{}, err
	}
	return a.finishMove(c, checkNotice(receipt.NoticeMerge, before.orders, before.label, c.TableLabel), before.err), nil
}

// MoveCheckItems moves the given order items from sourceCheckID onto
// targetCheckID and tells the kitchen which items went where. It answers the
// TARGET check.
func (a *App) MoveCheckItems(sourceCheckID, targetCheckID string, orderItemIDs []string) (CheckMoveResultDTO, error) {
	before := a.readMoveContext(sourceCheckID)
	c, err := a.api.MoveCheckItems(a.ctx, sourceCheckID, targetCheckID, orderItemIDs)
	if err != nil {
		return CheckMoveResultDTO{}, err
	}
	return a.finishMove(c, moveItemsNotice(before.orders, orderItemIDs, before.label, c.TableLabel), before.err), nil
}
