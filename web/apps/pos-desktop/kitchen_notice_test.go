package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
	"onlinemenu.tr/pos-desktop/internal/hardware"
	"onlinemenu.tr/pos-desktop/internal/hardware/escpos"
	"onlinemenu.tr/pos-desktop/internal/receipt"
	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

func orderWith(status string, items ...apiclient.OrderItem) apiclient.Order {
	return apiclient.Order{ID: "o-" + status, Status: status, Items: items}
}

func TestCheckNotice_NeedsAtLeastOneOrderTheKitchenIsStillWorkingOn(t *testing.T) {
	tests := []struct {
		name     string
		statuses []string
		want     bool
	}{
		{"one pending order", []string{"pending"}, true},
		{"accepted", []string{"accepted"}, true},
		{"preparing", []string{"preparing"}, true},
		{"ready but not yet delivered", []string{"ready"}, true},
		{"everything delivered", []string{"delivered", "delivered"}, false},
		{"delivered plus one still cooking", []string{"delivered", "preparing"}, true},
		{"only cancelled and rejected", []string{"cancelled", "rejected"}, false},
		{"no orders at all", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var orders []apiclient.Order
			for _, s := range tt.statuses {
				orders = append(orders, orderWith(s))
			}
			got := checkNotice(receipt.NoticeTransfer, orders, "Masa 3", "Masa 7")
			if (got != nil) != tt.want {
				t.Fatalf("notice = %+v, want present=%v", got, tt.want)
			}
			if got != nil && (got.Kind != "transfer" || got.From != "Masa 3" || got.To != "Masa 7" || got.Items == nil) {
				t.Fatalf("notice = %+v (Items must be [] not null for the frontend)", got)
			}
		})
	}
}

func TestMoveItemsNotice_ListsOnlyMovedItemsTheKitchenStillOwns(t *testing.T) {
	orders := []apiclient.Order{
		orderWith("delivered", apiclient.OrderItem{ID: "d1", ProductName: "Çorba", Quantity: 1}),
		orderWith("preparing",
			apiclient.OrderItem{ID: "p1", ProductName: "Lahmacun", Quantity: 2},
			apiclient.OrderItem{ID: "p2", ProductName: "Ayran", Quantity: 1},
		),
	}

	got := moveItemsNotice(orders, []string{"d1", "p1"}, "Masa 3", "Masa 7")
	if got == nil || got.Kind != "move-items" {
		t.Fatalf("notice = %+v", got)
	}
	if len(got.Items) != 1 || got.Items[0].Name != "Lahmacun" || got.Items[0].Quantity != 2 {
		t.Fatalf("items = %+v, want only the moved item still in the kitchen (Lahmacun ×2)", got.Items)
	}

	if moveItemsNotice(orders, []string{"d1"}, "Masa 3", "Masa 7") != nil {
		t.Fatal("moving only delivered items concerns the kitchen not at all — no slip")
	}
	if moveItemsNotice(orders, []string{"nope"}, "Masa 3", "Masa 7") != nil {
		t.Fatal("an id that matches nothing must not produce a slip")
	}
}

// --- App level: what really happens around the server call ---

type moveBackend struct {
	srv       *httptest.Server
	labels    map[string]string
	orders    map[string][]apiclient.Order
	moveAnswr apiclient.Check
	failMove  bool
}

func newMoveBackend(t *testing.T) *moveBackend {
	t.Helper()
	mb := &moveBackend{labels: map[string]string{}, orders: map[string][]apiclient.Order{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/pos/checks/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		_ = json.NewEncoder(w).Encode(apiclient.Check{ID: id, TableLabel: mb.labels[id], Status: "open"})
	})
	mux.HandleFunc("GET /api/v1/pos/checks/{id}/orders", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(mb.orders[r.PathValue("id")])
	})
	move := func(w http.ResponseWriter, r *http.Request) {
		if mb.failMove {
			http.Error(w, `{"error":"x","code":"table_occupied"}`, http.StatusConflict)
			return
		}
		_ = json.NewEncoder(w).Encode(mb.moveAnswr)
	}
	mux.HandleFunc("POST /api/v1/pos/checks/{id}/transfer", move)
	mux.HandleFunc("POST /api/v1/pos/checks/{id}/merge", move)
	mux.HandleFunc("POST /api/v1/pos/checks/{id}/move-items", move)
	mb.srv = httptest.NewServer(mux)
	t.Cleanup(mb.srv.Close)
	return mb
}

func (mb *moveBackend) app(t *testing.T, kitchen hardware.Printer) *App {
	t.Helper()
	return &App{
		ctx:            context.Background(),
		api:            apiclient.New(mb.srv.URL, tokenstore.New(t.TempDir(), nil)),
		kitchenPrinter: kitchen,
		receiptConfig:  receipt.Config{Width: escpos.Width48},
	}
}

func TestTransferCheck_PrintsAnOldToNewNoticeOnTheKitchenPrinter(t *testing.T) {
	mb := newMoveBackend(t)
	mb.labels["c1"] = "Masa 3"
	mb.orders["c1"] = []apiclient.Order{orderWith("preparing")}
	mb.moveAnswr = apiclient.Check{ID: "c1", TableLabel: "Masa 7", Status: "open"}
	kitchen := hardware.NewMockPrinter()

	res, err := mb.app(t, kitchen).TransferCheck("c1", "t7")
	if err != nil {
		t.Fatalf("TransferCheck: %v", err)
	}
	if res.Check.TableLabel != "Masa 7" || res.NoticeError != "" {
		t.Fatalf("result = %+v", res)
	}
	if res.Notice == nil || res.Notice.From != "Masa 3" || res.Notice.To != "Masa 7" {
		t.Fatalf("notice = %+v — the source label must be read BEFORE the move", res.Notice)
	}
	job := kitchen.LastJob()
	if !bytes.Contains(job, escpos.EncodeCP857("MASA TAŞINDI")) || !bytes.Contains(job, []byte("Masa 3")) || !bytes.Contains(job, []byte("-> Masa 7")) {
		t.Fatalf("kitchen job did not carry the notice:\n%q", job)
	}
}

func TestTransferCheck_NothingIsPrintedWhenEverythingWasDelivered(t *testing.T) {
	mb := newMoveBackend(t)
	mb.labels["c1"] = "Masa 3"
	mb.orders["c1"] = []apiclient.Order{orderWith("delivered"), orderWith("delivered")}
	mb.moveAnswr = apiclient.Check{ID: "c1", TableLabel: "Masa 7", Status: "open"}
	kitchen := hardware.NewMockPrinter()

	res, err := mb.app(t, kitchen).TransferCheck("c1", "t7")
	if err != nil {
		t.Fatalf("TransferCheck: %v", err)
	}
	if res.Notice != nil || res.NoticeError != "" || kitchen.LastJob() != nil {
		t.Fatalf("notice=%+v err=%q job=%v — a fully delivered adisyon must not print", res.Notice, res.NoticeError, kitchen.LastJob() != nil)
	}
}

func TestMergeChecks_NoticeGoesFromTheSourceToTheSurvivor(t *testing.T) {
	mb := newMoveBackend(t)
	mb.labels["src"] = "Masa 3"
	mb.orders["src"] = []apiclient.Order{orderWith("accepted")}
	mb.moveAnswr = apiclient.Check{ID: "dst", TableLabel: "Masa 2", Status: "open"}
	kitchen := hardware.NewMockPrinter()

	res, err := mb.app(t, kitchen).MergeChecks("dst", "src")
	if err != nil {
		t.Fatalf("MergeChecks: %v", err)
	}
	if res.Notice == nil || res.Notice.Kind != "merge" || res.Notice.From != "Masa 3" || res.Notice.To != "Masa 2" {
		t.Fatalf("notice = %+v", res.Notice)
	}
	if !bytes.Contains(kitchen.LastJob(), escpos.EncodeCP857("BİRLEŞTİ")) {
		t.Fatal("the merge slip was not printed")
	}
}

func TestMoveCheckItems_NoticeListsWhatMoved(t *testing.T) {
	mb := newMoveBackend(t)
	mb.labels["src"] = "Masa 3"
	mb.orders["src"] = []apiclient.Order{orderWith("preparing",
		apiclient.OrderItem{ID: "i1", ProductName: "Lahmacun", Quantity: 2},
		apiclient.OrderItem{ID: "i2", ProductName: "Ayran", Quantity: 1},
	)}
	mb.moveAnswr = apiclient.Check{ID: "dst", TableLabel: "Masa 7", Status: "open"}
	kitchen := hardware.NewMockPrinter()

	res, err := mb.app(t, kitchen).MoveCheckItems("src", "dst", []string{"i1"})
	if err != nil {
		t.Fatalf("MoveCheckItems: %v", err)
	}
	if res.Notice == nil || len(res.Notice.Items) != 1 || res.Notice.Items[0].Name != "Lahmacun" {
		t.Fatalf("notice = %+v", res.Notice)
	}
	job := kitchen.LastJob()
	if !bytes.Contains(job, escpos.EncodeCP857("KALEM TAŞINDI")) || !bytes.Contains(job, []byte("2x  Lahmacun")) {
		t.Fatalf("kitchen job missing the moved item:\n%q", job)
	}
	if bytes.Contains(job, []byte("Ayran")) {
		t.Fatal("an item that did not move must not be on the slip")
	}
}

type failingPrinter struct{ hardware.Printer }

func (failingPrinter) Print([]byte) error { return errors.New("paper jam") }

func TestTransferCheck_APrinterFaultNeverUndoesTheMove(t *testing.T) {
	mb := newMoveBackend(t)
	mb.labels["c1"] = "Masa 3"
	mb.orders["c1"] = []apiclient.Order{orderWith("pending")}
	mb.moveAnswr = apiclient.Check{ID: "c1", TableLabel: "Masa 7", Status: "open"}

	res, err := mb.app(t, failingPrinter{hardware.NewMockPrinter()}).TransferCheck("c1", "t7")
	if err != nil {
		t.Fatalf("a print fault must not fail the transfer, got %v", err)
	}
	if res.Check.TableLabel != "Masa 7" {
		t.Fatalf("check = %+v", res.Check)
	}
	if res.Notice == nil || !strings.Contains(res.NoticeError, "paper jam") {
		t.Fatalf("notice=%+v err=%q — the fault must be reported with the notice so it can be reprinted", res.Notice, res.NoticeError)
	}
}

func TestTransferCheck_ServerRefusalPrintsNothing(t *testing.T) {
	mb := newMoveBackend(t)
	mb.labels["c1"] = "Masa 3"
	mb.orders["c1"] = []apiclient.Order{orderWith("pending")}
	mb.failMove = true
	kitchen := hardware.NewMockPrinter()

	if _, err := mb.app(t, kitchen).TransferCheck("c1", "t7"); err == nil || !strings.Contains(err.Error(), "table_occupied") {
		t.Fatalf("err = %v, want the table_occupied refusal", err)
	}
	if kitchen.LastJob() != nil {
		t.Fatal("a refused move printed a slip for something that did not happen")
	}
}

func TestTransferCheck_UnreadableContextIsReportedNotSilent(t *testing.T) {
	mb := newMoveBackend(t)
	mb.moveAnswr = apiclient.Check{ID: "c1", TableLabel: "Masa 7", Status: "open"}
	a := mb.app(t, hardware.NewMockPrinter())
	// Only the move itself is served: every read the notice needs now 404s.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/pos/checks/{id}/transfer", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(mb.moveAnswr)
	})
	mb.srv.Config.Handler = mux

	res, err := a.TransferCheck("c1", "t7")
	if err != nil {
		t.Fatalf("TransferCheck: %v", err)
	}
	if res.Notice != nil || !strings.Contains(res.NoticeError, "bilgi fişi hazırlanamadı") {
		t.Fatalf("notice=%+v err=%q — when the kitchen's need cannot be decided, say so", res.Notice, res.NoticeError)
	}
}

func TestPrintKitchenNotice_RetryPrintsTheSameSlip(t *testing.T) {
	kitchen := hardware.NewMockPrinter()
	a := &App{ctx: context.Background(), kitchenPrinter: kitchen, receiptConfig: receipt.Config{Width: escpos.Width48}}

	err := a.PrintKitchenNotice(KitchenNoticeDTO{Kind: "move-items", From: "Masa 3", To: "Masa 7", Items: []KitchenNoticeItemDTO{{Name: "Ayran", Quantity: 3}}})
	if err != nil {
		t.Fatalf("PrintKitchenNotice: %v", err)
	}
	if !bytes.Contains(kitchen.LastJob(), []byte("3x  Ayran")) {
		t.Fatal("retry did not print the item")
	}
	if err := (&App{ctx: context.Background()}).PrintKitchenNotice(KitchenNoticeDTO{Kind: "transfer"}); err == nil {
		t.Fatal("with no kitchen printer the retry must fail loudly")
	}
}
