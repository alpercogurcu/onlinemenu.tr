package service_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"onlinemenu.tr/internal/modules/inventory/domain"
	"onlinemenu.tr/internal/modules/inventory/service"
)

func TestValidateSupplyPolicy(t *testing.T) {
	itemID := uuid.New()

	cases := []struct {
		name    string
		req     service.CreateSupplyPolicyRequest
		wantErr bool
	}{
		{
			name: "valid stock_item scope",
			req: service.CreateSupplyPolicyRequest{
				Scope: domain.SupplyScopeStockItem, StockItemID: &itemID, Mode: domain.SupplyModeExclusiveHQ,
			},
		},
		{
			name: "valid category scope",
			req: service.CreateSupplyPolicyRequest{
				Scope: domain.SupplyScopeCategory, Category: "meat", Mode: domain.SupplyModeFree,
			},
		},
		{
			name: "valid tenant_default scope",
			req:  service.CreateSupplyPolicyRequest{Scope: domain.SupplyScopeTenantDefault, Mode: domain.SupplyModeExclusiveHQ},
		},
		{
			name:    "invalid scope",
			req:     service.CreateSupplyPolicyRequest{Scope: "bogus", Mode: domain.SupplyModeFree},
			wantErr: true,
		},
		{
			name:    "invalid mode",
			req:     service.CreateSupplyPolicyRequest{Scope: domain.SupplyScopeTenantDefault, Mode: "bogus"},
			wantErr: true,
		},
		{
			name:    "stock_item scope requires stock_item_id",
			req:     service.CreateSupplyPolicyRequest{Scope: domain.SupplyScopeStockItem, Mode: domain.SupplyModeFree},
			wantErr: true,
		},
		{
			name: "stock_item scope forbids category",
			req: service.CreateSupplyPolicyRequest{
				Scope: domain.SupplyScopeStockItem, StockItemID: &itemID, Category: "meat", Mode: domain.SupplyModeFree,
			},
			wantErr: true,
		},
		{
			name:    "category scope requires category",
			req:     service.CreateSupplyPolicyRequest{Scope: domain.SupplyScopeCategory, Mode: domain.SupplyModeFree},
			wantErr: true,
		},
		{
			name: "category scope forbids stock_item_id",
			req: service.CreateSupplyPolicyRequest{
				Scope: domain.SupplyScopeCategory, Category: "meat", StockItemID: &itemID, Mode: domain.SupplyModeFree,
			},
			wantErr: true,
		},
		{
			name: "tenant_default scope forbids stock_item_id",
			req: service.CreateSupplyPolicyRequest{
				Scope: domain.SupplyScopeTenantDefault, StockItemID: &itemID, Mode: domain.SupplyModeFree,
			},
			wantErr: true,
		},
		{
			name: "tenant_default scope forbids category",
			req: service.CreateSupplyPolicyRequest{
				Scope: domain.SupplyScopeTenantDefault, Category: "meat", Mode: domain.SupplyModeFree,
			},
			wantErr: true,
		},
		{
			name: "approved_suppliers mode requires approved_supplier_ids",
			req: service.CreateSupplyPolicyRequest{
				Scope: domain.SupplyScopeTenantDefault, Mode: domain.SupplyModeApprovedSuppliers,
			},
			wantErr: true,
		},
		{
			name: "approved_suppliers mode with ids is valid",
			req: service.CreateSupplyPolicyRequest{
				Scope: domain.SupplyScopeTenantDefault, Mode: domain.SupplyModeApprovedSuppliers,
				ApprovedSupplierIDs: []uuid.UUID{uuid.New()},
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := service.ValidateSupplyPolicyForTest(tt.req)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
