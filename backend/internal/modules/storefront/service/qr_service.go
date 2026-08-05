package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

// qrTokenBytes is the entropy of a raw QR token before base64url encoding.
// 32 bytes = 256 bits, per ADR-ARCH-006 §4: the token is the ONLY secret
// standing between a passer-by and a table's ordering session, and it is
// printed on a sticker that is never rotated on a schedule, so it must be
// infeasible to guess or enumerate.
const qrTokenBytes = 32

// QRService owns the lifecycle of table QR codes.
type QRService struct {
	db     *db.Pool
	qrRepo *repo.QRCodeRepo
	logger *zap.Logger
}

// QRParams groups fx-injected dependencies.
type QRParams struct {
	fx.In

	DB     *db.Pool
	QRRepo *repo.QRCodeRepo
	Logger *zap.Logger
}

func NewQRService(p QRParams) *QRService {
	return &QRService{db: p.DB, qrRepo: p.QRRepo, logger: p.Logger}
}

// IssueRequest is the input to QRService.Issue.
type IssueRequest struct {
	BranchID   uuid.UUID
	TableID    uuid.UUID
	TableLabel string
	CreatedBy  uuid.UUID
}

// IssuedQRCode pairs a persisted QR code with its raw token.
//
// RawToken is populated exactly once, here, and is never persisted or
// re-derivable: only its SHA-256 hash reaches the database. Callers must put
// it in the create/rotate HTTP response and nowhere else — not in a log, not
// in an event payload, not in a list endpoint.
type IssuedQRCode struct {
	Code     domain.QRCode
	RawToken string
}

// Issue mints a new QR code for a table.
//
// The uniqueness of "one active code per table" is enforced by
// storefront_qr_codes_active_table_uidx, not by a read-then-write check here:
// two concurrent issues would both see "no active code" and both insert. The
// loser gets ErrTableAlreadyHasCode, mapped to pub.ErrInvalidTransition.
func (s *QRService) Issue(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, req IssueRequest) (IssuedQRCode, error) {
	if err := requireBranch(ctx, principal, req.BranchID); err != nil {
		return IssuedQRCode{}, err
	}

	rawToken, tokenHash, err := newQRToken()
	if err != nil {
		return IssuedQRCode{}, err
	}

	var created domain.QRCode
	err = s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = s.qrRepo.Create(ctx, tx, domain.QRCode{
			TenantID:   tenantID,
			BranchID:   req.BranchID,
			TableID:    req.TableID,
			TableLabel: req.TableLabel,
			TokenHash:  tokenHash,
			Status:     domain.QRCodeStatusActive,
			CreatedBy:  req.CreatedBy,
		})
		return err
	})
	if err != nil {
		return IssuedQRCode{}, mapQRErr(err, "storefront/service/qr: issue: %w")
	}
	return IssuedQRCode{Code: created, RawToken: rawToken}, nil
}

// List returns a branch's QR codes. The returned rows carry TokenHash, never
// a raw token — the DTO layer must not surface the hash either.
func (s *QRService) List(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, branchID uuid.UUID) ([]domain.QRCode, error) {
	if err := requireBranch(ctx, principal, branchID); err != nil {
		return nil, err
	}

	var codes []domain.QRCode
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		codes, err = s.qrRepo.ListByBranch(ctx, tx, branchID)
		return err
	})
	if err != nil {
		return nil, mapQRErr(err, "storefront/service/qr: list: %w")
	}
	return codes, nil
}

// GetByID returns a single QR code the acting principal may see.
func (s *QRService) GetByID(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, id uuid.UUID) (domain.QRCode, error) {
	var code domain.QRCode
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		code, err = s.qrRepo.GetByID(ctx, tx, id)
		return err
	})
	if err != nil {
		return domain.QRCode{}, mapQRErr(err, "storefront/service/qr: get by id: %w")
	}
	// Branch authorization runs after the load because the code's branch is
	// only known once loaded, and before anything is returned to the caller.
	if err := requireBranch(ctx, principal, code.BranchID); err != nil {
		return domain.QRCode{}, err
	}
	return code, nil
}

// Revoke retires a QR code. The row is locked first so two concurrent
// revoke/rotate calls serialize instead of both observing "active".
func (s *QRService) Revoke(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, id, revokedBy uuid.UUID) (domain.QRCode, error) {
	var revoked domain.QRCode
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.qrRepo.GetForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, current.BranchID); err != nil {
			return err
		}
		if err := domain.TransitionQRCodeStatus(current.Status, domain.QRCodeStatusRevoked); err != nil {
			return err
		}
		revoked, err = s.qrRepo.Revoke(ctx, tx, id, revokedBy)
		return err
	})
	if err != nil {
		return domain.QRCode{}, mapQRErr(err, "storefront/service/qr: revoke: %w")
	}
	return revoked, nil
}

// Rotate replaces a table's QR code with a freshly minted one.
//
// Revoke and create happen in ONE transaction: the partial unique index
// allows only one active code per table, so doing them separately would
// leave a window where the table has no working code (or, on failure, an
// orphaned revocation). The old row is kept, not deleted — it is the audit
// trail of a token that was once printed.
func (s *QRService) Rotate(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, id, actorID uuid.UUID) (IssuedQRCode, error) {
	rawToken, tokenHash, err := newQRToken()
	if err != nil {
		return IssuedQRCode{}, err
	}

	var created domain.QRCode
	err = s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.qrRepo.GetForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, current.BranchID); err != nil {
			return err
		}
		if err := domain.TransitionQRCodeStatus(current.Status, domain.QRCodeStatusRevoked); err != nil {
			return err
		}
		if _, err := s.qrRepo.Revoke(ctx, tx, id, actorID); err != nil {
			return err
		}
		created, err = s.qrRepo.Create(ctx, tx, domain.QRCode{
			TenantID:   tenantID,
			BranchID:   current.BranchID,
			TableID:    current.TableID,
			TableLabel: current.TableLabel,
			TokenHash:  tokenHash,
			Status:     domain.QRCodeStatusActive,
			CreatedBy:  actorID,
		})
		return err
	})
	if err != nil {
		return IssuedQRCode{}, mapQRErr(err, "storefront/service/qr: rotate: %w")
	}
	return IssuedQRCode{Code: created, RawToken: rawToken}, nil
}

// newQRToken generates a raw token and its storage hash together, so the two
// can never be derived by different code paths and drift apart.
func newQRToken() (rawToken, tokenHash string, err error) {
	buf := make([]byte, qrTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("storefront/service/qr: generate token: %w", err)
	}
	rawToken = base64.RawURLEncoding.EncodeToString(buf)
	return rawToken, HashQRToken(rawToken), nil
}

// HashQRToken is the single definition of "how a raw QR token becomes the
// value stored in storefront_qr_codes.token_hash": lowercase hex SHA-256.
// Both the issue path and the lookup path go through it, so a change in one
// cannot silently invalidate every printed sticker. It also matches the
// format db.WithQRTokenLookupTx validates before opening a transaction.
func HashQRToken(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

// mapQRErr translates repo/domain sentinels to their public equivalents so
// HTTP handlers can map them to status codes; anything else keeps operation
// context via format.
func mapQRErr(err error, format string) error {
	if errors.Is(err, pub.ErrBranchForbidden) {
		return err
	}
	if errors.Is(err, repo.ErrNotFound) {
		return pub.ErrNotFound
	}
	if errors.Is(err, repo.ErrInvalidTransition) ||
		errors.Is(err, repo.ErrTableAlreadyHasCode) ||
		errors.Is(err, domain.ErrInvalidTransition) {
		return pub.ErrInvalidTransition
	}
	return fmt.Errorf(format, err)
}
