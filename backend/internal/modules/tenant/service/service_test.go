package service

// White-box unit tests for the pure validation/error-mapping helpers in
// service.go. These need no database and run as fast unit tests; the
// service package previously had zero test coverage at all. DB-backed
// behavior (transaction boundaries, RLS interaction) is covered at the repo
// package's integration-test level (repo/*_test.go), which reuses the
// existing testcontainers harness — see repo/defects_test.go for why a
// service-level DB test would violate go-arch-lint's repo->service
// dependency direction if placed there instead.

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	pub "onlinemenu.tr/internal/modules/tenant/public"
)

func validDocumentFixture() pub.Document {
	return pub.Document{
		FileKey:      "tenants/x/vergi_levhasi/1.pdf",
		FileName:     "vergi-levhasi.pdf",
		FileSize:     1024,
		MimeType:     "application/pdf",
		DocumentType: pub.DocVergiLevhasi,
	}
}

func TestValidateDocument(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(d pub.Document) pub.Document
		wantErr error
	}{
		{"valid document passes", func(d pub.Document) pub.Document { return d }, nil},
		{"empty file key rejected", func(d pub.Document) pub.Document { d.FileKey = ""; return d }, pub.ErrInvalid},
		{"empty file name rejected", func(d pub.Document) pub.Document { d.FileName = ""; return d }, pub.ErrInvalid},
		{"zero file size rejected", func(d pub.Document) pub.Document { d.FileSize = 0; return d }, pub.ErrInvalid},
		{"negative file size rejected", func(d pub.Document) pub.Document { d.FileSize = -1; return d }, pub.ErrInvalid},
		{"oversized file rejected", func(d pub.Document) pub.Document { d.FileSize = maxDocumentSize + 1; return d }, pub.ErrInvalid},
		{"file at exactly the size cap is accepted", func(d pub.Document) pub.Document { d.FileSize = maxDocumentSize; return d }, nil},
		{"disallowed mime type rejected", func(d pub.Document) pub.Document { d.MimeType = "application/zip"; return d }, pub.ErrInvalid},
		{"unknown document type rejected", func(d pub.Document) pub.Document { d.DocumentType = "not_a_real_type"; return d }, pub.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDocument(tt.mutate(validDocumentFixture()))
			if tt.wantErr == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func validBranchDocumentFixture() pub.BranchDocument {
	return pub.BranchDocument{
		FileKey:      "branches/x/kira_sozlesmesi/1.pdf",
		FileName:     "kira.pdf",
		FileSize:     1024,
		MimeType:     "application/pdf",
		DocumentType: pub.BranchDocKiraSozlesmesi,
	}
}

func TestValidateBranchDocument(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(d pub.BranchDocument) pub.BranchDocument
		wantErr error
	}{
		{"valid branch document passes", func(d pub.BranchDocument) pub.BranchDocument { return d }, nil},
		{"empty file key rejected", func(d pub.BranchDocument) pub.BranchDocument { d.FileKey = ""; return d }, pub.ErrInvalid},
		{"zero file size rejected", func(d pub.BranchDocument) pub.BranchDocument { d.FileSize = 0; return d }, pub.ErrInvalid},
		{"disallowed mime type rejected", func(d pub.BranchDocument) pub.BranchDocument { d.MimeType = "text/plain"; return d }, pub.ErrInvalid},
		{"unknown branch document type rejected", func(d pub.BranchDocument) pub.BranchDocument { d.DocumentType = "not_a_real_type"; return d }, pub.ErrInvalid},
		{
			// tenant document types and branch document types are distinct
			// enums that happen to share some string values (e.g.
			// "vergi_levhasi") but not all — a tenant-only value must be
			// rejected here.
			name: "tenant-only document type rejected for branch documents",
			mutate: func(d pub.BranchDocument) pub.BranchDocument {
				d.DocumentType = pub.BranchDocumentType(pub.DocFaaliyetBelgesi)
				return d
			},
			wantErr: pub.ErrInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBranchDocument(tt.mutate(validBranchDocumentFixture()))
			if tt.wantErr == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestValidDocumentStatus(t *testing.T) {
	valid := []pub.DocumentStatus{pub.DocStatusPending, pub.DocStatusVerified, pub.DocStatusRejected, pub.DocStatusExpired}
	for _, s := range valid {
		assert.Truef(t, validDocumentStatus(s), "%s must be a valid status", s)
	}
	assert.False(t, validDocumentStatus(pub.DocumentStatus("approved")), "unknown status must be rejected")
	assert.False(t, validDocumentStatus(pub.DocumentStatus("")), "empty status must be rejected")
}

func TestWrapNotFound(t *testing.T) {
	t.Run("ErrNotFound passes through unwrapped", func(t *testing.T) {
		err := wrapNotFound(pub.ErrNotFound, "service: op: %w")
		assert.Equal(t, pub.ErrNotFound, err)
	})

	t.Run("ErrInvalid passes through unwrapped", func(t *testing.T) {
		err := wrapNotFound(pub.ErrInvalid, "service: op: %w")
		assert.Equal(t, pub.ErrInvalid, err)
	})

	t.Run("wrapped ErrNotFound still passes through unwrapped, not double-wrapped", func(t *testing.T) {
		inner := fmt.Errorf("pgx: %w", pub.ErrNotFound)
		err := wrapNotFound(inner, "service: op: %w")
		assert.ErrorIs(t, err, pub.ErrNotFound)
		assert.Equal(t, inner, err, "wrapNotFound must return the original error unchanged for sentinel matches, not re-wrap it")
	})

	t.Run("other errors are wrapped with the given format", func(t *testing.T) {
		other := errors.New("connection reset")
		err := wrapNotFound(other, "service: op: %w")
		assert.ErrorIs(t, err, other)
		assert.Contains(t, err.Error(), "service: op:")
	})
}
