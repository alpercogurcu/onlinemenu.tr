package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	contextTokenIssuer = "onlinemenu"
	contextTokenTTL    = 8 * time.Hour
	contextTokenTyp    = "CTX"
)

var (
	ErrTokenExpired = errors.New("auth: context token expired")
	ErrTokenInvalid = errors.New("auth: context token invalid")
)

// contextTokenHeader is the fixed header embedded in every context token.
type contextTokenHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// contextTokenClaims is the payload signed into a context token.
type contextTokenClaims struct {
	Iss      string   `json:"iss"`
	Sub      string   `json:"sub"` // person UUID
	Ctx      string   `json:"ctx"` // "staff" | "customer"
	TenantID string   `json:"tid,omitempty"`
	BranchID string   `json:"bid,omitempty"`
	RoleIDs  []string `json:"rids,omitempty"`
	Exp      int64    `json:"exp"`
}

// ContextTokenSigner issues and verifies platform-signed context tokens.
// These are distinct from Keycloak JWTs: the middleware checks the Typ header
// and routes CTX tokens here, leaving standard JWTs to KeycloakVerifier.
type ContextTokenSigner struct {
	secret []byte
}

// NewContextTokenSigner constructs a signer from a raw secret.
// The secret must be at least 32 bytes (enforced at startup via fx).
func NewContextTokenSigner(secret []byte) (*ContextTokenSigner, error) {
	if len(secret) < 32 {
		return nil, errors.New("auth: context token secret must be at least 32 bytes")
	}
	return &ContextTokenSigner{secret: secret}, nil
}

// IssueStaff creates a context token for a branch-scoped staff session.
func (s *ContextTokenSigner) IssueStaff(personID, tenantID, branchID uuid.UUID, roleIDs []uuid.UUID) (string, error) {
	rids := make([]string, len(roleIDs))
	for i, id := range roleIDs {
		rids[i] = id.String()
	}
	return s.sign(contextTokenClaims{
		Iss:      contextTokenIssuer,
		Sub:      personID.String(),
		Ctx:      string(ContextStaff),
		TenantID: tenantID.String(),
		BranchID: branchID.String(),
		RoleIDs:  rids,
		Exp:      time.Now().Add(contextTokenTTL).Unix(),
	})
}

// IssueCustomer creates a context token for a platform-wide customer session.
func (s *ContextTokenSigner) IssueCustomer(personID uuid.UUID) (string, error) {
	return s.sign(contextTokenClaims{
		Iss: contextTokenIssuer,
		Sub: personID.String(),
		Ctx: string(ContextCustomer),
		Exp: time.Now().Add(contextTokenTTL).Unix(),
	})
}

// Verify parses and validates a context token, returning the encoded Principal.
func (s *ContextTokenSigner) Verify(raw string) (Principal, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Principal{}, ErrTokenInvalid
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Principal{}, ErrTokenInvalid
	}
	var hdr contextTokenHeader
	if err := json.Unmarshal(headerJSON, &hdr); err != nil || hdr.Typ != contextTokenTyp {
		return Principal{}, ErrTokenInvalid
	}

	if !s.validSignature(parts[0]+"."+parts[1], parts[2]) {
		return Principal{}, ErrTokenInvalid
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Principal{}, ErrTokenInvalid
	}
	var claims contextTokenClaims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return Principal{}, ErrTokenInvalid
	}

	if time.Now().Unix() > claims.Exp {
		return Principal{}, ErrTokenExpired
	}

	return claimsToContextPrincipal(claims)
}

// IsContextToken returns true if the raw token's Typ header is CTX.
// Used by the middleware to route tokens without full verification.
func IsContextToken(raw string) bool {
	parts := strings.SplitN(raw, ".", 2)
	if len(parts) < 1 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	var hdr contextTokenHeader
	if err := json.Unmarshal(b, &hdr); err != nil {
		return false
	}
	return hdr.Typ == contextTokenTyp
}

func (s *ContextTokenSigner) sign(claims contextTokenClaims) (string, error) {
	hdrJSON, err := json.Marshal(contextTokenHeader{Alg: "HS256", Typ: contextTokenTyp})
	if err != nil {
		return "", fmt.Errorf("auth: marshal token header: %w", err)
	}
	payJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("auth: marshal token claims: %w", err)
	}

	hdrEnc := base64.RawURLEncoding.EncodeToString(hdrJSON)
	payEnc := base64.RawURLEncoding.EncodeToString(payJSON)
	sigEnc := s.computeSignature(hdrEnc + "." + payEnc)

	return hdrEnc + "." + payEnc + "." + sigEnc, nil
}

func (s *ContextTokenSigner) computeSignature(input string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(input))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *ContextTokenSigner) validSignature(input, gotEnc string) bool {
	want := s.computeSignature(input)
	return hmac.Equal([]byte(want), []byte(gotEnc))
}

func claimsToContextPrincipal(c contextTokenClaims) (Principal, error) {
	personID, err := uuid.Parse(c.Sub)
	if err != nil {
		return Principal{}, fmt.Errorf("auth: invalid sub in context token: %w", err)
	}

	ctx := Context(c.Ctx)
	if ctx != ContextStaff && ctx != ContextCustomer {
		return Principal{}, fmt.Errorf("auth: unknown context %q", c.Ctx)
	}

	p := Principal{PersonID: personID, Ctx: ctx}

	if ctx == ContextStaff {
		p.TenantID, err = uuid.Parse(c.TenantID)
		if err != nil {
			return Principal{}, fmt.Errorf("auth: invalid tid in context token: %w", err)
		}
		p.BranchID, err = uuid.Parse(c.BranchID)
		if err != nil {
			return Principal{}, fmt.Errorf("auth: invalid bid in context token: %w", err)
		}
		p.RoleIDs = make([]uuid.UUID, 0, len(c.RoleIDs))
		for _, raw := range c.RoleIDs {
			id, err := uuid.Parse(raw)
			if err != nil {
				return Principal{}, fmt.Errorf("auth: invalid role id %q in context token: %w", raw, err)
			}
			p.RoleIDs = append(p.RoleIDs, id)
		}
	}

	return p, nil
}
