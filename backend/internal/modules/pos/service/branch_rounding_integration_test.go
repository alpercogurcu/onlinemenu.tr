package service_test

// Cash-rounding policy on pos_branch_settings (beşli yuvarlama,
// kasa-rapor-programi G.2) and its cross-module read.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/modules/pos/service"
)

func newCheckReadService() *service.CheckReadService {
	return service.NewCheckReadService(service.CheckReadParams{
		DB:        sharedPool,
		CheckRepo: repo.NewCheckRepo(),
		Settings:  repo.NewBranchSettingsRepo(),
	})
}

func ptr[T any](v T) *T { return &v }

// TestBranchRounding_DefaultsAreOff: a branch never configured reads rounding
// off — through the admin GET and through payment's policy read alike.
func TestBranchRounding_DefaultsAreOff(t *testing.T) {
	ctx := context.Background()
	branch := uuid.New()

	s, err := newBranchSettingsService().Get(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), branch)
	require.NoError(t, err)
	assert.Equal(t, domain.DefaultRoundingPolicy(), s.Rounding)

	policy, err := newCheckReadService().BranchRoundingPolicy(ctx, tenantA, branch)
	require.NoError(t, err)
	assert.Equal(t, pub.RoundingPolicy{StepMinor: 500, MaxPerCheckMinor: 1000}, policy)
	assert.Equal(t, 0, branchSettingsRowCount(t, branch), "reads must not create a row")
}

// TestBranchRounding_PartialUpsert: each rounding field is written on its
// own and the others — rounding and pre-existing preferences — are kept.
func TestBranchRounding_PartialUpsert(t *testing.T) {
	ctx := context.Background()
	branch := uuid.New()
	svc := newBranchSettingsService()

	simple := domain.OrderFlowSimple
	_, err := svc.Set(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(),
		service.SetBranchSettingsRequest{BranchID: branch, OrderFlow: &simple, UpdatedBy: staffA})
	require.NoError(t, err)

	s, err := svc.Set(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(),
		service.SetBranchSettingsRequest{BranchID: branch, RoundingCashEnabled: ptr(true), RoundingStepMinor: ptr(int64(100)), UpdatedBy: staffA})
	require.NoError(t, err)
	assert.Equal(t, domain.OrderFlowSimple, s.OrderFlow, "a rounding PUT must keep the order flow")
	assert.Equal(t, domain.RoundingPolicy{CashEnabled: true, StepMinor: 100, MaxPerCheckMinor: 1000}, s.Rounding)

	s, err = svc.Set(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(),
		service.SetBranchSettingsRequest{BranchID: branch, RoundingCardEnabled: ptr(true), RoundingMaxPerCheck: ptr(int64(0)), UpdatedBy: staffA})
	require.NoError(t, err)
	assert.Equal(t, domain.RoundingPolicy{CashEnabled: true, CardEnabled: true, StepMinor: 100, MaxPerCheckMinor: 0}, s.Rounding)

	policy, err := newCheckReadService().BranchRoundingPolicy(ctx, tenantA, branch)
	require.NoError(t, err)
	assert.Equal(t, pub.RoundingPolicy{CashEnabled: true, CardEnabled: true, StepMinor: 100, MaxPerCheckMinor: 0}, policy)
}

func TestBranchRounding_InvalidValues(t *testing.T) {
	ctx := context.Background()
	branch := uuid.New()
	svc := newBranchSettingsService()

	tests := []struct {
		name string
		req  service.SetBranchSettingsRequest
	}{
		{name: "step outside the allowed set", req: service.SetBranchSettingsRequest{RoundingStepMinor: ptr(int64(200))}},
		{name: "zero step", req: service.SetBranchSettingsRequest{RoundingStepMinor: ptr(int64(0))}},
		{name: "negative ceiling", req: service.SetBranchSettingsRequest{RoundingMaxPerCheck: ptr(int64(-1))}},
		{name: "ceiling above ₺100", req: service.SetBranchSettingsRequest{RoundingMaxPerCheck: ptr(int64(10001))}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.req.BranchID = branch
			tt.req.UpdatedBy = staffA
			_, err := svc.Set(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), tt.req)
			assert.ErrorIs(t, err, service.ErrInvalidRounding)
		})
	}
	assert.Equal(t, 0, branchSettingsRowCount(t, branch), "a rejected PUT must not write a row")
}
