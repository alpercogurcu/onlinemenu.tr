package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"onlinemenu.tr/internal/modules/pos/domain"
)

// TestValidSeatNo pins the seat range contract (pos/000011): 0 ("no seat
// assigned") through domain.MaxSeatNo inclusive, nothing else. The bounds
// must match the order_items_seat_no_chk DB constraint — a Go-side range
// wider than the constraint would turn a client typo into a 500 instead of
// the 422 OrderService.Place owes it.
func TestValidSeatNo(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want bool
	}{
		{"zero means unassigned and is valid", 0, true},
		{"first real seat", 1, true},
		{"upper bound inclusive", domain.MaxSeatNo, true},
		{"negative", -1, false},
		{"just above the bound", domain.MaxSeatNo + 1, false},
		{"far above the bound", 1000, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, domain.ValidSeatNo(tt.n))
		})
	}
}

// TestMaxItemSeatNo pins the pax derivation input: the highest seat across
// the order's lines, with 0 (not an error) when no line carries a seat —
// that is what tells OrderService.Place "this order says nothing about how
// many people sit here, leave pax alone".
func TestMaxItemSeatNo(t *testing.T) {
	item := func(seat int) domain.OrderItem { return domain.OrderItem{SeatNo: seat} }

	tests := []struct {
		name  string
		items []domain.OrderItem
		want  int
	}{
		{"no items", nil, 0},
		{"all unassigned", []domain.OrderItem{item(0), item(0)}, 0},
		{"single seat", []domain.OrderItem{item(3)}, 3},
		{"max wins regardless of position", []domain.OrderItem{item(2), item(5), item(1)}, 5},
		{"unassigned lines do not mask assigned ones", []domain.OrderItem{item(0), item(4), item(0)}, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, domain.MaxItemSeatNo(tt.items))
		})
	}
}
