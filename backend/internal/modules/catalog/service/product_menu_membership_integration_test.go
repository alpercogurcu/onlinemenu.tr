package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/catalog/domain"
	"onlinemenu.tr/internal/modules/catalog/repo"
)

func seedMenu(t *testing.T, tenantID uuid.UUID, name string, active bool) domain.Menu {
	t.Helper()
	var m domain.Menu
	require.NoError(t, sharedPool.WithTenantTx(context.Background(), tenantID, func(tx pgx.Tx) error {
		var err error
		m, err = repo.NewMenuRepo().Create(context.Background(), tx, domain.Menu{
			TenantID: tenantID, Name: name, IsActive: active,
		})
		return err
	}))
	return m
}

func menuProductIDs(t *testing.T, tenantID, menuID uuid.UUID) []uuid.UUID {
	t.Helper()
	var ids []uuid.UUID
	require.NoError(t, sharedPool.WithTenantReadTx(context.Background(), tenantID, func(tx pgx.Tx) error {
		items, err := repo.NewMenuItemRepo().ListByMenu(context.Background(), tx, menuID)
		for _, it := range items {
			ids = append(ids, it.ProductID)
		}
		return err
	}))
	return ids
}

func TestProductService_Create_AutoAddsToSingleActiveMenu(t *testing.T) {
	tenantID := uuid.New()
	main := seedMenu(t, tenantID, "Ana Menü", true)
	seedMenu(t, tenantID, "Eski Menü", false)

	created, err := newProductService().Create(context.Background(), tenantID, domain.Product{
		Name: "Yeni Ürün", PriceAmount: 5000, Currency: "TRY", Unit: "adet", IsActive: true,
	})
	require.NoError(t, err)

	assert.Equal(t, domain.MenuMembershipAuto, created.MenuMembership)
	assert.Contains(t, menuProductIDs(t, tenantID, main.ID), created.ID)
}

func TestProductService_Create_MultipleActiveMenus_NotAdded(t *testing.T) {
	tenantID := uuid.New()
	a := seedMenu(t, tenantID, "Öğle", true)
	b := seedMenu(t, tenantID, "Akşam", true)

	created, err := newProductService().Create(context.Background(), tenantID, domain.Product{
		Name: "Yeni Ürün", PriceAmount: 5000, Currency: "TRY", Unit: "adet", IsActive: true,
	})
	require.NoError(t, err)

	assert.Equal(t, domain.MenuMembershipManual, created.MenuMembership)
	assert.NotContains(t, menuProductIDs(t, tenantID, a.ID), created.ID)
	assert.NotContains(t, menuProductIDs(t, tenantID, b.ID), created.ID)
}

func TestProductService_Create_NoMenu_Manual(t *testing.T) {
	created, err := newProductService().Create(context.Background(), uuid.New(), domain.Product{
		Name: "Yeni Ürün", PriceAmount: 5000, Currency: "TRY", Unit: "adet", IsActive: true,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.MenuMembershipManual, created.MenuMembership)
}
