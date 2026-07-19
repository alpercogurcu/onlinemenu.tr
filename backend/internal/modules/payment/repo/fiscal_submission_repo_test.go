package repo

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/domain"
)

// TestMarkResult_ZeroCompletedAt_Errors pins the guard that replaced the old
// COALESCE($5, NOW()) fallback: NOW() reads the DATABASE host's clock, which
// is exactly the API-vs-DB clock skew CreatedAt's explicit stamping already
// guards against (see FiscalSubmission.CreatedAt). A caller that forgets to
// stamp CompletedAt must fail loudly here rather than silently pick up a
// different clock. No DB round-trip happens: the guard runs before the query,
// so a nil tx is fine.
func TestMarkResult_ZeroCompletedAt_Errors(t *testing.T) {
	r := NewFiscalSubmissionRepo()

	_, err := r.MarkResult(context.Background(), nil, uuid.New(),
		domain.FiscalSubmissionCompleted, nil, "", time.Time{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "completed_at is required")
}
