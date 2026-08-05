// Package public exposes the storefront module's cross-module contract:
// the sentinel errors other layers (HTTP handlers, future modules) translate.
// No direct DB access across module boundaries.
package public

import "errors"

// ErrQRNotFound is returned when a QR token does not resolve to any code.
// The HTTP layer maps it to 404.
var ErrQRNotFound = errors.New("storefront: qr code not found")

// ErrQRRevoked is returned when a QR token resolves to a code that was
// revoked. It is deliberately distinct from ErrQRNotFound so the revocation
// can be logged and counted — but the HTTP layer maps BOTH to 404, because
// telling an anonymous caller "this token was real, just retired" is a free
// oracle for anyone probing tokens.
var ErrQRRevoked = errors.New("storefront: qr code revoked")

// ErrGuestForbidden is returned when a guest session tries to reach a
// resource that belongs to another session. Like the above it surfaces as
// 404, not 403: a guest must not be able to confirm the resource exists.
var ErrGuestForbidden = errors.New("storefront: forbidden for this guest session")

// ErrBranchForbidden is returned when an authenticated staff principal tries
// to manage QR codes for a branch it has no access to (ADR-AUTH-001 layer 3).
// Unlike the guest-facing errors above this maps to 403: the caller is known
// staff and the resource is already tenant-visible, so a 404 would only hide
// a misconfiguration from the person able to fix it.
var ErrBranchForbidden = errors.New("storefront: forbidden for this branch")

// ErrNotFound is the generic not-visible sentinel for storefront resources.
var ErrNotFound = errors.New("storefront: not found")

// ErrInvalidTransition is returned when a QR code status change is not
// allowed from its current status (e.g. revoking an already-revoked code).
var ErrInvalidTransition = errors.New("storefront: invalid status transition")

// ValidationError is returned when a diner's submission cannot be honoured as
// sent — an empty cart, a product the branch does not currently sell, a
// modifier that is not attached to its product. The HTTP layer checks for it
// with errors.As and answers 422.
//
// Msg is diner-facing: it is rendered into the problem detail, so it must
// stay free of internal identifiers beyond the ids the diner already sent.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return "storefront: " + e.Msg }
