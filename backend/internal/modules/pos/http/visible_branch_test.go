package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/platform/auth"
)

// TestVisibleBranch pins the 404-not-403 rule for single-row reads
// (docs/lessons-from-b2b.md §2). A 403 on GET /pos/checks/{id} would confirm
// the id exists inside the tenant, which lets a branch B station enumerate how
// busy branch A is without reading a field. The write paths keep their 403 and
// are NOT routed through here — the e2e role matrix pins that distinction, so
// a future "simplification" that folds ErrBranchForbidden into Handler.error
// would break it.
//
// No OPA scope is planted in the request context: platform/auth exposes no
// exported inverse of ScopeFromContext (deliberately — an exported setter
// would let any module forge a tenant exemption). The manager path is covered
// at the router level instead, see branch_read_authz_test.go.
func TestVisibleBranch(t *testing.T) {
	branchA := uuid.MustParse("11111111-0000-0000-0000-0000000000aa")
	branchB := uuid.MustParse("22222222-0000-0000-0000-0000000000bb")

	staffAt := func(branchID uuid.UUID) auth.Principal {
		return auth.Principal{
			PersonID: uuid.New(),
			Ctx:      auth.ContextStaff,
			TenantID: uuid.New(),
			BranchID: branchID,
		}
	}

	tests := []struct {
		name      string
		principal auth.Principal
		rowBranch uuid.UUID
		wantOK    bool
	}{
		{
			name:      "own branch proceeds",
			principal: staffAt(branchB),
			rowBranch: branchB,
			wantOK:    true,
		},
		{
			name:      "another branch is hidden",
			principal: staffAt(branchB),
			rowBranch: branchA,
			wantOK:    false,
		},
		{
			// A branch-scoped principal with no branch fails closed rather
			// than reading as chain-wide (see service.requireBranch).
			name:      "branchless staff principal is hidden",
			principal: staffAt(uuid.Nil),
			rowBranch: branchA,
			wantOK:    false,
		},
		{
			name:      "customer context is hidden",
			principal: auth.Principal{PersonID: uuid.New(), Ctx: auth.ContextCustomer},
			rowBranch: branchA,
			wantOK:    false,
		},
	}

	h := &Handler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/pos/checks/"+uuid.NewString(), nil)

			got := h.visibleBranch(rec, req, tt.principal, tt.rowBranch)

			require.Equal(t, tt.wantOK, got)
			if tt.wantOK {
				assert.Equal(t, http.StatusOK, rec.Code, "nothing may be written when the caller may proceed")
				assert.Empty(t, rec.Body.String())
				return
			}
			assert.Equal(t, http.StatusNotFound, rec.Code, "a cross-branch row must be hidden, not refused")
		})
	}
}
