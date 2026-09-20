package service_test

// POST /api/v1/pos/tables/{id}/status authorization matrix (pilot finding:
// closing a check parks its table in "cleaning", and only pos.table.manage
// holders could flip it back, so cashiers and waiters were stuck). The route
// now needs the wide pos.table.clean grant, and anything other than
// cleaning -> empty still needs pos.table.manage. The whole chain runs for
// real: chi route, embedded OPA, TableService against Postgres.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/pos/domain"
	poshttp "onlinemenu.tr/internal/modules/pos/http"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/platform/auth"
)

var (
	shiftManagerRoleID = uuid.MustParse("00000001-0000-0000-0000-000000000002")
	kitchenRoleID      = uuid.MustParse("00000001-0000-0000-0000-000000000004")
	waiterRoleID       = uuid.MustParse("00000001-0000-0000-0000-000000000008")
)

func tableStatusMux(t *testing.T) *chi.Mux {
	t.Helper()
	engine, err := auth.NewEngine(
		auth.EngineConfig{BundlePath: "../../../../configs/opa/bundles"},
		redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 1}),
		zap.NewNop(),
	)
	require.NoError(t, err)

	hwc := poshttp.NewHandler(poshttp.Params{
		Tables: newTableService(),
		Logger: zap.NewNop(),
		Engine: engine,
		Cache:  redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 1}),
	})
	mux := chi.NewMux()
	hwc.RegisterRoutes(mux)
	return mux
}

func staffPrincipal(roleID uuid.UUID) auth.Principal {
	return auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantA,
		BranchID: branchA,
		RoleIDs:  []uuid.UUID{roleID},
	}
}

func postTableStatus(t *testing.T, ctx context.Context, mux *chi.Mux, principal auth.Principal, tableID uuid.UUID, status string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"status": status})
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(auth.WithPrincipal(ctx, principal), http.MethodPost, "/api/v1/pos/tables/"+tableID.String()+"/status", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// newTableInStatus creates an "empty" table and drives it straight to want
// through the repo, the way CheckService.Open / Close would have.
func newTableInStatus(t *testing.T, ctx context.Context, want domain.TableStatus) domain.Table {
	t.Helper()
	tbl := newOpenTestTable(t, ctx, branchA)
	if want == domain.TableStatusEmpty {
		return tbl
	}
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		if want == domain.TableStatusCleaning {
			if _, err := repo.NewTableRepo().UpdateStatus(ctx, tx, tbl.ID, domain.TableStatusOccupied, domain.TableStatusEmpty); err != nil {
				return err
			}
			_, err := repo.NewTableRepo().UpdateStatus(ctx, tx, tbl.ID, domain.TableStatusCleaning, domain.TableStatusOccupied)
			return err
		}
		_, err := repo.NewTableRepo().UpdateStatus(ctx, tx, tbl.ID, want, domain.TableStatusEmpty)
		return err
	})
	require.NoError(t, err)
	tbl.Status = want
	return tbl
}

func TestTableStatusHTTP_CleaningToEmpty_CounterRolesAllowed(t *testing.T) {
	ctx := context.Background()
	mux := tableStatusMux(t)

	for _, tc := range []struct {
		name string
		role uuid.UUID
	}{
		{"cashier", cashierRoleID},
		{"waiter", waiterRoleID},
		{"shift_manager", shiftManagerRoleID},
		{"manager", managerRoleID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tbl := newTableInStatus(t, ctx, domain.TableStatusCleaning)

			rec := postTableStatus(t, ctx, mux, staffPrincipal(tc.role), tbl.ID, "empty")

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var got struct {
				Status string `json:"status"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			assert.Equal(t, "empty", got.Status)
		})
	}
}

// The clean grant covers exactly one edge. A cashier who could also flip an
// occupied table would be able to free a table that still has an open check.
func TestTableStatusHTTP_OtherTransitions_NeedManage(t *testing.T) {
	ctx := context.Background()
	mux := tableStatusMux(t)

	t.Run("cashier occupied to empty is forbidden", func(t *testing.T) {
		tbl := newTableInStatus(t, ctx, domain.TableStatusOccupied)
		rec := postTableStatus(t, ctx, mux, staffPrincipal(cashierRoleID), tbl.ID, "empty")
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})

	t.Run("waiter empty to reserved is forbidden", func(t *testing.T) {
		tbl := newTableInStatus(t, ctx, domain.TableStatusEmpty)
		rec := postTableStatus(t, ctx, mux, staffPrincipal(waiterRoleID), tbl.ID, "reserved")
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})

	t.Run("cashier cleaning to reserved is forbidden, not a transition error", func(t *testing.T) {
		tbl := newTableInStatus(t, ctx, domain.TableStatusCleaning)
		rec := postTableStatus(t, ctx, mux, staffPrincipal(cashierRoleID), tbl.ID, "reserved")
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})

	t.Run("shift_manager empty to reserved still works", func(t *testing.T) {
		tbl := newTableInStatus(t, ctx, domain.TableStatusEmpty)
		rec := postTableStatus(t, ctx, mux, staffPrincipal(shiftManagerRoleID), tbl.ID, "reserved")
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})
}

func TestTableStatusHTTP_KitchenHasNoCleanGrant(t *testing.T) {
	ctx := context.Background()
	mux := tableStatusMux(t)
	tbl := newTableInStatus(t, ctx, domain.TableStatusCleaning)

	rec := postTableStatus(t, ctx, mux, staffPrincipal(kitchenRoleID), tbl.ID, "empty")

	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
}
