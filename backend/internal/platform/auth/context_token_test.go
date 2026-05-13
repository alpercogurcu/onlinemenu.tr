package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testSecret = []byte("this-is-a-32-byte-test-secret!!!")

func newTestSigner(t *testing.T) *ContextTokenSigner {
	t.Helper()
	s, err := NewContextTokenSigner(testSecret)
	require.NoError(t, err)
	return s
}

func TestNewContextTokenSigner_RejectsShortSecret(t *testing.T) {
	_, err := NewContextTokenSigner([]byte("short"))
	require.Error(t, err)
}

func TestIssueStaff_VerifyRoundTrip(t *testing.T) {
	s := newTestSigner(t)
	personID := uuid.New()
	tenantID := uuid.New()
	branchID := uuid.New()
	roleIDs := []uuid.UUID{uuid.New(), uuid.New()}

	token, err := s.IssueStaff(personID, tenantID, branchID, roleIDs)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	p, err := s.Verify(token)
	require.NoError(t, err)

	assert.Equal(t, personID, p.PersonID)
	assert.Equal(t, tenantID, p.TenantID)
	assert.Equal(t, branchID, p.BranchID)
	assert.ElementsMatch(t, roleIDs, p.RoleIDs)
	assert.True(t, p.IsStaff())
	assert.False(t, p.IsCustomer())
	assert.False(t, p.IsPreContext())
}

func TestIssueCustomer_VerifyRoundTrip(t *testing.T) {
	s := newTestSigner(t)
	personID := uuid.New()

	token, err := s.IssueCustomer(personID)
	require.NoError(t, err)

	p, err := s.Verify(token)
	require.NoError(t, err)

	assert.Equal(t, personID, p.PersonID)
	assert.True(t, p.IsCustomer())
	assert.False(t, p.IsStaff())
	assert.Equal(t, uuid.Nil, p.TenantID)
	assert.Nil(t, p.RoleIDs)
}

func TestVerify_TamperedSignature(t *testing.T) {
	s := newTestSigner(t)
	token, err := s.IssueCustomer(uuid.New())
	require.NoError(t, err)

	parts := strings.Split(token, ".")
	parts[2] = "invalidsignature"
	tampered := strings.Join(parts, ".")

	_, err = s.Verify(tampered)
	assert.ErrorIs(t, err, ErrTokenInvalid)
}

func TestVerify_TamperedPayload(t *testing.T) {
	s := newTestSigner(t)
	token, err := s.IssueCustomer(uuid.New())
	require.NoError(t, err)

	parts := strings.Split(token, ".")
	// Replace payload with a different base64 encoding.
	parts[1] = "dGFtcGVyZWQ"
	tampered := strings.Join(parts, ".")

	_, err = s.Verify(tampered)
	assert.ErrorIs(t, err, ErrTokenInvalid)
}

func TestVerify_WrongSecret(t *testing.T) {
	signer1 := newTestSigner(t)
	signer2, err := NewContextTokenSigner([]byte("another-32-byte-test-secret!!!!!"))
	require.NoError(t, err)

	token, err := signer1.IssueCustomer(uuid.New())
	require.NoError(t, err)

	_, err = signer2.Verify(token)
	assert.ErrorIs(t, err, ErrTokenInvalid)
}

func TestVerify_MalformedToken(t *testing.T) {
	s := newTestSigner(t)

	cases := []string{"", "a.b", "a.b.c.d", "!!.!.!"}
	for _, raw := range cases {
		_, err := s.Verify(raw)
		assert.Error(t, err, "expected error for %q", raw)
	}
}

func TestVerify_ExpiredToken(t *testing.T) {
	s := newTestSigner(t)
	claims := contextTokenClaims{
		Iss: contextTokenIssuer,
		Sub: uuid.New().String(),
		Ctx: string(ContextCustomer),
		Exp: time.Now().Add(-time.Second).Unix(),
	}
	token, err := s.sign(claims)
	require.NoError(t, err)

	_, err = s.Verify(token)
	assert.ErrorIs(t, err, ErrTokenExpired)
}

func TestIsContextToken(t *testing.T) {
	s := newTestSigner(t)
	token, err := s.IssueCustomer(uuid.New())
	require.NoError(t, err)

	assert.True(t, IsContextToken(token))
	assert.False(t, IsContextToken("not.a.ctx.token"))
	assert.False(t, IsContextToken(""))
}
