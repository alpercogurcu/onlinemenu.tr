package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/identity/domain"
	pub "onlinemenu.tr/internal/modules/identity/public"
	"onlinemenu.tr/internal/modules/identity/repo"
	"onlinemenu.tr/internal/platform/db"
)

// PinService implements pub.CashierPinService (ADR-DATA-008 PIN akışı). It is
// the ONLY code path in this module allowed to write or read cashier_pins.
type PinService struct {
	db     *db.Pool
	pins   *repo.CashierPinRepo
	logger *zap.Logger
}

// PinParams groups fx-injected dependencies.
type PinParams struct {
	fx.In

	DB     *db.Pool
	Pins   *repo.CashierPinRepo
	Logger *zap.Logger
}

// NewPinService constructs a PinService for fx injection.
func NewPinService(p PinParams) *PinService {
	return &PinService{db: p.DB, pins: p.Pins, logger: p.Logger}
}

// SetOwnPin sets/replaces personID's PIN. See pub.CashierPinService for the
// trust precondition the caller must have already established.
func (s *PinService) SetOwnPin(ctx context.Context, tenantID, personID uuid.UUID, pin string) error {
	if err := domain.ValidatePinFormat(pin); err != nil {
		// Translate to the public sentinel at the module boundary: callers
		// outside identity cannot import identity/domain (module isolation),
		// so domain.ErrPinFormatInvalid must never cross this line unwrapped.
		return pub.ErrPinFormatInvalid
	}
	salt, hash, err := domain.HashPin(pin)
	if err != nil {
		return fmt.Errorf("identity/service/pin: set own pin: %w", err)
	}
	err = s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return s.pins.Upsert(ctx, tx, tenantID, personID, salt, hash)
	})
	if err != nil {
		return fmt.Errorf("identity/service/pin: set own pin: %w", err)
	}
	// Info, not Debug: this is a security-relevant event a reviewer of the
	// audit trail needs to see (who set/changed a PIN, when) — but the PIN
	// value and its hash never appear here or anywhere else.
	s.logger.Info("identity/service/pin: pin set",
		zap.String("tenant_id", tenantID.String()), zap.String("person_id", personID.String()))
	return nil
}

// VerifyPin reports whether pin matches personID's stored PIN. It ALWAYS
// performs one argon2id computation, on the real stored hash if one exists
// or on a freshly-random throwaway hash (domain.DummyPinCost) if it does
// not — "no PIN set" and "wrong PIN" must cost the same CPU time and return
// the same error, or the timing difference alone tells a caller which
// person UUIDs are real cashiers with a PIN set. See pub.ErrPinVerificationFailed.
func (s *PinService) VerifyPin(ctx context.Context, tenantID, personID uuid.UUID, pin string) error {
	var salt, hash []byte
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		salt, hash, err = s.pins.GetHash(ctx, tx, tenantID, personID)
		return err
	})

	switch {
	case err == nil:
		if domain.VerifyPinHash(pin, salt, hash) {
			return nil
		}
		return pub.ErrPinVerificationFailed
	case errors.Is(err, pub.ErrNotFound):
		// No PIN row for this (tenant, person) — pay the same argon2 cost a
		// real mismatch would, then return the identical sentinel.
		domain.DummyPinCost(pin)
		return pub.ErrPinVerificationFailed
	default:
		return fmt.Errorf("identity/service/pin: verify pin: %w", err)
	}
}

// HasPin reports whether personID has a PIN set for tenantID. See
// pub.CashierPinService for why this carries no verification semantics —
// it never touches pin_salt/pin_hash, only the row's existence.
func (s *PinService) HasPin(ctx context.Context, tenantID, personID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		exists, err = s.pins.Exists(ctx, tx, tenantID, personID)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("identity/service/pin: has pin: %w", err)
	}
	return exists, nil
}

// ResetPin deletes personID's PIN row (manager action). See
// pub.CashierPinService for why this never reads or accepts a PIN value.
func (s *PinService) ResetPin(ctx context.Context, tenantID, personID uuid.UUID) error {
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return s.pins.Delete(ctx, tx, tenantID, personID)
	})
	if err != nil {
		return fmt.Errorf("identity/service/pin: reset pin: %w", err)
	}
	s.logger.Warn("identity/service/pin: pin reset by manager",
		zap.String("tenant_id", tenantID.String()), zap.String("person_id", personID.String()))
	return nil
}
