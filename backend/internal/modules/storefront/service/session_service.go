package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	pospub "onlinemenu.tr/internal/modules/pos/public"
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
	tables pospub.GuestTableReader
	signer *auth.GuestTokenSigner
	logger *zap.Logger
}

// SessionParams groups fx-injected dependencies.
type SessionParams struct {
	fx.In

	DB     *db.Pool
	QRRepo *repo.QRCodeRepo
	Tables pospub.GuestTableReader
	Signer *auth.GuestTokenSigner
	Logger *zap.Logger
}

func NewSessionService(p SessionParams) *SessionService {
	return &SessionService{
		db:     p.DB,
		qrRepo: p.QRRepo,
		tables: p.Tables,
		signer: p.Signer,
		logger: p.Logger,
	}
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
//  4. The code's table is re-verified through pos/public: it must still
//     exist, still be active, and still belong to the branch the code was
//     issued for. A sticker that outlived its table (deleted, deactivated, or
//     moved to another branch) mints no session — otherwise the diner would
//     browse a menu happily and only hit the wall at checkout.
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

	table, err := s.tables.GetGuestTable(ctx, code.TenantID, code.TableID)
	if err != nil {
		if errors.Is(err, pospub.ErrTableNotFound) {
			s.logger.Info("storefront: qr code points at a missing table",
				zap.String("qr_code_id", code.ID.String()),
				zap.String("table_id", code.TableID.String()),
			)
			return ResolvedSession{}, pub.ErrQRNotFound
		}
		return ResolvedSession{}, fmt.Errorf("storefront/service/session: read table: %w", err)
	}
	if table.BranchID != code.BranchID || !table.IsActive {
		// The reason is logged, never returned: to an anonymous caller a
		// misconfigured sticker and a made-up token must look identical.
		s.logger.Warn("storefront: qr code no longer matches its table",
			zap.String("qr_code_id", code.ID.String()),
			zap.String("qr_branch_id", code.BranchID.String()),
			zap.String("table_branch_id", table.BranchID.String()),
			zap.Bool("table_active", table.IsActive),
		)
		return ResolvedSession{}, pub.ErrQRNotFound
	}

	sessionID := uuid.New()
	token, err := s.signer.IssueGuest(code.TenantID, code.BranchID, code.TableID, code.ID, sessionID)
	if err != nil {
		return ResolvedSession{}, fmt.Errorf("storefront/service/session: issue guest token: %w", err)
	}

	// The live table name wins over storefront_qr_codes.table_label: the
	// latter is a snapshot taken when the sticker was printed and drifts as
	// soon as staff rename the table.
	label := table.Label
	if label == "" {
		label = code.TableLabel
	}

	return ResolvedSession{
		Token:      token,
		TenantID:   code.TenantID,
		BranchID:   code.BranchID,
		TableID:    code.TableID,
		QRCodeID:   code.ID,
		SessionID:  sessionID,
		TableLabel: label,
	}, nil
}
