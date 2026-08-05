package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/storefront/domain"
	pub "onlinemenu.tr/internal/modules/storefront/public"
	"onlinemenu.tr/internal/modules/storefront/repo"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// SessionService exchanges a raw QR token for a guest session token.
type SessionService struct {
	db     *db.Pool
	qrRepo *repo.QRCodeRepo
	signer *auth.GuestTokenSigner
	logger *zap.Logger
}

// SessionParams groups fx-injected dependencies.
type SessionParams struct {
	fx.In

	DB     *db.Pool
	QRRepo *repo.QRCodeRepo
	Signer *auth.GuestTokenSigner
	Logger *zap.Logger
}

func NewSessionService(p SessionParams) *SessionService {
	return &SessionService{db: p.DB, qrRepo: p.QRRepo, signer: p.Signer, logger: p.Logger}
}

// ResolvedSession is what a successful QR scan yields.
type ResolvedSession struct {
	Token      string
	TenantID   uuid.UUID
	BranchID   uuid.UUID
	TableID    uuid.UUID
	QRCodeID   uuid.UUID
	SessionID  uuid.UUID
	TableLabel string
}

// ResolveToken exchanges a raw QR token for a signed guest session token.
//
// Order of operations matters:
//  1. The raw token is hashed immediately; the raw value never reaches the
//     database, a log line or a GUC.
//  2. The hash is resolved through db.WithQRTokenLookupTx — the only
//     pre-tenant bootstrap path, read-only, never setting app.tenant_id
//     (ADR-ARCH-006 §4). Its RLS branch can unlock at most the single row
//     whose token_hash the caller already holds.
//  3. Revocation is decided HERE, not in the policy: the policy deliberately
//     ignores status so a revoked scan stays observable (and loggable) rather
//     than being indistinguishable from a nonexistent token at the DB level.
//     Both outcomes still surface as 404 to the caller — see pub.ErrQRRevoked.
//
// Deliberately NOT done yet (WP2): verifying via pos/public that the code's
// table still exists and still belongs to BranchID. Until that lands, a QR
// code whose table was moved to another branch or deleted will still mint a
// session, and the mismatch is only caught at order placement.
func (s *SessionService) ResolveToken(ctx context.Context, rawToken string) (ResolvedSession, error) {
	if rawToken == "" {
		return ResolvedSession{}, pub.ErrQRNotFound
	}
	tokenHash := HashQRToken(rawToken)

	var code domain.QRCode
	err := s.db.WithQRTokenLookupTx(ctx, tokenHash, func(tx pgx.Tx) error {
		var err error
		code, err = s.qrRepo.LookupByTokenHash(ctx, tx, tokenHash)
		return err
	})
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) || errors.Is(err, db.ErrInvalidTokenHash) {
			return ResolvedSession{}, pub.ErrQRNotFound
		}
		return ResolvedSession{}, fmt.Errorf("storefront/service/session: resolve token: %w", err)
	}

	if !code.IsActive() {
		// Logged rather than silently 404'd: a scan of a retired sticker is
		// a real-world signal (someone is using an old table card, or a
		// leaked token is being probed) that the 404 alone would erase.
		s.logger.Info("storefront: revoked qr code scanned",
			zap.String("qr_code_id", code.ID.String()),
			zap.String("tenant_id", code.TenantID.String()),
			zap.String("branch_id", code.BranchID.String()),
		)
		return ResolvedSession{}, pub.ErrQRRevoked
	}

	sessionID := uuid.New()
	token, err := s.signer.IssueGuest(code.TenantID, code.BranchID, code.TableID, code.ID, sessionID)
	if err != nil {
		return ResolvedSession{}, fmt.Errorf("storefront/service/session: issue guest token: %w", err)
	}

	return ResolvedSession{
		Token:      token,
		TenantID:   code.TenantID,
		BranchID:   code.BranchID,
		TableID:    code.TableID,
		QRCodeID:   code.ID,
		SessionID:  sessionID,
		TableLabel: code.TableLabel,
	}, nil
}
