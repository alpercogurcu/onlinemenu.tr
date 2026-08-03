package keycloak

import "errors"

// ErrNotConfigured is returned by every Client method when the process was
// started without a Keycloak admin service account configured (Config.ClientID
// empty). This is a deliberate, legible failure at call time rather than a
// boot-time failure — see Config's doc comment. It surfaces to the staff
// invite caller as a real (5xx) error instead of the process refusing to
// start in dev/CI, where no local Keycloak admin client is set up.
var ErrNotConfigured = errors.New("keycloak: admin API not configured")

// ErrUserAlreadyExists is returned by CreateUser when Keycloak rejects the
// creation with 409 Conflict — another request (this invite's own retry, or a
// concurrent invite for the same email from a different tenant) created the
// user between this call's FindUserByEmail and CreateUser. Callers must
// recover by calling FindUserByEmail again, never by treating this as a hard
// failure: the ADR requires reusing the existing user, never creating a
// second one.
var ErrUserAlreadyExists = errors.New("keycloak: user already exists")
