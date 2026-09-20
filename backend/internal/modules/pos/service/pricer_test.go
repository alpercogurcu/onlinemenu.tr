package service_test

import (
	"context"
	"sync"

	"github.com/google/uuid"

	catalogpub "onlinemenu.tr/internal/modules/catalog/public"
)

// staticPricer stands in for the catalog in the tests of this package whose
// subject is the order machine (branch authz, check guards, lifecycle) rather
// than pricing.
//
// It is NOT a way to skip the price check: it answers the same contract the
// real catalog service does, and a line whose product was never registered is
// rejected exactly as an unsellable product would be. What it avoids is
// seeding catalog rows in tests that have nothing to say about the catalog —
// pos/service may not import catalog/service (go-arch-lint: cross-module
// imports reach public/ only), so the real pricer is exercised in
// catalog/service's own integration tests and in the admin e2e suite.
type staticPricer struct{}

func (staticPricer) PriceStaffCart(_ context.Context, _ uuid.UUID, lines []catalogpub.StaffCartLine) ([]catalogpub.PricedLine, error) {
	out := make([]catalogpub.PricedLine, len(lines))
	for i, l := range lines {
		priced, ok := lookupTestProduct(l.ProductID)
		if !ok {
			return nil, &catalogpub.ValidationError{Msg: "product is not orderable: " + l.ProductID.String()}
		}
		if l.Quantity < 1 {
			return nil, &catalogpub.ValidationError{Msg: "cart line quantity must be positive"}
		}
		priced.Quantity = l.Quantity
		for _, id := range l.ModifierIDs {
			delta, ok := lookupTestModifier(l.ProductID, id)
			if !ok {
				return nil, &catalogpub.ValidationError{Msg: "modifier is not available for this product: " + id.String()}
			}
			priced.UnitPriceAmount += delta
			priced.Modifiers = append(priced.Modifiers, catalogpub.PricedModifier{ID: id, PriceDelta: delta})
		}
		out[i] = priced
	}
	return out, nil
}

var _ catalogpub.StaffPricer = staticPricer{}

// testCatalog is the price table staticPricer answers from. It is package
// state because the pricer is constructed per service instance while the
// products belong to the test run as a whole.
// productModifier keys a modifier on the PAIR, exactly as the real pricer
// does: an option that exists but belongs to another product must not price
// this line.
type productModifier struct{ product, modifier uuid.UUID }

var testCatalog = struct {
	mu        sync.Mutex
	products  map[uuid.UUID]catalogpub.PricedLine
	modifiers map[productModifier]int64
}{
	products:  map[uuid.UUID]catalogpub.PricedLine{},
	modifiers: map[productModifier]int64{},
}

// testProduct registers a sellable product and returns its id, so an order
// fixture reads the same way it did before the price check existed — the id
// is still opaque, it is just no longer unknown to the catalog.
func testProduct(name string, unitPrice int64) uuid.UUID {
	id := uuid.New()
	testCatalog.mu.Lock()
	defer testCatalog.mu.Unlock()
	testCatalog.products[id] = catalogpub.PricedLine{
		ProductID:       id,
		ProductName:     name,
		BasePriceAmount: unitPrice,
		UnitPriceAmount: unitPrice,
		Currency:        "TRY",
		TaxRateBPS:      1000,
	}
	return id
}

func lookupTestProduct(id uuid.UUID) (catalogpub.PricedLine, bool) {
	testCatalog.mu.Lock()
	defer testCatalog.mu.Unlock()
	p, ok := testCatalog.products[id]
	return p, ok
}

// testModifier attaches an option with a price delta (kuruş) to a registered
// product and returns its id.
func testModifier(productID uuid.UUID, delta int64) uuid.UUID {
	id := uuid.New()
	testCatalog.mu.Lock()
	defer testCatalog.mu.Unlock()
	testCatalog.modifiers[productModifier{productID, id}] = delta
	return id
}

func lookupTestModifier(productID, modifierID uuid.UUID) (int64, bool) {
	testCatalog.mu.Lock()
	defer testCatalog.mu.Unlock()
	delta, ok := testCatalog.modifiers[productModifier{productID, modifierID}]
	return delta, ok
}
