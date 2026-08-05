package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	catalogpub "onlinemenu.tr/internal/modules/catalog/public"
)

// forbiddenMenuSubstrings are the field names an anonymous menu response must
// never contain (ADR-ARCH-006 §6 / docs/lessons-from-b2b.md). The assertion is
// on the RAW body, not on a decoded struct: a decode-based check would only
// see the fields the test itself knows to look for, whereas a widened
// projection shows up here immediately.
var forbiddenMenuSubstrings = []string{
	"cost_price",
	"cost",
	"stock",
	"supplier",
	"internal_note",
	"auto_close_on_zero_stock",
	"sku",
	"barcode",
	"tax_rate",
}

func TestGetMenu_ResponseCarriesNoInternalFields(t *testing.T) {
	deps := &testDeps{menuReader: &stubMenuReader{categories: sampleMenu()}}
	router := newPublicRouter(t, deps)

	req := httptest.NewRequest(http.MethodGet, "/api/public/v1/menu", nil)
	req.AddCookie(guestCookie(t, deps.signer))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := strings.ToLower(rec.Body.String())
	require.NotEmpty(t, body)

	for _, forbidden := range forbiddenMenuSubstrings {
		assert.NotContainsf(t, body, forbidden,
			"the guest menu response must not expose %q", forbidden)
	}
}

// TestGetMenu_ResponseShape pins the contract the menu app is built against.
func TestGetMenu_ResponseShape(t *testing.T) {
	deps := &testDeps{menuReader: &stubMenuReader{categories: sampleMenu()}}
	router := newPublicRouter(t, deps)

	req := httptest.NewRequest(http.MethodGet, "/api/public/v1/menu", nil)
	req.AddCookie(guestCookie(t, deps.signer))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Categories []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			SortOrder int    `json:"sort_order"`
			Products  []struct {
				ID             string   `json:"id"`
				Name           string   `json:"name"`
				Description    string   `json:"description"`
				PriceAmount    int64    `json:"price_amount"`
				Currency       string   `json:"currency"`
				ImageKey       string   `json:"image_key"`
				Allergens      []string `json:"allergens"`
				IsAvailable    bool     `json:"is_available"`
				ModifierGroups []struct {
					ID        string `json:"id"`
					Name      string `json:"name"`
					MinSelect int    `json:"min_select"`
					MaxSelect int    `json:"max_select"`
					Modifiers []struct {
						ID         string `json:"id"`
						Name       string `json:"name"`
						PriceDelta int64  `json:"price_delta"`
					} `json:"modifiers"`
				} `json:"modifier_groups"`
			} `json:"products"`
		} `json:"categories"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	require.Len(t, resp.Categories, 1)
	require.Len(t, resp.Categories[0].Products, 1)
	product := resp.Categories[0].Products[0]
	assert.Equal(t, int64(4500), product.PriceAmount)
	assert.Equal(t, "products/latte.jpg", product.ImageKey)
	// Empty, never null: the menu app iterates it unconditionally.
	assert.NotNil(t, product.Allergens)
	require.Len(t, product.ModifierGroups, 1)
	require.Len(t, product.ModifierGroups[0].Modifiers, 1)
	assert.Equal(t, int64(500), product.ModifierGroups[0].Modifiers[0].PriceDelta)
}

// TestGetMenu_EmptyMenuSerializesAsEmptyArray keeps the client free of a null
// check: an empty menu is [], not null.
func TestGetMenu_EmptyMenuSerializesAsEmptyArray(t *testing.T) {
	deps := &testDeps{menuReader: &stubMenuReader{}}
	router := newPublicRouter(t, deps)

	req := httptest.NewRequest(http.MethodGet, "/api/public/v1/menu", nil)
	req.AddCookie(guestCookie(t, deps.signer))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"categories":[]}`, rec.Body.String())
}

func sampleMenu() []catalogpub.StorefrontCategory {
	return []catalogpub.StorefrontCategory{{
		ID:        uuid.New(),
		Name:      "Sıcak İçecekler",
		SortOrder: 1,
		Products: []catalogpub.StorefrontProduct{{
			ID:          uuid.New(),
			Name:        "Latte",
			Description: "Espresso ve süt",
			PriceAmount: 4500,
			Currency:    "TRY",
			ImageKey:    "products/latte.jpg",
			Allergens:   []string{},
			IsAvailable: true,
			ModifierGroups: []catalogpub.StorefrontModifierGroup{{
				ID:            uuid.New(),
				Name:          "Ekstralar",
				SelectionType: "multiple",
				MinSelect:     0,
				MaxSelect:     0,
				Modifiers: []catalogpub.StorefrontModifier{{
					ID:         uuid.New(),
					Name:       "Ekstra shot",
					PriceDelta: 500,
				}},
			}},
		}},
	}}
}
