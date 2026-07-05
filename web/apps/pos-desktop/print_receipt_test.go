package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
	"onlinemenu.tr/pos-desktop/internal/hardware"
	"onlinemenu.tr/pos-desktop/internal/hardware/escpos"
	"onlinemenu.tr/pos-desktop/internal/receipt"
	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

// This file exercises App.PrintReceipt's glue — GetCheck + ListCheckOrders
// -> receipt.Item flattening -> receipt.Build -> hardware.Printer.Print —
// against an httptest fake backend and hardware.MockPrinter.LastJob(),
// matching app_test.go's existing pattern of building an App literal
// directly rather than going through NewApp/startup (no real Wails runtime
// context is available in a Go test).

// newFakeBackendForPrintReceipt serves exactly the two GET endpoints
// PrintReceipt calls (see apiclient.Client.GetCheck / ListCheckOrders),
// unconditionally — matching the actual backend handlers this was verified
// against (pos/repo's CheckRepo.GetByID / OrderRepo.ListByCheck have no
// WHERE status = 'open' filter, and pos.check.read/pos.order.read are
// granted to "cashier" unconditionally, not scoped to open checks — see
// backend/configs/opa/bundles/authz.rego's pos_counter_actions), so serving
// a closed check's data here is not a test shortcut, it is what the real
// backend does too.
func newFakeBackendForPrintReceipt(t *testing.T, checkID string, check apiclient.Check, orders []apiclient.Order) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/pos/checks/"+checkID, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(check); err != nil {
			t.Fatalf("encode check: %v", err)
		}
	})
	mux.HandleFunc("/api/v1/pos/checks/"+checkID+"/orders", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(orders); err != nil {
			t.Fatalf("encode orders: %v", err)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestPrintReceipt_BuildsAndPrintsExpectedJob(t *testing.T) {
	const checkID = "check-1"
	checkIDVal := checkID
	opened := time.Date(2026, 7, 5, 12, 30, 0, 0, time.UTC)
	check := apiclient.Check{ID: checkID, BranchID: "b1", TableLabel: "Masa 7", Status: "open", OpenedAt: opened}
	orders := []apiclient.Order{
		{
			ID: "o1", CheckID: &checkIDVal, Status: "sent",
			Items: []apiclient.OrderItem{
				{ID: "i1", ProductID: "p1", ProductName: "Çay", Quantity: 2, UnitPriceAmount: 500},
				{ID: "i2", ProductID: "p2", ProductName: "Su", Quantity: 1, UnitPriceAmount: 300},
			},
		},
	}
	srv := newFakeBackendForPrintReceipt(t, checkID, check, orders)

	printer := hardware.NewMockPrinter()
	a := &App{
		ctx:     context.Background(),
		api:     apiclient.New(srv.URL, tokenstore.New(t.TempDir(), nil)),
		printer: printer,
		receiptConfig: receipt.Config{
			BusinessName: "Test Lokanta",
			BranchName:   "Merkez",
			Width:        escpos.Width48,
		},
	}

	const receivedAmount = int64(1500)
	if err := a.PrintReceipt(checkID, receivedAmount); err != nil {
		t.Fatalf("PrintReceipt: %v", err)
	}

	got := printer.LastJob()
	if got == nil {
		t.Fatal("PrintReceipt did not call Printer.Print (MockPrinter.LastJob() is nil)")
	}

	want := receipt.Build(
		a.receiptConfig,
		check.TableLabel,
		check.OpenedAt,
		[]receipt.Item{
			{ProductName: "Çay", Quantity: 2, UnitPriceAmount: 500},
			{ProductName: "Su", Quantity: 1, UnitPriceAmount: 300},
		},
		receivedAmount,
	)
	if !bytes.Equal(got, want) {
		t.Fatalf("printed job =\n% x\nwant\n% x", got, want)
	}

	// Cross-check against a concrete, independently-computed expectation
	// (not just receipt.Build called a second time with the same
	// arguments) — pins that PrintReceipt actually flattens orders' items
	// and forwards receivedAmount, rather than e.g. silently dropping
	// items or always passing 0.
	wantItemLine := escpos.EncodeCP857(escpos.Columns(48, "2x Çay", "10,00 TL"))
	if !bytes.Contains(got, wantItemLine) {
		t.Fatal("printed job does not contain the expected item line for the first order's first item")
	}
	wantReceivedLine := escpos.EncodeCP857(escpos.Columns(48, "ALINAN", "15,00 TL"))
	if !bytes.Contains(got, wantReceivedLine) {
		t.Fatal("printed job does not contain the expected ALINAN line for the passed-in receivedAmount")
	}
}

func TestPrintReceipt_PrinterFailurePropagatesError(t *testing.T) {
	const checkID = "check-2"
	check := apiclient.Check{ID: checkID, TableLabel: "Masa 1", Status: "closed", OpenedAt: time.Now()}
	srv := newFakeBackendForPrintReceipt(t, checkID, check, nil)

	// A NetworkPrinter that was never started (no run loop) has no
	// connection — Print returns hardware.ErrPrinterNotConnected, exercising
	// PrintReceipt's error-wrapping path without needing any TCP server.
	printer := hardware.NewNetworkPrinter("127.0.0.1:0")
	a := &App{
		ctx:           context.Background(),
		api:           apiclient.New(srv.URL, tokenstore.New(t.TempDir(), nil)),
		printer:       printer,
		receiptConfig: receipt.Config{Width: escpos.Width32},
	}

	if err := a.PrintReceipt(checkID, 0); err == nil {
		t.Fatal("PrintReceipt: want error when the printer is not connected, got nil")
	}
}
