package service

import (
	"errors"

	pub "onlinemenu.tr/internal/modules/payment/public"
	pospub "onlinemenu.tr/internal/modules/pos/public"
)

// translateCheckGuardErr re-expresses a pos verdict as payment's own sentinel,
// or returns nil when err is not a verdict at all (an unreachable database, a
// cancelled context). The nil case is load-bearing: RegisterSale must answer
// 409 only when pos actually refused the check, and 500 when it could not be
// asked — collapsing the two would tell a cashier "adisyon kapalı" every time
// the pool is exhausted.
//
// It lives here so payment_http keeps importing payment_public only: pos's
// sentinels stop at the service boundary, exactly as identity's
// ErrPinVerificationFailed does (see pub.ErrPinVerificationFailed).
func translateCheckGuardErr(err error) error {
	switch {
	case errors.Is(err, pospub.ErrCheckNotOpen):
		return pub.ErrCheckNotOpen
	case errors.Is(err, pospub.ErrCheckBranchMismatch):
		return pub.ErrCheckBranchMismatch
	case errors.Is(err, pospub.ErrNotFound):
		return pub.ErrCheckNotFound
	default:
		return nil
	}
}
