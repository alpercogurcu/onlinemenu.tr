package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"
	"go.uber.org/zap"

	identitypub "onlinemenu.tr/internal/modules/identity/public"
	"onlinemenu.tr/internal/modules/payment/domain"
	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

const (
	// pinMaxAttempts is the ADR-DATA-008 PIN akışı §5 lockout threshold:
	// the 5th consecutive failure locks the PIN path for that person on
	// that session until they re-join via the full Keycloak flow.
	pinMaxAttempts = 5

	// pinLockoutTTL bounds how long a Redis lockout counter lives. It is
	// NOT the lockout duration (the lockout itself never expires on its
	// own — only a re-join via Join clears it, per the ADR) — it is
	// housekeeping so an abandoned counter for a session that will never
	// be re-joined does not sit in Redis forever. 24h comfortably outlives
	// any realistic single shift.
	pinLockoutTTL = 24 * time.Hour

	pinLockoutKeyPrefix = "cash_session_pin_lockout:"
)

// CashSessionPinService implements ADR-DATA-008 "PIN akışının ayrıntıları":
// cashier participation (join), PIN-based switching, and manager-driven PIN
// reset. It owns cash_session_participants (payment's table) and the Redis
// brute-force counter; PIN storage/verification itself is delegated to
// identity via identitypub.CashierPinService — payment never sees a PIN
// hash, and this file never imports identity/domain or identity/repo
// (module isolation, enforced by go-arch-lint).
type CashSessionPinService struct {
	db          *db.Pool
	sessions    *repo.CashSessionRepo
	pins        identitypub.CashierPinService
	memberships identitypub.MembershipResolver
	signer      *auth.ContextTokenSigner
	redis       *redis.Client
	logger      *zap.Logger
}

// CashSessionPinParams groups fx-injected dependencies.
type CashSessionPinParams struct {
	fx.In

	DB          *db.Pool
	Sessions    *repo.CashSessionRepo
	Pins        identitypub.CashierPinService
	Memberships identitypub.MembershipResolver
	Signer      *auth.ContextTokenSigner
	Redis       *redis.Client
	Logger      *zap.Logger
}

// NewCashSessionPinService constructs a CashSessionPinService for fx injection.
func NewCashSessionPinService(p CashSessionPinParams) *CashSessionPinService {
	return &CashSessionPinService{
		db: p.DB, sessions: p.Sessions, pins: p.Pins, memberships: p.Memberships,
		signer: p.Signer, redis: p.Redis, logger: p.Logger,
	}
}

// Join records that the calling principal joined sessionID via the full
// Keycloak flow (ADR-DATA-008 PIN akışı §4) and, if pin is non-empty,
// sets/replaces the principal's own PIN in the same trusted moment (§2).
// pin is optional: a cashier who joins without supplying one simply cannot
// be PIN-switched into later — identity.VerifyPin fails the same way for
// "no PIN set" as for "wrong PIN" (enumeration-safe by construction), so
// there is no separate "not allowed to join" failure mode to invent here.
//
// The caller's principal MUST be freshly Keycloak-authenticated — its
// SessionID claim must be uuid.Nil, i.e. it must NOT itself be a token
// issued by Switch below. Allowing an already-PIN-switched principal to call
// Join would let PIN-based access transitively re-mint trust it was never
// granted (the "trusted moment" the ADR requires is the actual Keycloak
// login, not a token derived from a PIN guess).
func (s *CashSessionPinService) Join(ctx context.Context, principal auth.Principal, sessionID uuid.UUID, pin string) error {
	if !principal.IsStaff() {
		return pub.ErrBranchForbidden
	}
	if principal.SessionID != uuid.Nil {
		return pub.ErrSessionScopedPrincipal
	}

	session, err := s.getSession(ctx, principal.TenantID, sessionID)
	if err != nil {
		return err
	}
	if err := requireBranch(ctx, principal, session.BranchID); err != nil {
		return err
	}
	if session.Status == domain.CashSessionClosed {
		return pub.ErrCashSessionClosed
	}

	err = s.db.WithTenantTx(ctx, principal.TenantID, func(tx pgx.Tx) error {
		return s.sessions.UpsertParticipant(ctx, tx, domain.CashSessionParticipant{
			TenantID:  principal.TenantID,
			SessionID: sessionID,
			BranchID:  session.BranchID,
			PersonID:  principal.PersonID,
		})
	})
	if err != nil {
		return fmt.Errorf("payment/service: join cash session: %w", err)
	}

	// A re-join is the ADR §5 mechanism that clears this person's lockout on
	// this session — do this unconditionally, even if pin is empty, since
	// joining itself (not PIN-setting) is what the ADR ties the unlock to.
	if err := s.redis.Del(ctx, pinLockoutKey(sessionID, principal.PersonID)).Err(); err != nil {
		// Best-effort: a stale lockout counter self-heals via pinLockoutTTL,
		// so a Redis hiccup here must not fail the join itself.
		s.logger.Warn("payment/service: clear pin lockout on join failed", zap.Error(err))
	}

	if pin != "" {
		if err := s.pins.SetOwnPin(ctx, principal.TenantID, principal.PersonID, pin); err != nil {
			if errors.Is(err, identitypub.ErrPinFormatInvalid) {
				return fmt.Errorf("%w: pin must be 4-6 digits", pub.ErrInvalidInput)
			}
			return fmt.Errorf("payment/service: join cash session: set pin: %w", err)
		}
	}

	s.logger.Info("payment/service: cashier joined cash session",
		zap.String("tenant_id", principal.TenantID.String()),
		zap.String("session_id", sessionID.String()),
		zap.String("person_id", principal.PersonID.String()))
	return nil
}

// Switch verifies pin against targetPersonID's stored PIN and, on success,
// issues a session-scoped CTX token that acts as targetPersonID for the
// remainder of sessionID's lifetime (ADR-DATA-008 PIN akışı §2/§3/§4).
//
// The caller's principal need only be a valid staff principal already
// authorized at this branch — it may itself be session-scoped (switching
// again to a third cashier is exactly the "onlarca kez" use case the ADR
// exists for).
func (s *CashSessionPinService) Switch(ctx context.Context, principal auth.Principal, sessionID, targetPersonID uuid.UUID, pin string) (string, error) {
	if !principal.IsStaff() {
		return "", pub.ErrBranchForbidden
	}

	session, err := s.getSession(ctx, principal.TenantID, sessionID)
	if err != nil {
		return "", err
	}
	if err := requireBranch(ctx, principal, session.BranchID); err != nil {
		return "", err
	}
	if session.Status == domain.CashSessionClosed {
		return "", pub.ErrCashSessionClosed
	}

	locked, err := s.isLocked(ctx, sessionID, targetPersonID)
	if err != nil {
		return "", fmt.Errorf("payment/service: switch cashier: check lockout: %w", err)
	}
	if locked {
		// Fast path, deliberately skipping VerifyPin's argon2id work: this
		// is primarily a cost control (an unlimited stream of 64MiB argon2id
		// computations per request is itself a resource-exhaustion vector),
		// not a timing-parity claim — a locked person's request is already
		// distinguishable in timing from a not-yet-locked wrong guess. The
		// requirement this codebase must hold is wrong-PIN vs unknown-person
		// parity, which is unaffected: this branch only fires once the
		// caller has already made 5 failed attempts against a KNOWN
		// participant, at which point "known cashier, locked" is not new
		// information an attacker gains for free.
		s.logger.Info("payment/service: pin verify rejected — locked",
			zap.String("session_id", sessionID.String()), zap.String("person_id", targetPersonID.String()))
		return "", pub.ErrPinVerificationFailed
	}

	var participantOK bool
	err = s.db.WithTenantReadTx(ctx, principal.TenantID, func(tx pgx.Tx) error {
		var err error
		participantOK, err = s.sessions.IsParticipant(ctx, tx, principal.TenantID, sessionID, targetPersonID)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("payment/service: switch cashier: check participant: %w", err)
	}

	// VerifyPin is called UNCONDITIONALLY — even when participantOK is
	// false — so "not a participant" costs exactly as much CPU time as
	// "participant, wrong PIN". Short-circuiting on participantOK would
	// reopen the timing side-channel VerifyPin's own dummy-hash path exists
	// to close.
	pinErr := s.pins.VerifyPin(ctx, principal.TenantID, targetPersonID, pin)
	if pinErr != nil && !errors.Is(pinErr, identitypub.ErrPinVerificationFailed) {
		// A genuine infra failure (DB down, etc.) is not a verification
		// outcome — it must not be folded into the lockout counter or the
		// generic 401, or a Postgres blip would silently start locking out
		// every cashier who happens to attempt a switch during it.
		return "", fmt.Errorf("payment/service: switch cashier: verify pin: %w", pinErr)
	}
	pinOK := pinErr == nil

	if !participantOK || !pinOK {
		becameLocked, lockErr := s.recordFailure(ctx, sessionID, targetPersonID)
		if lockErr != nil {
			s.logger.Warn("payment/service: record pin failure failed", zap.Error(lockErr))
		}
		if becameLocked {
			// ADR-DATA-008 PIN akışı §5: "kilitlenme olayı denetim izine
			// yazılır — sessizce kilitlenip kasiyeri şaşırtmamalı." Warn,
			// not Info: this is the audit-trail event a manager reviewing
			// access needs to see. Never logs the PIN or its hash.
			s.logger.Warn("payment/service: pin path locked after repeated failures",
				zap.String("tenant_id", principal.TenantID.String()),
				zap.String("session_id", sessionID.String()),
				zap.String("person_id", targetPersonID.String()))
		} else {
			s.logger.Info("payment/service: pin verify failed",
				zap.String("session_id", sessionID.String()), zap.String("person_id", targetPersonID.String()))
		}
		return "", pub.ErrPinVerificationFailed
	}

	if err := s.redis.Del(ctx, pinLockoutKey(sessionID, targetPersonID)).Err(); err != nil {
		s.logger.Warn("payment/service: clear pin lockout on success failed", zap.Error(err))
	}

	roleIDs, err := s.memberships.ActiveRoleIDsAt(ctx, principal.TenantID, targetPersonID, session.BranchID)
	if err != nil {
		return "", fmt.Errorf("payment/service: switch cashier: resolve roles: %w", err)
	}

	// sessionDeadline is nil: cash_sessions carries no scheduled-close
	// timestamp today, so IssueStaffForSession's min(8h, deadline) resolves
	// to plain 8h — see that function's doc comment. The bound that
	// actually matters (closing invalidates the token) is enforced by
	// RequireOpenSession on every subsequent request, not by this exp.
	token, err := s.signer.IssueStaffForSession(targetPersonID, principal.TenantID, session.BranchID, sessionID, roleIDs, nil)
	if err != nil {
		return "", fmt.Errorf("payment/service: switch cashier: issue token: %w", err)
	}

	s.logger.Info("payment/service: cashier switched via pin",
		zap.String("tenant_id", principal.TenantID.String()),
		zap.String("session_id", sessionID.String()),
		zap.String("acting_person_id", principal.PersonID.String()),
		zap.String("switched_to_person_id", targetPersonID.String()))
	return token, nil
}

// ResetPin clears targetPersonID's PIN (manager-only — see
// identitypub.CashierPinService.ResetPin: identity itself exposes no path
// for a manager to read or set another person's PIN, only to clear it).
// sessionID is used only to resolve/authorize the branch the caller must be
// staff at; the PIN itself is (person, tenant)-scoped, not session-scoped,
// so this clears it everywhere in the tenant, not just for this session.
func (s *CashSessionPinService) ResetPin(ctx context.Context, principal auth.Principal, sessionID, targetPersonID uuid.UUID) error {
	if !principal.IsStaff() {
		return pub.ErrBranchForbidden
	}
	session, err := s.getSession(ctx, principal.TenantID, sessionID)
	if err != nil {
		return err
	}
	if err := requireBranch(ctx, principal, session.BranchID); err != nil {
		return err
	}

	if err := s.pins.ResetPin(ctx, principal.TenantID, targetPersonID); err != nil {
		return fmt.Errorf("payment/service: reset cashier pin: %w", err)
	}
	if err := s.redis.Del(ctx, pinLockoutKey(sessionID, targetPersonID)).Err(); err != nil {
		s.logger.Warn("payment/service: clear pin lockout on reset failed", zap.Error(err))
	}

	s.logger.Warn("payment/service: cashier pin reset by manager",
		zap.String("tenant_id", principal.TenantID.String()),
		zap.String("session_id", sessionID.String()),
		zap.String("acting_person_id", principal.PersonID.String()),
		zap.String("target_person_id", targetPersonID.String()))
	return nil
}

// IsOpen reports whether sessionID is still open — satisfies
// auth.SessionValidator structurally (see payment.CashSessionOpenChecker,
// the thin adapter that wires this into RequireOpenSession).
func (s *CashSessionPinService) IsOpen(ctx context.Context, tenantID, sessionID uuid.UUID) (bool, error) {
	session, err := s.getSession(ctx, tenantID, sessionID)
	if err != nil {
		if errors.Is(err, pub.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return session.Status != domain.CashSessionClosed, nil
}

// getSession is a plain (unlocked) read used by Join/Switch/ResetPin/IsOpen —
// none of them mutate the session row, so GetByIDForUpdate's lock is
// unnecessary contention.
func (s *CashSessionPinService) getSession(ctx context.Context, tenantID, sessionID uuid.UUID) (domain.CashSession, error) {
	var session domain.CashSession
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		session, err = s.sessions.GetByID(ctx, tx, tenantID, sessionID)
		return err
	})
	if errors.Is(err, domain.ErrNotFound) {
		return domain.CashSession{}, pub.ErrNotFound
	}
	if err != nil {
		return domain.CashSession{}, fmt.Errorf("payment/service: get cash session: %w", err)
	}
	return session, nil
}

func pinLockoutKey(sessionID, personID uuid.UUID) string {
	return pinLockoutKeyPrefix + sessionID.String() + ":" + personID.String()
}

// isLocked reports whether sessionID+personID has reached pinMaxAttempts.
func (s *CashSessionPinService) isLocked(ctx context.Context, sessionID, personID uuid.UUID) (bool, error) {
	val, err := s.redis.Get(ctx, pinLockoutKey(sessionID, personID)).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("payment/service: read pin attempt counter: %w", err)
	}
	count, err := strconv.Atoi(val)
	if err != nil {
		// A corrupt counter value must fail closed toward "not locked" being
		// re-derived from scratch, not toward silently granting access —
		// treating this as "locked" is the safer of the two wrong answers.
		return true, fmt.Errorf("payment/service: corrupt pin attempt counter %q: %w", val, err)
	}
	return count >= pinMaxAttempts, nil
}

// recordFailure increments the attempt counter and reports whether this
// increment is the one that crossed pinMaxAttempts (the transition into
// "locked" — this is what gets audit-logged, not every attempt while
// already locked, per Switch's caller).
func (s *CashSessionPinService) recordFailure(ctx context.Context, sessionID, personID uuid.UUID) (becameLocked bool, err error) {
	key := pinLockoutKey(sessionID, personID)
	count, err := s.redis.Incr(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("payment/service: increment pin attempt counter: %w", err)
	}
	if count == 1 {
		if err := s.redis.Expire(ctx, key, pinLockoutTTL).Err(); err != nil {
			s.logger.Warn("payment/service: set pin attempt counter ttl failed", zap.Error(err))
		}
	}
	return count == pinMaxAttempts, nil
}
