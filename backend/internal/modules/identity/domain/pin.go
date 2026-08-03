package domain

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters for cashier PIN hashing (ADR-DATA-008 PIN akışı §5).
// PINs are 4-6 digits — a small keyspace — so the defence is attempt count
// (Redis lockout, see identity/service/pin.go), not hash cost alone; these
// parameters are OWASP's second recommended argon2id point
// (m=64MiB, t=1, p=4), chosen over the first (m=19MiB, t=2, p=1) because this
// runs synchronously on a request a cashier is actively waiting on during a
// shift-change burst, and 64MiB/t=1/p=4 parallelises across the memory cost
// instead of serialising it — see identity/service/pin_test.go for a
// measured wall-clock sample on this params set.
const (
	pinArgon2Time    = 1
	pinArgon2Memory  = 64 * 1024 // KiB → 64 MiB
	pinArgon2Threads = 4
	pinArgon2KeyLen  = 32
	pinSaltLen       = 16
)

// ErrPinFormatInvalid marks a PIN that fails the 4-6 digit format rule.
// Distinct from ErrPinVerificationFailed (auth/public.go equivalent):
// format is a caller-input problem (422), verification failure is an auth
// outcome (401) that must not leak whether the person/PIN was the issue.
var ErrPinFormatInvalid = errors.New("identity/domain: pin must be 4-6 digits")

// ValidatePinFormat enforces ADR-DATA-008's "PIN 4-6 hane" shape. It is the
// only format check — HashPin/VerifyPinHash below operate on any non-empty
// byte string and do not re-validate, so every caller (SetOwnPin) must call
// this first.
func ValidatePinFormat(pin string) error {
	if len(pin) < 4 || len(pin) > 6 {
		return fmt.Errorf("%w: got length %d", ErrPinFormatInvalid, len(pin))
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			return fmt.Errorf("%w: non-digit character", ErrPinFormatInvalid)
		}
	}
	return nil
}

// HashPin generates a fresh random salt and returns the argon2id hash of pin
// under it. Called only from the trusted "cashier sets their own PIN at
// join" moment (ADR-DATA-008 PIN akışı §2) — never with another person's PIN.
func HashPin(pin string) (salt, hash []byte, err error) {
	salt = make([]byte, pinSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, nil, fmt.Errorf("identity/domain: generate pin salt: %w", err)
	}
	hash = argon2.IDKey([]byte(pin), salt, pinArgon2Time, pinArgon2Memory, pinArgon2Threads, pinArgon2KeyLen)
	return salt, hash, nil
}

// VerifyPinHash reports whether pin, hashed under salt with the same argon2id
// parameters HashPin uses, constant-time-equals hash.
func VerifyPinHash(pin string, salt, hash []byte) bool {
	computed := argon2.IDKey([]byte(pin), salt, pinArgon2Time, pinArgon2Memory, pinArgon2Threads, pinArgon2KeyLen)
	return subtle.ConstantTimeCompare(computed, hash) == 1
}

// DummyPinCost performs the same argon2id work as VerifyPinHash against a
// freshly-random salt/hash pair it generates and discards, then returns
// false. It exists purely so "person has no PIN set" and "person does not
// exist at all" cost exactly as much CPU time as "person exists, PIN is
// wrong" — see identity/service/pin.go's VerifyPin for why: the task
// requirement is that a wrong PIN and an unknown person are
// indistinguishable to the caller, in error shape AND timing, and skipping
// the hash entirely on the not-found path would leak the distinction through
// response latency alone.
func DummyPinCost(pin string) bool {
	salt := make([]byte, pinSaltLen)
	_, _ = rand.Read(salt) // best-effort; an all-zero salt still costs the same argon2id work.
	// The reference plaintext must be random, not a fixed literal like
	// "0000": a fixed literal would make this function return true (a false
	// "verified") whenever the caller's real pin happened to equal it —
	// exactly the kind of self-defeating bug a hardcoded dummy invites.
	dummyPin := make([]byte, pinSaltLen)
	_, _ = rand.Read(dummyPin)
	dummyHash := argon2.IDKey(dummyPin, salt, pinArgon2Time, pinArgon2Memory, pinArgon2Threads, pinArgon2KeyLen)
	return VerifyPinHash(pin, salt, dummyHash)
}
