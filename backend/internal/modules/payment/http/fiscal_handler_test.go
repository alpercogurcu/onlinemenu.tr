package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/payment/domain"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/platform/auth"
)

// ─── Test doubles ───────────────────────────────────────────────────────────

// fakeStore implements fiscalAdminStore. Unset hooks panic rather than return
// zero values, so a test that reaches an unexpected call fails loudly instead
// of asserting against a silently empty result.
type fakeStore struct {
	upsertTerminal   func(context.Context, repo.FiscalTerminal) (repo.FiscalTerminal, error)
	listTerminals    func(context.Context, uuid.UUID, uuid.UUID) ([]repo.FiscalTerminal, error)
	getTerminal      func(context.Context, uuid.UUID, uuid.UUID) (repo.FiscalTerminal, error)
	updateTerminal   func(context.Context, uuid.UUID, uuid.UUID, repo.TerminalPatch) (repo.FiscalTerminal, error)
	replaceSections  func(context.Context, uuid.UUID, uuid.UUID, []domain.DeviceSection) error
	listSections     func(context.Context, uuid.UUID, uuid.UUID) ([]repo.FiscalDeviceSection, error)
	listMappings     func(context.Context, uuid.UUID, uuid.UUID) ([]repo.FiscalSectionMapping, error)
	replaceMappings  func(context.Context, uuid.UUID, uuid.UUID, []repo.FiscalSectionMapping) error
	replaceSectionsN int
	replaceMappingsN int
}

func (f *fakeStore) UpsertTerminal(ctx context.Context, t repo.FiscalTerminal) (repo.FiscalTerminal, error) {
	return f.upsertTerminal(ctx, t)
}

func (f *fakeStore) ListTerminals(ctx context.Context, tenantID, branchID uuid.UUID) ([]repo.FiscalTerminal, error) {
	return f.listTerminals(ctx, tenantID, branchID)
}

func (f *fakeStore) GetTerminal(ctx context.Context, tenantID, id uuid.UUID) (repo.FiscalTerminal, error) {
	return f.getTerminal(ctx, tenantID, id)
}

func (f *fakeStore) UpdateTerminal(ctx context.Context, tenantID, id uuid.UUID, patch repo.TerminalPatch) (repo.FiscalTerminal, error) {
	return f.updateTerminal(ctx, tenantID, id, patch)
}

func (f *fakeStore) ReplaceSections(ctx context.Context, tenantID, terminalID uuid.UUID, s []domain.DeviceSection) error {
	f.replaceSectionsN++
	return f.replaceSections(ctx, tenantID, terminalID, s)
}

func (f *fakeStore) ListSections(ctx context.Context, tenantID, terminalID uuid.UUID) ([]repo.FiscalDeviceSection, error) {
	return f.listSections(ctx, tenantID, terminalID)
}

func (f *fakeStore) ListSectionMappings(ctx context.Context, tenantID, branchID uuid.UUID) ([]repo.FiscalSectionMapping, error) {
	return f.listMappings(ctx, tenantID, branchID)
}

func (f *fakeStore) ReplaceSectionMappings(ctx context.Context, tenantID, branchID uuid.UUID, m []repo.FiscalSectionMapping) error {
	f.replaceMappingsN++
	return f.replaceMappings(ctx, tenantID, branchID, m)
}

// nonSyncingAdapter is a FiscalDeviceAdapter that does NOT implement
// domain.SectionSyncer — the exact shape a wire/DLL driver would take. Without
// it the 501 branch of syncSections would be unreachable in tests, since both
// shipping adapters (mock, tokenx) implement the capability.
type nonSyncingAdapter struct{}

func (nonSyncingAdapter) SubmitSale(context.Context, domain.FiscalSale) (*domain.FiscalResult, error) {
	return nil, nil
}

func (nonSyncingAdapter) VoidSale(context.Context, domain.FiscalSubmissionRef) (*domain.FiscalResult, error) {
	return nil, nil
}

func (nonSyncingAdapter) Capabilities() domain.FiscalCapabilities { return domain.FiscalCapabilities{} }

// failingSyncer implements SectionSyncer but the device is unreachable.
type failingSyncer struct {
	nonSyncingAdapter
	sections []domain.DeviceSection
	err      error
}

func (f failingSyncer) FetchSections(context.Context, string) ([]domain.DeviceSection, error) {
	return f.sections, f.err
}

// ─── Fixtures ───────────────────────────────────────────────────────────────

var (
	testTenantID = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	testBranchID = uuid.MustParse("22222222-2222-2222-2222-222222222222")
)

func newFiscalHandler(store fiscalAdminStore, adapter domain.FiscalDeviceAdapter) *FiscalHandler {
	return &FiscalHandler{store: store, adapter: adapter, logger: zap.NewNop()}
}

// newRequest builds a request carrying an authenticated principal and, when
// pathID is non-empty, a chi route context supplying {id}. Routes are exercised
// through the handler methods directly: RegisterRoutes' auth middleware is
// covered by TestRegisterRoutes_AllRoutesRequirePermission.
func newRequest(t *testing.T, method, target, body, pathID string) *http.Request {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	ctx := auth.WithPrincipal(r.Context(), auth.Principal{
		Ctx:      auth.ContextStaff,
		TenantID: testTenantID,
		BranchID: testBranchID,
		PersonID: uuid.New(),
	})
	if pathID != "" {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", pathID)
		ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	}
	return r.WithContext(ctx)
}

// ─── QR parsing ─────────────────────────────────────────────────────────────

func TestParseQR(t *testing.T) {
	tests := []struct {
		name                                 string
		qr                                   string
		wantMerchant, wantBranch, wantSerial string
		wantErr                              bool
	}{
		{name: "valid", qr: "M123_B456_AV0000000658", wantMerchant: "M123", wantBranch: "B456", wantSerial: "AV0000000658"},
		{name: "surrounding whitespace trimmed", qr: "  M1_B2_S3  ", wantMerchant: "M1", wantBranch: "B2", wantSerial: "S3"},
		{name: "too few parts", qr: "M123_AV0000000658", wantErr: true},
		{name: "too many parts", qr: "M_B_S_extra", wantErr: true},
		{name: "empty middle part", qr: "M123__AV0000000658", wantErr: true},
		{name: "blank middle part", qr: "M123_ _AV0000000658", wantErr: true},
		{name: "empty string", qr: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merchant, branch, serial, err := parseQR(tt.qr)
			if tt.wantErr {
				assert.ErrorIs(t, err, errInvalidQR)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantMerchant, merchant)
			assert.Equal(t, tt.wantBranch, branch)
			assert.Equal(t, tt.wantSerial, serial)
		})
	}
}

// ─── createTerminal ─────────────────────────────────────────────────────────

func TestCreateTerminal_FromQR(t *testing.T) {
	var got repo.FiscalTerminal
	store := &fakeStore{upsertTerminal: func(_ context.Context, tr repo.FiscalTerminal) (repo.FiscalTerminal, error) {
		got = tr
		tr.ID = uuid.New()
		return tr, nil
	}}
	h := newFiscalHandler(store, domain.MockFiscalAdapter{})

	body := `{"qr":"M123_B456_AV0000000658","branch_id":"` + testBranchID.String() + `","label":"Kasa 1","basket_mode":"list"}`
	rec := httptest.NewRecorder()
	h.createTerminal(rec, newRequest(t, http.MethodPost, "/api/v1/fiscal/terminals", body, ""))

	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "M123", got.VendorMerchantRef)
	assert.Equal(t, "B456", got.VendorBranchRef)
	assert.Equal(t, "AV0000000658", got.TerminalSerial)
	assert.Equal(t, "tokenx", got.Vendor)
	assert.Equal(t, "list", got.BasketMode)
	assert.Equal(t, "Kasa 1", got.Label)
	assert.Equal(t, testTenantID, got.TenantID, "tenant must come from the principal, never the body")
	assert.Equal(t, testBranchID, got.BranchID)

	var resp terminalResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "AV0000000658", resp.TerminalSerial)
}

func TestCreateTerminal_ExplicitFields_DefaultsToInstantMode(t *testing.T) {
	var got repo.FiscalTerminal
	store := &fakeStore{upsertTerminal: func(_ context.Context, tr repo.FiscalTerminal) (repo.FiscalTerminal, error) {
		got = tr
		return tr, nil
	}}
	h := newFiscalHandler(store, domain.MockFiscalAdapter{})

	body := `{"terminal_serial":"AV1","vendor_merchant_ref":"M1","vendor_branch_ref":"B1","branch_id":"` + testBranchID.String() + `"}`
	rec := httptest.NewRecorder()
	h.createTerminal(rec, newRequest(t, http.MethodPost, "/api/v1/fiscal/terminals", body, ""))

	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "instant", got.BasketMode)
	assert.Equal(t, "AV1", got.TerminalSerial)
}

// TestCreateTerminal_QROverridesExplicitFields pins the documented precedence:
// the QR is read off the physical device, so it cannot disagree with itself.
func TestCreateTerminal_QROverridesExplicitFields(t *testing.T) {
	var got repo.FiscalTerminal
	store := &fakeStore{upsertTerminal: func(_ context.Context, tr repo.FiscalTerminal) (repo.FiscalTerminal, error) {
		got = tr
		return tr, nil
	}}
	h := newFiscalHandler(store, domain.MockFiscalAdapter{})

	body := `{"qr":"QM_QB_QS","terminal_serial":"IGNORED","vendor_merchant_ref":"IGNORED","branch_id":"` + testBranchID.String() + `"}`
	rec := httptest.NewRecorder()
	h.createTerminal(rec, newRequest(t, http.MethodPost, "/api/v1/fiscal/terminals", body, ""))

	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "QS", got.TerminalSerial)
	assert.Equal(t, "QM", got.VendorMerchantRef)
}

func TestCreateTerminal_ValidationErrors(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantCode int
	}{
		{name: "malformed json", body: `{`, wantCode: http.StatusBadRequest},
		{name: "bad qr", body: `{"qr":"only_two","branch_id":"` + testBranchID.String() + `"}`, wantCode: http.StatusUnprocessableEntity},
		{name: "missing branch_id", body: `{"terminal_serial":"AV1"}`, wantCode: http.StatusUnprocessableEntity},
		{name: "missing serial and qr", body: `{"branch_id":"` + testBranchID.String() + `"}`, wantCode: http.StatusUnprocessableEntity},
		{name: "blank serial", body: `{"terminal_serial":"   ","branch_id":"` + testBranchID.String() + `"}`, wantCode: http.StatusUnprocessableEntity},
		{name: "unknown basket_mode", body: `{"terminal_serial":"AV1","basket_mode":"turbo","branch_id":"` + testBranchID.String() + `"}`, wantCode: http.StatusUnprocessableEntity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{upsertTerminal: func(context.Context, repo.FiscalTerminal) (repo.FiscalTerminal, error) {
				t.Fatal("store must not be reached on a validation failure")
				return repo.FiscalTerminal{}, nil
			}}
			h := newFiscalHandler(store, domain.MockFiscalAdapter{})
			rec := httptest.NewRecorder()
			h.createTerminal(rec, newRequest(t, http.MethodPost, "/api/v1/fiscal/terminals", tt.body, ""))
			assert.Equal(t, tt.wantCode, rec.Code)
		})
	}
}

// TestCreateTerminal_SerialTakenByAnotherTenant guards the cross-tenant claim:
// (vendor, terminal_serial) is globally unique so an inbound webhook resolves
// to one tenant. A second tenant claiming the device must be told, not served.
func TestCreateTerminal_SerialTakenByAnotherTenant(t *testing.T) {
	store := &fakeStore{upsertTerminal: func(context.Context, repo.FiscalTerminal) (repo.FiscalTerminal, error) {
		return repo.FiscalTerminal{}, repo.ErrTerminalSerialTaken
	}}
	h := newFiscalHandler(store, domain.MockFiscalAdapter{})

	body := `{"terminal_serial":"AV1","branch_id":"` + testBranchID.String() + `"}`
	rec := httptest.NewRecorder()
	h.createTerminal(rec, newRequest(t, http.MethodPost, "/api/v1/fiscal/terminals", body, ""))

	assert.Equal(t, http.StatusConflict, rec.Code)
}

// ─── listTerminals ──────────────────────────────────────────────────────────

func TestListTerminals_RequiresBranchID(t *testing.T) {
	h := newFiscalHandler(&fakeStore{}, domain.MockFiscalAdapter{})

	rec := httptest.NewRecorder()
	h.listTerminals(rec, newRequest(t, http.MethodGet, "/api/v1/fiscal/terminals", "", ""))
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	rec = httptest.NewRecorder()
	h.listTerminals(rec, newRequest(t, http.MethodGet, "/api/v1/fiscal/terminals?branch_id=nope", "", ""))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestListTerminals_EmptyIsArrayNotNull pins the wire shape: list endpoints
// return a bare JSON array (matching catalog's admin-facing convention), and an
// empty result must serialize as [] so the admin UI can map over it directly.
func TestListTerminals_EmptyIsArrayNotNull(t *testing.T) {
	store := &fakeStore{listTerminals: func(context.Context, uuid.UUID, uuid.UUID) ([]repo.FiscalTerminal, error) {
		return nil, nil
	}}
	h := newFiscalHandler(store, domain.MockFiscalAdapter{})

	rec := httptest.NewRecorder()
	h.listTerminals(rec, newRequest(t, http.MethodGet, "/api/v1/fiscal/terminals?branch_id="+testBranchID.String(), "", ""))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `[]`, rec.Body.String())
}

// ─── updateTerminal ─────────────────────────────────────────────────────────

func TestUpdateTerminal_PatchPassesOnlyPresentFields(t *testing.T) {
	terminalID := uuid.New()
	var got repo.TerminalPatch
	store := &fakeStore{updateTerminal: func(_ context.Context, _, _ uuid.UUID, p repo.TerminalPatch) (repo.FiscalTerminal, error) {
		got = p
		return repo.FiscalTerminal{ID: terminalID, BasketMode: "list"}, nil
	}}
	h := newFiscalHandler(store, domain.MockFiscalAdapter{})

	rec := httptest.NewRecorder()
	h.updateTerminal(rec, newRequest(t, http.MethodPatch, "/x", `{"basket_mode":"list"}`, terminalID.String()))

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, got.BasketMode)
	assert.Equal(t, "list", *got.BasketMode)
	assert.Nil(t, got.Label, "an absent field must stay nil so the repo leaves it untouched")
	assert.Nil(t, got.IsActive)
}

// TestUpdateTerminal_ClearLabelIsDistinctFromAbsent proves the *string encoding
// carries "set to empty" apart from "don't touch".
func TestUpdateTerminal_ClearLabelIsDistinctFromAbsent(t *testing.T) {
	var got repo.TerminalPatch
	store := &fakeStore{updateTerminal: func(_ context.Context, _, _ uuid.UUID, p repo.TerminalPatch) (repo.FiscalTerminal, error) {
		got = p
		return repo.FiscalTerminal{}, nil
	}}
	h := newFiscalHandler(store, domain.MockFiscalAdapter{})

	rec := httptest.NewRecorder()
	h.updateTerminal(rec, newRequest(t, http.MethodPatch, "/x", `{"label":"","is_active":false}`, uuid.NewString()))

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, got.Label)
	assert.Equal(t, "", *got.Label)
	require.NotNil(t, got.IsActive)
	assert.False(t, *got.IsActive)
}

func TestUpdateTerminal_Errors(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		h := newFiscalHandler(&fakeStore{}, domain.MockFiscalAdapter{})
		rec := httptest.NewRecorder()
		h.updateTerminal(rec, newRequest(t, http.MethodPatch, "/x", `{}`, "not-a-uuid"))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("unknown basket_mode", func(t *testing.T) {
		h := newFiscalHandler(&fakeStore{}, domain.MockFiscalAdapter{})
		rec := httptest.NewRecorder()
		h.updateTerminal(rec, newRequest(t, http.MethodPatch, "/x", `{"basket_mode":"turbo"}`, uuid.NewString()))
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	})

	t.Run("not found", func(t *testing.T) {
		store := &fakeStore{updateTerminal: func(context.Context, uuid.UUID, uuid.UUID, repo.TerminalPatch) (repo.FiscalTerminal, error) {
			return repo.FiscalTerminal{}, repo.ErrTerminalNotFound
		}}
		h := newFiscalHandler(store, domain.MockFiscalAdapter{})
		rec := httptest.NewRecorder()
		h.updateTerminal(rec, newRequest(t, http.MethodPatch, "/x", `{}`, uuid.NewString()))
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

// ─── syncSections ───────────────────────────────────────────────────────────

// TestSyncSections_AdapterWithoutCapability_Returns501 covers the branch that
// neither shipping adapter can reach.
func TestSyncSections_AdapterWithoutCapability_Returns501(t *testing.T) {
	store := &fakeStore{getTerminal: func(context.Context, uuid.UUID, uuid.UUID) (repo.FiscalTerminal, error) {
		t.Fatal("capability must be checked before any DB access")
		return repo.FiscalTerminal{}, nil
	}}
	h := newFiscalHandler(store, nonSyncingAdapter{})

	rec := httptest.NewRecorder()
	h.syncSections(rec, newRequest(t, http.MethodPost, "/x", "", uuid.NewString()))

	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestSyncSections_MockAdapter_ReplacesAndReturnsSections(t *testing.T) {
	terminalID := uuid.New()
	var written []domain.DeviceSection
	store := &fakeStore{
		getTerminal: func(context.Context, uuid.UUID, uuid.UUID) (repo.FiscalTerminal, error) {
			return repo.FiscalTerminal{ID: terminalID, TenantID: testTenantID, TerminalSerial: "AV1"}, nil
		},
		replaceSections: func(_ context.Context, tenantID, tid uuid.UUID, s []domain.DeviceSection) error {
			assert.Equal(t, testTenantID, tenantID)
			assert.Equal(t, terminalID, tid)
			written = s
			return nil
		},
		listSections: func(context.Context, uuid.UUID, uuid.UUID) ([]repo.FiscalDeviceSection, error) {
			return []repo.FiscalDeviceSection{{SectionNo: 2, Name: "KDV %10", TaxPermyriad: 1000}}, nil
		},
	}
	h := newFiscalHandler(store, domain.MockFiscalAdapter{})

	rec := httptest.NewRecorder()
	h.syncSections(rec, newRequest(t, http.MethodPost, "/x", "", terminalID.String()))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, written, 3, "mock adapter reports the three default VAT sections")
	assert.Equal(t, 1, store.replaceSectionsN)

	var resp []sectionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp, 1)
	assert.Equal(t, 1000, resp[0].TaxPermyriad)
}

// TestSyncSections_DevicePreservedOnFailure asserts the last good sync survives
// an unreachable device and an empty section list: erasing the stored sections
// would make every later sale fail its section resolve.
func TestSyncSections_DevicePreservedOnFailure(t *testing.T) {
	tests := []struct {
		name    string
		adapter domain.FiscalDeviceAdapter
	}{
		{name: "device unreachable", adapter: failingSyncer{err: errors.New("dial tcp: timeout")}},
		{name: "device reports no sections", adapter: failingSyncer{sections: []domain.DeviceSection{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{
				getTerminal: func(context.Context, uuid.UUID, uuid.UUID) (repo.FiscalTerminal, error) {
					return repo.FiscalTerminal{ID: uuid.New(), TerminalSerial: "AV1"}, nil
				},
				replaceSections: func(context.Context, uuid.UUID, uuid.UUID, []domain.DeviceSection) error {
					t.Fatal("stored sections must not be replaced when the sync fails")
					return nil
				},
			}
			h := newFiscalHandler(store, tt.adapter)

			rec := httptest.NewRecorder()
			h.syncSections(rec, newRequest(t, http.MethodPost, "/x", "", uuid.NewString()))

			assert.Equal(t, http.StatusBadGateway, rec.Code)
			assert.Zero(t, store.replaceSectionsN)
		})
	}
}

func TestSyncSections_UnknownTerminal_Returns404(t *testing.T) {
	store := &fakeStore{getTerminal: func(context.Context, uuid.UUID, uuid.UUID) (repo.FiscalTerminal, error) {
		return repo.FiscalTerminal{}, repo.ErrTerminalNotFound
	}}
	h := newFiscalHandler(store, domain.MockFiscalAdapter{})

	rec := httptest.NewRecorder()
	h.syncSections(rec, newRequest(t, http.MethodPost, "/x", "", uuid.NewString()))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// ─── listSections ───────────────────────────────────────────────────────────

func TestListSections_UnknownTerminal_Returns404(t *testing.T) {
	store := &fakeStore{getTerminal: func(context.Context, uuid.UUID, uuid.UUID) (repo.FiscalTerminal, error) {
		return repo.FiscalTerminal{}, repo.ErrTerminalNotFound
	}}
	h := newFiscalHandler(store, domain.MockFiscalAdapter{})

	rec := httptest.NewRecorder()
	h.listSections(rec, newRequest(t, http.MethodGet, "/x", "", uuid.NewString()))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestListSections_OK(t *testing.T) {
	store := &fakeStore{
		getTerminal: func(context.Context, uuid.UUID, uuid.UUID) (repo.FiscalTerminal, error) {
			return repo.FiscalTerminal{ID: uuid.New()}, nil
		},
		listSections: func(context.Context, uuid.UUID, uuid.UUID) ([]repo.FiscalDeviceSection, error) {
			return []repo.FiscalDeviceSection{{SectionNo: 1, Name: "KDV %1", TaxPermyriad: 100}}, nil
		},
	}
	h := newFiscalHandler(store, domain.MockFiscalAdapter{})

	rec := httptest.NewRecorder()
	h.listSections(rec, newRequest(t, http.MethodGet, "/x", "", uuid.NewString()))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp []sectionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp, 1)
	assert.Equal(t, 100, resp[0].TaxPermyriad)
	assert.Equal(t, "KDV %1", resp[0].Name)
}

// ─── section mappings ───────────────────────────────────────────────────────

func TestListSectionMappings_RequiresBranchID(t *testing.T) {
	h := newFiscalHandler(&fakeStore{}, domain.MockFiscalAdapter{})
	rec := httptest.NewRecorder()
	h.listSectionMappings(rec, newRequest(t, http.MethodGet, "/api/v1/fiscal/section-mappings", "", ""))
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestListSectionMappings_BareArrayShape(t *testing.T) {
	categoryID := uuid.New()
	store := &fakeStore{listMappings: func(context.Context, uuid.UUID, uuid.UUID) ([]repo.FiscalSectionMapping, error) {
		return []repo.FiscalSectionMapping{{CategoryID: categoryID, SectionNo: 2}}, nil
	}}
	h := newFiscalHandler(store, domain.MockFiscalAdapter{})

	rec := httptest.NewRecorder()
	h.listSectionMappings(rec, newRequest(t, http.MethodGet, "/api/v1/fiscal/section-mappings?branch_id="+testBranchID.String(), "", ""))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `[{"category_id":"`+categoryID.String()+`","section_no":2}]`, rec.Body.String())
}

func TestReplaceSectionMappings_FullReplace(t *testing.T) {
	catA, catB := uuid.New(), uuid.New()
	var got []repo.FiscalSectionMapping
	store := &fakeStore{
		replaceMappings: func(_ context.Context, tenantID, branchID uuid.UUID, m []repo.FiscalSectionMapping) error {
			assert.Equal(t, testTenantID, tenantID)
			assert.Equal(t, testBranchID, branchID)
			got = m
			return nil
		},
		listMappings: func(context.Context, uuid.UUID, uuid.UUID) ([]repo.FiscalSectionMapping, error) {
			return []repo.FiscalSectionMapping{{CategoryID: catA, SectionNo: 1}, {CategoryID: catB, SectionNo: 2}}, nil
		},
	}
	h := newFiscalHandler(store, domain.MockFiscalAdapter{})

	body := `{"branch_id":"` + testBranchID.String() + `","mappings":[{"category_id":"` + catA.String() + `","section_no":1},{"category_id":"` + catB.String() + `","section_no":2}]}`
	rec := httptest.NewRecorder()
	h.replaceSectionMappings(rec, newRequest(t, http.MethodPut, "/api/v1/fiscal/section-mappings", body, ""))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, got, 2)
	assert.Equal(t, catA, got[0].CategoryID)
	assert.Equal(t, 1, store.replaceMappingsN)

	var resp []sectionMappingResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Len(t, resp, 2)
}

// TestReplaceSectionMappings_EmptyClearsBranch documents that an empty array is
// a legitimate "unmap everything", not a no-op.
func TestReplaceSectionMappings_EmptyClearsBranch(t *testing.T) {
	store := &fakeStore{
		replaceMappings: func(_ context.Context, _, _ uuid.UUID, m []repo.FiscalSectionMapping) error {
			assert.Empty(t, m)
			return nil
		},
		listMappings: func(context.Context, uuid.UUID, uuid.UUID) ([]repo.FiscalSectionMapping, error) {
			return nil, nil
		},
	}
	h := newFiscalHandler(store, domain.MockFiscalAdapter{})

	body := `{"branch_id":"` + testBranchID.String() + `","mappings":[]}`
	rec := httptest.NewRecorder()
	h.replaceSectionMappings(rec, newRequest(t, http.MethodPut, "/api/v1/fiscal/section-mappings", body, ""))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, store.replaceMappingsN)
	assert.JSONEq(t, `[]`, rec.Body.String())
}

func TestReplaceSectionMappings_ValidationErrors(t *testing.T) {
	catA := uuid.New()
	tests := []struct {
		name     string
		body     string
		wantCode int
	}{
		{name: "malformed json", body: `{`, wantCode: http.StatusBadRequest},
		{name: "missing branch_id", body: `{"mappings":[]}`, wantCode: http.StatusUnprocessableEntity},
		{
			name:     "nil category_id",
			body:     `{"branch_id":"` + testBranchID.String() + `","mappings":[{"category_id":"00000000-0000-0000-0000-000000000000","section_no":1}]}`,
			wantCode: http.StatusUnprocessableEntity,
		},
		{
			name:     "zero section_no",
			body:     `{"branch_id":"` + testBranchID.String() + `","mappings":[{"category_id":"` + catA.String() + `","section_no":0}]}`,
			wantCode: http.StatusUnprocessableEntity,
		},
		{
			name:     "negative section_no",
			body:     `{"branch_id":"` + testBranchID.String() + `","mappings":[{"category_id":"` + catA.String() + `","section_no":-1}]}`,
			wantCode: http.StatusUnprocessableEntity,
		},
		{
			name:     "duplicate category",
			body:     `{"branch_id":"` + testBranchID.String() + `","mappings":[{"category_id":"` + catA.String() + `","section_no":1},{"category_id":"` + catA.String() + `","section_no":2}]}`,
			wantCode: http.StatusUnprocessableEntity,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{replaceMappings: func(context.Context, uuid.UUID, uuid.UUID, []repo.FiscalSectionMapping) error {
				t.Fatal("store must not be reached on a validation failure")
				return nil
			}}
			h := newFiscalHandler(store, domain.MockFiscalAdapter{})
			rec := httptest.NewRecorder()
			h.replaceSectionMappings(rec, newRequest(t, http.MethodPut, "/api/v1/fiscal/section-mappings", tt.body, ""))
			assert.Equal(t, tt.wantCode, rec.Code)
			assert.Zero(t, store.replaceMappingsN)
		})
	}
}

// ─── wire contract ──────────────────────────────────────────────────────────

// TestTerminalResponse_WireContract pins the exact JSON keys the admin UI is
// coded against (web/apps/admin/src/types/index.ts: FiscalTerminal, and
// hooks/use-fiscal.ts, which types every list endpoint as a BARE ARRAY — not
// an enveloped object like the payments endpoints). Renaming a field or
// wrapping the arrays would break the UI silently at runtime; it breaks here
// instead.
func TestTerminalResponse_WireContract(t *testing.T) {
	id, tenantID, branchID := uuid.New(), uuid.New(), uuid.New()
	created := time.Date(2026, 7, 10, 9, 30, 0, 0, time.UTC)

	raw, err := json.Marshal(toTerminalResponse(repo.FiscalTerminal{
		ID: id, TenantID: tenantID, BranchID: branchID,
		Vendor: "tokenx", TerminalSerial: "AV0000000658",
		VendorMerchantRef: "M1", VendorBranchRef: "B1",
		Label: "Kasa 1", BasketMode: "instant", IsActive: true,
		CreatedAt: created, UpdatedAt: created,
	}))
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	wantKeys := []string{
		"id", "tenant_id", "branch_id", "vendor", "terminal_serial",
		"vendor_merchant_ref", "vendor_branch_ref", "label", "basket_mode",
		"is_active", "created_at", "updated_at",
	}
	for _, k := range wantKeys {
		assert.Contains(t, got, k)
	}
	assert.Len(t, got, len(wantKeys), "an unexpected field leaked into the terminal DTO")
	assert.Equal(t, "2026-07-10T09:30:00Z", got["created_at"], "timestamps are RFC3339")
}

func TestSectionResponse_WireContract(t *testing.T) {
	raw, err := json.Marshal(toSectionResponses([]repo.FiscalDeviceSection{
		{SectionNo: 2, Name: "KDV %10", TaxPermyriad: 1000, SyncedAt: time.Date(2026, 7, 10, 9, 30, 0, 0, time.UTC)},
	}))
	require.NoError(t, err)
	assert.JSONEq(t, `[{"section_no":2,"name":"KDV %10","tax_permyriad":1000,"synced_at":"2026-07-10T09:30:00Z"}]`, string(raw))
}

// ─── unauthenticated ────────────────────────────────────────────────────────

// TestFiscalHandlers_RejectMissingPrincipal complements the OPA smoke test: even
// if a route were mounted without middleware, the handler itself refuses to run
// with no tenant in context (defense in depth against a tenant_id of uuid.Nil
// reaching WithTenantTx).
func TestFiscalHandlers_RejectMissingPrincipal(t *testing.T) {
	h := newFiscalHandler(&fakeStore{}, domain.MockFiscalAdapter{})
	handlers := map[string]http.HandlerFunc{
		"createTerminal":         h.createTerminal,
		"listTerminals":          h.listTerminals,
		"updateTerminal":         h.updateTerminal,
		"syncSections":           h.syncSections,
		"listSections":           h.listSections,
		"listSectionMappings":    h.listSectionMappings,
		"replaceSectionMappings": h.replaceSectionMappings,
	}
	for name, fn := range handlers {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			fn(rec, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{}`)))
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
		})
	}
}
