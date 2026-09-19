package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
	"onlinemenu.tr/pos-desktop/internal/config"
	"onlinemenu.tr/pos-desktop/internal/hardware"
	"onlinemenu.tr/pos-desktop/internal/hardware/escpos"
	"onlinemenu.tr/pos-desktop/internal/printedids"
	"onlinemenu.tr/pos-desktop/internal/receipt"
	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

// This file exercises App.PrintKitchenTicket's glue — GetOrder (+ GetCheck for
// the table label) -> receipt.KitchenItem flattening -> BuildKitchenTicket ->
// the KITCHEN hardware.Printer — with the same httptest + MockPrinter.LastJob
// approach print_receipt_test.go uses for the customer receipt.

const orderUUID = "a1b2c3d4-1111-2222-3333-444455556666"

type kitchenBackend struct {
	srv         *httptest.Server
	checkCalls  atomic.Int32
	orderStatus int
}

// newKitchenBackend serves GET /orders/{id} and GET /checks/{id}. orderStatus
// lets a test force a non-200 answer for the order.
func newKitchenBackend(t *testing.T, order apiclient.Order, check apiclient.Check, orderStatus int) *kitchenBackend {
	t.Helper()
	kb := &kitchenBackend{orderStatus: orderStatus}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/pos/orders/"+order.ID, func(w http.ResponseWriter, r *http.Request) {
		if kb.orderStatus != 0 && kb.orderStatus != http.StatusOK {
			http.Error(w, "boom", kb.orderStatus)
			return
		}
		if err := json.NewEncoder(w).Encode(order); err != nil {
			t.Fatalf("encode order: %v", err)
		}
	})
	mux.HandleFunc("/api/v1/pos/checks/"+check.ID, func(w http.ResponseWriter, r *http.Request) {
		kb.checkCalls.Add(1)
		if err := json.NewEncoder(w).Encode(check); err != nil {
			t.Fatalf("encode check: %v", err)
		}
	})
	kb.srv = httptest.NewServer(mux)
	t.Cleanup(kb.srv.Close)
	return kb
}

func kitchenTestApp(t *testing.T, kb *kitchenBackend, receiptPrinter, kitchenPrinter hardware.Printer) *App {
	t.Helper()
	return &App{
		ctx:            context.Background(),
		api:            apiclient.New(kb.srv.URL, tokenstore.New(t.TempDir(), nil)),
		printer:        receiptPrinter,
		kitchenPrinter: kitchenPrinter,
		receiptConfig:  receipt.Config{BusinessName: "Test Lokanta", Width: escpos.Width48},
	}
}

func sampleOrder(checkID *string) apiclient.Order {
	return apiclient.Order{
		ID:        orderUUID,
		CheckID:   checkID,
		Status:    "sent",
		CreatedAt: time.Date(2026, 9, 19, 14, 32, 0, 0, time.UTC),
		Items: []apiclient.OrderItem{
			{ID: "i1", ProductName: "Adana Kebap", Quantity: 2, UnitPriceAmount: 25000, Note: "acısız"},
			{ID: "i2", ProductName: "Ayran", Quantity: 1, UnitPriceAmount: 3000},
		},
	}
}

func TestPrintKitchenTicket_BuildsAndPrintsOnKitchenPrinter(t *testing.T) {
	checkID := "check-1"
	order := sampleOrder(&checkID)
	check := apiclient.Check{ID: checkID, TableLabel: "Masa 7", Status: "open", OpenedAt: time.Now()}
	kb := newKitchenBackend(t, order, check, 0)

	customer := hardware.NewMockPrinter()
	kitchen := hardware.NewMockPrinter()
	a := kitchenTestApp(t, kb, customer, kitchen)

	if err := a.PrintKitchenTicket(order.ID); err != nil {
		t.Fatalf("PrintKitchenTicket: %v", err)
	}

	want := receipt.BuildKitchenTicket(a.receiptConfig, "Masa 7", "a1b2c3d4", order.CreatedAt, []receipt.KitchenItem{
		{ProductName: "Adana Kebap", Quantity: 2, Note: "acısız"},
		{ProductName: "Ayran", Quantity: 1},
	})
	got := kitchen.LastJob()
	if got == nil {
		t.Fatal("kitchen printer received no job")
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("kitchen job =\n% x\nwant\n% x", got, want)
	}
	if customer.LastJob() != nil {
		t.Fatal("kitchen ticket leaked onto the customer receipt printer")
	}
	// Independent cross-check that prices really are absent, not just equal
	// to BuildKitchenTicket's own output.
	for _, forbidden := range []string{"250,00", "TL", "TOPLAM"} {
		if bytes.Contains(got, []byte(forbidden)) {
			t.Fatalf("kitchen job contains %q", forbidden)
		}
	}
}

func TestPrintKitchenTicket_SharedPrinterForSingleDeviceShop(t *testing.T) {
	checkID := "check-1"
	order := sampleOrder(&checkID)
	kb := newKitchenBackend(t, order, apiclient.Check{ID: checkID, TableLabel: "Masa 2"}, 0)

	only := hardware.NewMockPrinter()
	a := kitchenTestApp(t, kb, only, only)

	if err := a.PrintKitchenTicket(order.ID); err != nil {
		t.Fatalf("PrintKitchenTicket: %v", err)
	}
	if !bytes.Contains(only.LastJob(), escpos.EncodeCP857("MUTFAK")) {
		t.Fatal("shared printer did not receive the kitchen ticket")
	}
}

func TestPrintKitchenTicket_OrderWithoutCheckSkipsCheckLookup(t *testing.T) {
	order := sampleOrder(nil)
	kb := newKitchenBackend(t, order, apiclient.Check{ID: "unused"}, 0)

	kitchen := hardware.NewMockPrinter()
	a := kitchenTestApp(t, kb, hardware.NewMockPrinter(), kitchen)

	if err := a.PrintKitchenTicket(order.ID); err != nil {
		t.Fatalf("PrintKitchenTicket: %v", err)
	}
	if kb.checkCalls.Load() != 0 {
		t.Fatalf("GetCheck called %d times for an order with no check", kb.checkCalls.Load())
	}
	if !bytes.Contains(kitchen.LastJob(), []byte("Adisyon")) {
		t.Fatal(`table label must fall back to "Adisyon" when the order has no check`)
	}
}

func TestPrintKitchenTicket_Errors(t *testing.T) {
	checkID := "check-1"
	check := apiclient.Check{ID: checkID, TableLabel: "Masa 1"}
	emptyOrder := sampleOrder(&checkID)
	emptyOrder.Items = nil

	tests := []struct {
		name        string
		order       apiclient.Order
		orderStatus int
		orderID     string
		kitchen     func() hardware.Printer
	}{
		{
			name:        "order lookup fails",
			order:       sampleOrder(&checkID),
			orderStatus: http.StatusNotFound,
			orderID:     orderUUID,
			kitchen:     func() hardware.Printer { return hardware.NewMockPrinter() },
		},
		{
			name:    "order has no items",
			order:   emptyOrder,
			orderID: orderUUID,
			kitchen: func() hardware.Printer { return hardware.NewMockPrinter() },
		},
		{
			name:    "printer not connected",
			order:   sampleOrder(&checkID),
			orderID: orderUUID,
			// Never started: Print returns hardware.ErrPrinterNotConnected.
			kitchen: func() hardware.Printer { return hardware.NewNetworkPrinter("127.0.0.1:0") },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := newKitchenBackend(t, tt.order, check, tt.orderStatus)
			customer := hardware.NewMockPrinter()
			a := kitchenTestApp(t, kb, customer, tt.kitchen())

			if err := a.PrintKitchenTicket(tt.orderID); err == nil {
				t.Fatal("PrintKitchenTicket: want error, got nil")
			}
			if customer.LastJob() != nil {
				t.Fatal("a failed kitchen print must not fall over to the customer printer")
			}
		})
	}
}

func TestPrintKitchenTicket_NilKitchenPrinterIsAnError(t *testing.T) {
	checkID := "check-1"
	order := sampleOrder(&checkID)
	kb := newKitchenBackend(t, order, apiclient.Check{ID: checkID}, 0)
	a := kitchenTestApp(t, kb, hardware.NewMockPrinter(), nil)

	if err := a.PrintKitchenTicket(order.ID); err == nil {
		t.Fatal("want error when no kitchen printer is wired")
	}
}

func TestNewPrinters_Selection(t *testing.T) {
	tests := []struct {
		name          string
		cfg           config.Config
		wantReceipt   string // "mock" | "network"
		wantKitchen   string
		wantSeparated bool
	}{
		{"no addresses: one mock serves both", config.Config{}, "mock", "mock", false},
		{"receipt printer only: kitchen shares it", config.Config{PrinterAddr: "10.0.0.5:9100"}, "network", "network", false},
		{"both set: two devices", config.Config{PrinterAddr: "10.0.0.5:9100", KitchenPrinterAddr: "10.0.0.6:9100"}, "network", "network", true},
		{"kitchen only: mock receipt, real kitchen", config.Config{KitchenPrinterAddr: "10.0.0.6:9100"}, "mock", "network", true},
	}
	kind := func(p hardware.Printer) string {
		switch p.(type) {
		case *hardware.MockPrinter:
			return "mock"
		case *hardware.NetworkPrinter:
			return "network"
		}
		return "unknown"
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, k := newPrinters(tt.cfg)
			if kind(r) != tt.wantReceipt || kind(k) != tt.wantKitchen {
				t.Fatalf("receipt=%s kitchen=%s, want %s/%s", kind(r), kind(k), tt.wantReceipt, tt.wantKitchen)
			}
			if (r != k) != tt.wantSeparated {
				t.Fatalf("separated = %v, want %v", r != k, tt.wantSeparated)
			}
		})
	}
}

func TestPrinterStatus_ReportsKitchenPrinter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	receiptP := hardware.NewMockPrinter()
	kitchenP := hardware.NewMockPrinter()
	receiptP.Start(ctx)
	// kitchenP deliberately never started: reads as disconnected.
	defer func() { cancel(); receiptP.Wait() }()
	go func() {
		for range receiptP.Events() {
		}
	}()

	separate := &App{printer: receiptP, kitchenPrinter: kitchenP}
	got := separate.PrinterStatus()
	if got.Status != "connected" || got.KitchenStatus != "disconnected" || !got.KitchenSeparate {
		t.Fatalf("separate kitchen: %+v", got)
	}

	shared := &App{printer: receiptP, kitchenPrinter: receiptP}
	got = shared.PrinterStatus()
	if got.KitchenStatus != "connected" || got.KitchenSeparate {
		t.Fatalf("shared kitchen: %+v", got)
	}
}

func TestPrintKitchenTicket_RecordsOrderAsPrintedOnlyOnSuccess(t *testing.T) {
	checkID := "check-1"
	order := sampleOrder(&checkID)
	kb := newKitchenBackend(t, order, apiclient.Check{ID: checkID, TableLabel: "Masa 1"}, 0)

	set := printedids.Open(t.TempDir(), 10, nil)

	failing := kitchenTestApp(t, kb, hardware.NewMockPrinter(), hardware.NewNetworkPrinter("127.0.0.1:0"))
	failing.printedOrders = set
	if err := failing.PrintKitchenTicket(order.ID); err == nil {
		t.Fatal("want print error")
	}
	if set.Has(order.ID) {
		t.Fatal("a failed print must not be recorded — the dispatcher would never retry it")
	}

	ok := kitchenTestApp(t, kb, hardware.NewMockPrinter(), hardware.NewMockPrinter())
	ok.printedOrders = set
	if err := ok.PrintKitchenTicket(order.ID); err != nil {
		t.Fatalf("PrintKitchenTicket: %v", err)
	}
	if !set.Has(order.ID) {
		t.Fatal("a successful print must be recorded so the dispatcher skips it")
	}
}

func TestPrintKitchenTicket_MemoryWriteFailureIsNotAPrintFailure(t *testing.T) {
	checkID := "check-1"
	order := sampleOrder(&checkID)
	kb := newKitchenBackend(t, order, apiclient.Check{ID: checkID, TableLabel: "Masa 1"}, 0)

	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var warned []string
	kitchen := hardware.NewMockPrinter()
	a := kitchenTestApp(t, kb, hardware.NewMockPrinter(), kitchen)
	a.printedOrders = printedids.Open(filepath.Join(blocker, "sub"), 10, nil)
	a.logWarn = func(msg string) { warned = append(warned, msg) }

	if err := a.PrintKitchenTicket(order.ID); err != nil {
		t.Fatalf("the ticket is on paper, so this must not be an error: %v", err)
	}
	if kitchen.LastJob() == nil {
		t.Fatal("ticket not printed")
	}
	if len(warned) != 1 {
		t.Fatalf("warnings = %v, want exactly one about the unpersisted memory", warned)
	}
	if !a.printedOrders.Has(order.ID) {
		t.Fatal("in-memory dedupe must still work")
	}
}
