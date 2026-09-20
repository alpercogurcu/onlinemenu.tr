//go:build live

package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

// Live smoke: drives the App bindings (the same methods the webview calls)
// against a RUNNING dev API, end to end — cash session, option product order
// with server-side price validation, item-based payment with fiscal lines,
// card payment, close, and the transfer / merge / move-items endpoints.
//
//	task pos:test:live                        # http://localhost:8081
//	POS_LIVE_API_URL=http://host:8081 task pos:test:live
//
// It writes real rows to that API's database (checks named SMOKE-*, payments)
// and cleans up after itself where the API allows it: every check it opens is
// paid and closed (a merged one cannot be), and a cash session it had to open
// is closed again. Never point it at production.

const (
	liveDefaultEmail = "kasiyer@dev.onlinemenu.tr"
	liveSettleWait   = 25 * time.Second
)

type liveTokenStore struct {
	mu    sync.Mutex
	token string
}

func (s *liveTokenStore) Save(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = token
	return nil
}

func (s *liveTokenStore) Load() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token == "" {
		return "", tokenstore.ErrNoToken
	}
	return s.token, nil
}

func (s *liveTokenStore) Clear() error { return s.Save("") }

type live struct {
	t      *testing.T
	a      *App
	branch string
}

func (l *live) must(err error, what string) {
	l.t.Helper()
	if err != nil {
		l.t.Fatalf("%s: %v", what, err)
	}
}

// wantErr asserts that err is a failure carrying the given machine-readable code.
func (l *live) wantErr(err error, code, what string) {
	l.t.Helper()
	if err == nil {
		l.t.Fatalf("%s: expected an error with code %q, got success", what, code)
	}
	if !strings.Contains(err.Error(), `"code":"`+code+`"`) {
		l.t.Fatalf("%s: error = %v, want code %q", what, err, code)
	}
}

// step runs fn as a subtest whose failures stop the whole run: every later step
// depends on the state the earlier ones built.
func (l *live) step(name string, fn func(t *testing.T)) {
	parent := l.t
	ok := parent.Run(name, func(t *testing.T) {
		l.t = t
		defer func() { l.t = parent }()
		fn(t)
	})
	if !ok {
		parent.FailNow()
	}
}

func (l *live) orders(checkID string) []OrderDTO {
	l.t.Helper()
	orders, err := l.a.ListCheckOrders(checkID)
	l.must(err, "ListCheckOrders "+checkID)
	return orders
}

func (l *live) items(checkID string) []OrderItemDTO {
	var out []OrderItemDTO
	for _, o := range l.orders(checkID) {
		out = append(out, o.Items...)
	}
	return out
}

func itemsTotal(items []OrderItemDTO) int64 {
	var total int64
	for _, it := range items {
		total += int64(it.Quantity) * it.UnitPriceAmount
	}
	return total
}

// settlement polls until no payment on the check is still awaiting its fiscal
// record (the mock adapter settles asynchronously) and returns the settled sum.
func (l *live) settledTotal(checkID string) int64 {
	l.t.Helper()
	deadline := time.Now().Add(liveSettleWait)
	for {
		s, err := l.a.CheckSettlement(checkID)
		l.must(err, "CheckSettlement")
		if s.PendingTotal == 0 {
			var sum int64
			for _, c := range s.Completed {
				sum += c.AmountTotal
			}
			return sum
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("payments on %s still pending after %s (pending %d) — is the fiscal worker running?", checkID, liveSettleWait, s.PendingTotal)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// exactLines are the items as they are: quantity and unit price unchanged.
func exactLines(items []OrderItemDTO) []PaymentLineDTO {
	lines := make([]PaymentLineDTO, len(items))
	for i, it := range items {
		lines[i] = PaymentLineDTO{ProductID: it.ProductID, Name: it.ProductName, UnitPriceMinor: it.UnitPriceAmount, QuantityMilli: int64(it.Quantity) * 1000}
	}
	return lines
}

// shareLines mirrors frontend lib/paymentLines.ts: the amount shared across the
// items in proportion to their cost (largest remainder), one unit each.
func shareLines(items []OrderItemDTO, amount int64) []PaymentLineDTO {
	total := itemsTotal(items)
	if amount == total {
		return exactLines(items)
	}
	type share struct {
		floor, rest int64
		idx         int
	}
	shares := make([]share, len(items))
	var floors int64
	for i, it := range items {
		exact := amount * int64(it.Quantity) * it.UnitPriceAmount
		shares[i] = share{floor: exact / total, rest: exact % total, idx: i}
		floors += shares[i].floor
	}
	leftover := amount - floors
	order := append([]share(nil), shares...)
	sort.SliceStable(order, func(a, b int) bool { return order[a].rest > order[b].rest })
	for i := 0; leftover > 0 && i < len(order); i++ {
		shares[order[i].idx].floor++
		leftover--
	}
	var lines []PaymentLineDTO
	for i, it := range items {
		if shares[i].floor > 0 {
			lines = append(lines, PaymentLineDTO{ProductID: it.ProductID, Name: it.ProductName, UnitPriceMinor: shares[i].floor, QuantityMilli: 1000})
		}
	}
	return lines
}

func (l *live) pay(checkID, method string, amount int64, lines []PaymentLineDTO) PaymentDTO {
	l.t.Helper()
	p, err := l.a.RegisterPayment(RegisterPaymentInputDTO{
		BranchID: l.branch, CheckID: checkID, Method: method, AmountTotal: amount, Lines: lines, TableLabel: "SMOKE",
	})
	l.must(err, fmt.Sprintf("RegisterPayment %s %d", method, amount))
	if p.ID == "" || p.AmountTotal != amount {
		l.t.Fatalf("payment = %+v, want amount %d", p, amount)
	}
	return p
}

// settleAndClose pays whatever is left with a proportional basket and closes.
func (l *live) settleAndClose(checkID string) {
	l.t.Helper()
	items := l.items(checkID)
	total := itemsTotal(items)
	settled := l.settledTotal(checkID)
	if remaining := total - settled; remaining > 0 {
		l.pay(checkID, "cash", remaining, shareLines(items, remaining))
		l.settledTotal(checkID)
	}
	_, err := l.a.CloseCheck(checkID)
	l.must(err, "CloseCheck "+checkID)
}

func (l *live) openCheck(tableID, label string) CheckDTO {
	l.t.Helper()
	c, err := l.a.OpenCheck(l.branch, tableID, label, "")
	l.must(err, "OpenCheck "+label)
	// A failed run must not leave open adisyons behind on the dev branch.
	l.t.Cleanup(func() {
		got, err := l.a.GetCheck(c.ID)
		if err != nil || got.Status != "open" {
			return
		}
		func() {
			defer func() { _ = recover() }()
			l.settleAndCloseBestEffort(c.ID)
		}()
	})
	return c
}

func (l *live) settleAndCloseBestEffort(checkID string) {
	items := l.items(checkID)
	total := itemsTotal(items)
	settled := l.settledTotalBestEffort(checkID)
	if remaining := total - settled; remaining > 0 {
		if _, err := l.a.RegisterPayment(RegisterPaymentInputDTO{BranchID: l.branch, CheckID: checkID, Method: "cash", AmountTotal: remaining, Lines: shareLines(items, remaining)}); err != nil {
			l.t.Logf("cleanup: pay %s: %v", checkID, err)
			return
		}
		l.settledTotalBestEffort(checkID)
	}
	if _, err := l.a.CloseCheck(checkID); err != nil {
		l.t.Logf("cleanup: close %s: %v", checkID, err)
	}
}

func (l *live) settledTotalBestEffort(checkID string) int64 {
	deadline := time.Now().Add(liveSettleWait)
	for time.Now().Before(deadline) {
		s, err := l.a.CheckSettlement(checkID)
		if err == nil && s.PendingTotal == 0 {
			var sum int64
			for _, c := range s.Completed {
				sum += c.AmountTotal
			}
			return sum
		}
		time.Sleep(300 * time.Millisecond)
	}
	return 0
}

func (l *live) place(checkID string, items ...OrderItemInputDTO) OrderDTO {
	l.t.Helper()
	o, err := l.a.PlaceOrder(l.branch, checkID, items)
	l.must(err, "PlaceOrder")
	return o
}

func plainInput(p ProductDTO, qty int) OrderItemInputDTO {
	return OrderItemInputDTO{
		ProductID: p.ID, ProductName: p.Name, ProductPriceAmount: p.PriceAmount, ProductCurrency: "TRY",
		TaxRateBPS: p.TaxRateBPS, Quantity: qty, UnitPriceAmount: p.PriceAmount, ModifierIDs: []string{},
	}
}

func TestLiveSmoke(t *testing.T) {
	base := os.Getenv("POS_LIVE_API_URL")
	if base == "" {
		t.Skip("POS_LIVE_API_URL not set — run via `task pos:test:live`")
	}
	email := os.Getenv("POS_LIVE_EMAIL")
	if email == "" {
		email = liveDefaultEmail
	}
	ctx := context.Background()
	api := apiclient.New(base, &liveTokenStore{})
	session, err := api.Login(ctx, email)
	if err != nil {
		t.Fatalf("dev login as %s against %s: %v", email, base, err)
	}
	if session.BranchID == "" {
		t.Fatalf("session for %s has no branch", email)
	}
	l := &live{t: t, a: &App{ctx: ctx, api: api}, branch: session.BranchID}
	t.Logf("logged in as %s, branch %s", email, l.branch)

	// --- 1. cash session: use the open one or open (and later close) ours ---
	state, err := l.a.GetActiveCashSession(l.branch)
	l.must(err, "GetActiveCashSession")
	if state.HasActiveSession {
		t.Logf("cash session %s already open (%s) — using it", state.Session.ID, state.Session.Status)
	} else {
		opened, err := l.a.OpenCashSession(l.branch, 0, "live smoke")
		l.must(err, "OpenCashSession")
		t.Logf("opened cash session %s", opened.ID)
		t.Cleanup(func() {
			cur, err := l.a.GetActiveCashSession(l.branch)
			if err != nil || !cur.HasActiveSession || cur.Session.ID != opened.ID {
				return
			}
			if _, err := l.a.SubmitClosingCount(opened.ID, cur.Session.ExpectedClose, nil, "live smoke"); err != nil {
				t.Logf("cleanup: submit closing count: %v", err)
				return
			}
			if _, err := l.a.CloseCashSession(opened.ID); err != nil {
				t.Logf("cleanup: close cash session: %v", err)
			}
		})
	}

	// --- 2. catalog: an option product (through the option resolver) + plain ones ---
	cats, err := l.a.ListCategories()
	l.must(err, "ListCategories")
	var optionCandidates []ProductDTO
	var plain []ProductDTO
	for _, c := range cats {
		products, err := l.a.ListProducts(c.ID)
		l.must(err, "ListProducts "+c.Name)
		for _, p := range products {
			switch {
			case len(p.ModifierGroups) > 0 && !p.OptionsUnavailable:
				optionCandidates = append(optionCandidates, p)
			case len(p.ModifierGroups) == 0 && !p.OptionsUnavailable && p.PriceAmount > 0 && len(plain) < 2:
				plain = append(plain, p)
			}
		}
	}
	if len(optionCandidates) == 0 {
		t.Fatal("no product with option groups in the dev catalog — cannot exercise the option flow")
	}
	if len(plain) < 2 {
		t.Fatalf("need two plain products, found %d", len(plain))
	}

	// optionLine picks the fast path the picker would: the first option of every
	// required group (and of the first group if none is required), and prices it
	// as base + Σ deltas — the formula the server validates.
	optionLine := func(p ProductDTO) (OrderItemInputDTO, int64, []string) {
		best := func(g ModifierGroupDTO) ModifierDTO {
			m := g.Modifiers[0]
			for _, cand := range g.Modifiers {
				if cand.PriceDelta > m.PriceDelta {
					m = cand
				}
			}
			return m
		}
		unit := p.PriceAmount
		var ids, names []string
		for i, g := range p.ModifierGroups {
			m := best(g)
			// Required groups (and the first group, as the picker's fast path
			// would) are always answered; an optional group is answered only when
			// its best option changes the price — the point here is to exercise
			// base + Σ deltas with something other than zeros.
			if g.IsRequired || i == 0 || m.PriceDelta > 0 {
				ids, names = append(ids, m.ID), append(names, m.Name)
				unit += m.PriceDelta
			}
		}
		return OrderItemInputDTO{
			ProductID: p.ID, ProductName: p.Name, ProductPriceAmount: p.PriceAmount, ProductCurrency: "TRY",
			TaxRateBPS: p.TaxRateBPS, Quantity: 1, UnitPriceAmount: unit, Note: strings.Join(names, " | "), ModifierIDs: ids,
		}, unit, names
	}

	// Prefer a product whose options actually change the price, so the
	// base + Σ deltas formula is exercised with something other than zeros.
	lineDelta := func(p ProductDTO) int64 {
		_, unit, _ := optionLine(p)
		return unit - p.PriceAmount
	}
	sort.SliceStable(optionCandidates, func(i, j int) bool { return lineDelta(optionCandidates[i]) > lineDelta(optionCandidates[j]) })
	if lineDelta(optionCandidates[0]) == 0 {
		t.Log("WARNING: no option product in the dev catalog carries a price difference — base + Σ deltas is only exercised with 0")
	}

	// --- 3. order: server price validation ---
	a := l.openCheck("", "SMOKE-A")
	var optionProduct ProductDTO
	var optionInput OrderItemInputDTO
	var unit int64
	l.step("server rejects a manipulated price", func(t *testing.T) {
		// The dev catalog holds leftover test products the server may refuse
		// outright (invalid_order_line). Use the first candidate that gets as
		// far as the price check, and say which ones did not.
		for _, cand := range optionCandidates {
			line, u, names := optionLine(cand)
			bad := line
			bad.UnitPriceAmount = u - 1
			_, err := l.a.PlaceOrder(l.branch, a.ID, []OrderItemInputDTO{bad})
			if err != nil && strings.Contains(err.Error(), `"code":"invalid_order_line"`) {
				// The grid must never offer what the server will not sell.
				t.Errorf("the POS offers %q (options %v) but the server refuses it as not sellable: %v", cand.Name, names, err)
				continue
			}
			l.wantErr(err, "price_mismatch", "PlaceOrder with a price 1 kuruş too low for "+cand.Name)
			optionProduct, optionInput, unit = cand, line, u
			if u == cand.PriceAmount {
				t.Log("WARNING: the accepted option product has no price difference — the price formula is only exercised with 0")
			}
			t.Logf("option product %q base %d + options %v => unit %d", cand.Name, cand.PriceAmount, names, u)
			break
		}
		if optionProduct.ID == "" {
			t.Fatalf("none of the %d option products was accepted by the server — the option flow cannot be exercised", len(optionCandidates))
		}
		if n := len(l.items(a.ID)); n != 0 {
			t.Fatalf("a rejected order left %d items on the check", n)
		}
	})
	l.place(a.ID, optionInput, plainInput(plain[0], 2), plainInput(plain[1], 1))
	aItems := l.items(a.ID)
	if len(aItems) != 3 {
		t.Fatalf("check A has %d items, want 3", len(aItems))
	}
	total := itemsTotal(aItems)
	wantTotal := unit + 2*plain[0].PriceAmount + plain[1].PriceAmount
	if total != wantTotal {
		t.Fatalf("check total %d, want %d", total, wantTotal)
	}

	// --- 4. item payment: only the two plain[0] units, lines exactly those ---
	l.step("a basket that names an unknown product is refused before any money moves", func(t *testing.T) {
		_, err := l.a.RegisterPayment(RegisterPaymentInputDTO{
			BranchID: l.branch, CheckID: a.ID, Method: "cash", AmountTotal: 100,
			Lines: []PaymentLineDTO{{ProductID: "00000000-0000-0000-0000-00000000dead", Name: "Yok", UnitPriceMinor: 100, QuantityMilli: 1000}},
		})
		if err == nil {
			t.Fatal("expected a refusal")
		}
		if got := l.settledTotal(a.ID); got != 0 {
			t.Fatalf("settled %d after a refused payment", got)
		}
	})
	var pickedItem []OrderItemDTO
	for _, it := range aItems {
		if it.ProductID == plain[0].ID {
			pickedItem = append(pickedItem, it)
		}
	}
	first := 2 * plain[0].PriceAmount
	l.pay(a.ID, "cash", first, exactLines(pickedItem))
	if got := l.settledTotal(a.ID); got != first {
		t.Fatalf("settled %d after the item payment, want %d", got, first)
	}
	remaining := total - first
	t.Logf("item payment %d settled; remaining %d", first, remaining)

	// --- 5. closing early is refused with the machine-readable code ---
	_, err = l.a.CloseCheck(a.ID)
	l.wantErr(err, "insufficient_payment", "CloseCheck with a balance left")

	// --- 6. second payment: the rest by card, lines = the remaining items ---
	var rest []OrderItemDTO
	for _, it := range aItems {
		if it.ProductID != plain[0].ID {
			rest = append(rest, it)
		}
	}
	l.pay(a.ID, "card", remaining, exactLines(rest))
	if got := l.settledTotal(a.ID); got != total {
		t.Fatalf("settled %d after the card payment, want the full %d", got, total)
	}
	closed, err := l.a.CloseCheck(a.ID)
	l.must(err, "CloseCheck A")
	if closed.Status != "closed" {
		t.Fatalf("check A status %q after close", closed.Status)
	}

	// --- 7. a partial payment whose lines are shares, not the items ---
	b := l.openCheck("", "SMOKE-B")
	l.place(b.ID, plainInput(plain[0], 1), plainInput(plain[1], 1))
	bItems := l.items(b.ID)
	bTotal := itemsTotal(bItems)
	half := bTotal / 3
	l.pay(b.ID, "cash", half, shareLines(bItems, half))
	if got := l.settledTotal(b.ID); got != half {
		t.Fatalf("settled %d after the partial payment, want %d", got, half)
	}
	_, err = l.a.CloseCheck(b.ID)
	l.wantErr(err, "insufficient_payment", "CloseCheck B before the rest is paid")
	l.settleAndClose(b.ID)

	// --- 8. transfer / merge / move-items ---
	freeTables := func() []TableDTO {
		zones, err := l.a.ListTables(l.branch)
		l.must(err, "ListTables")
		var free []TableDTO
		for _, z := range zones {
			for _, tb := range z.Tables {
				if tb.Status == "empty" && tb.IsActive {
					free = append(free, tb)
				}
			}
		}
		return free
	}
	free := freeTables()
	if len(free) < 3 {
		l.resetCleaningTables(base)
		free = freeTables()
	}
	// Registered before the checks below so it runs after their cleanups (LIFO):
	// the tables they close turn "cleaning" and would stay that way.
	t.Cleanup(func() { l.resetCleaningTables(base) })
	if len(free) < 3 {
		t.Skipf("only %d free tables — transfer/merge/move need 3; steps 1-7 passed", len(free))
	}
	t1, t2, t3 := free[0], free[1], free[2]

	x := l.openCheck(t1.ID, t1.Name)
	y := l.openCheck(t2.ID, t2.Name)
	l.place(x.ID, plainInput(plain[0], 1))
	l.place(y.ID, plainInput(plain[1], 1))

	l.step("transfer moves the adisyon and flips both tables in one go", func(t *testing.T) {
		moved, err := l.a.TransferCheck(x.ID, t3.ID)
		l.must(err, "TransferCheck")
		if moved.TableLabel != t3.Name {
			t.Fatalf("table label %q after transfer, want %q", moved.TableLabel, t3.Name)
		}
		status := l.tableStatuses()
		if status[t1.ID] != "empty" || status[t3.ID] != "occupied" {
			t.Fatalf("table statuses after transfer: t1=%q t3=%q, want empty/occupied", status[t1.ID], status[t3.ID])
		}
		again, err := l.a.TransferCheck(x.ID, t3.ID)
		l.must(err, "TransferCheck to the same table again")
		if again.ID != x.ID {
			t.Fatalf("repeat transfer answered %q", again.ID)
		}
		_, err = l.a.TransferCheck(y.ID, t3.ID)
		l.wantErr(err, "table_occupied", "TransferCheck onto an occupied table")
	})

	z := l.openCheck("", "SMOKE-Z")
	l.place(z.ID, plainInput(plain[0], 1), plainInput(plain[1], 1))
	l.step("move-items puts the chosen item on the target adisyon", func(t *testing.T) {
		var moveID string
		for _, it := range l.items(z.ID) {
			if it.ProductID == plain[1].ID {
				moveID = it.ID
			}
		}
		target, err := l.a.MoveCheckItems(z.ID, y.ID, []string{moveID})
		l.must(err, "MoveCheckItems")
		if target.ID != y.ID {
			t.Fatalf("move-items answered %q, want the target %q", target.ID, y.ID)
		}
		if n := len(l.items(y.ID)); n != 2 {
			t.Fatalf("target has %d items after the move, want 2", n)
		}
		for _, it := range l.items(z.ID) {
			if it.ID == moveID {
				t.Fatal("the moved item is still on the source")
			}
		}
	})

	l.step("merge refuses the same check and an adisyon that already has payments", func(t *testing.T) {
		_, err := l.a.MergeChecks(y.ID, y.ID)
		l.wantErr(err, "same_check", "MergeChecks into itself")

		zItems := l.items(z.ID)
		l.pay(z.ID, "cash", zItems[0].UnitPriceAmount, exactLines(zItems[:1]))
		_, err = l.a.MergeChecks(y.ID, z.ID)
		l.wantErr(err, "payments_present", "MergeChecks with a paid source")
	})

	l.step("merge folds the source into the target and frees its table", func(t *testing.T) {
		before := len(l.items(y.ID)) + len(l.items(x.ID))
		merged, err := l.a.MergeChecks(y.ID, x.ID)
		l.must(err, "MergeChecks")
		if merged.ID != y.ID {
			t.Fatalf("merge answered %q, want the surviving %q", merged.ID, y.ID)
		}
		if n := len(l.items(y.ID)); n != before {
			t.Fatalf("target has %d items after the merge, want %d", n, before)
		}
		src, err := l.a.GetCheck(x.ID)
		l.must(err, "GetCheck source")
		if src.Status != "merged" {
			t.Fatalf("source status %q, want merged", src.Status)
		}
		open, err := l.a.ListOpenChecks()
		l.must(err, "ListOpenChecks")
		for _, c := range open {
			if c.ID == x.ID {
				t.Fatal("a merged adisyon is still listed as open")
			}
		}
		if got := l.tableStatuses()[t3.ID]; got != "empty" {
			t.Fatalf("the merged source's table is %q, want empty", got)
		}
	})

	l.settleAndClose(y.ID)
	l.settleAndClose(z.ID)
	t.Log("live smoke passed")
}

// resetCleaningTables sets every table that is "cleaning" back to "empty" — ALL
// of them on the branch, not only the ones this smoke used, so a colleague's
// legitimately dirty table is marked clean too. Fine on a dev database; another
// reason never to point this at anything shared.
// Closing an adisyon turns its table into "cleaning", and a cashier cannot undo
// that (POST /pos/tables/{id}/status needs pos.table.manage) — so each run of
// this smoke would otherwise use up the branch's free tables. A shift manager
// can, which is what the counter staff does in practice. Best effort: without
// that login the transfer/merge/move steps skip when too few tables are free.
func (l *live) resetCleaningTables(base string) {
	l.t.Helper()
	email := os.Getenv("POS_LIVE_MANAGER_EMAIL")
	if email == "" {
		email = "shift@dev.onlinemenu.tr"
	}
	store := &liveTokenStore{}
	mgr := apiclient.New(base, store)
	if _, err := mgr.Login(context.Background(), email); err != nil {
		l.t.Logf("cannot log in as %s to free 'cleaning' tables: %v", email, err)
		return
	}
	token, _ := store.Load()
	statuses, err := l.tableStatusesBestEffort()
	if err != nil {
		l.t.Logf("cannot list tables to free 'cleaning' ones: %v", err)
		return
	}
	for id, status := range statuses {
		if status != "cleaning" {
			continue
		}
		req, err := http.NewRequest(http.MethodPost, base+"/api/v1/pos/tables/"+id+"/status", bytes.NewReader([]byte(`{"status":"empty"}`)))
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			l.t.Logf("reset table %s: %v", id, err)
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			l.t.Logf("reset table %s answered %d", id, resp.StatusCode)
		}
	}
}

func (l *live) tableStatusesBestEffort() (map[string]string, error) {
	zones, err := l.a.ListTables(l.branch)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, z := range zones {
		for _, tb := range z.Tables {
			out[tb.ID] = tb.Status
		}
	}
	return out, nil
}

func (l *live) tableStatuses() map[string]string {
	l.t.Helper()
	zones, err := l.a.ListTables(l.branch)
	l.must(err, "ListTables")
	out := map[string]string{}
	for _, z := range zones {
		for _, tb := range z.Tables {
			out[tb.ID] = tb.Status
		}
	}
	return out
}
