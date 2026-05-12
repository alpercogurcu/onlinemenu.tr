package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	pub "onlinemenu.tr/internal/modules/tenant/public"
)

// DocumentRepo provides data access for tenant_documents and branch_documents tables.
type DocumentRepo struct{}

// NewDocumentRepo constructs a DocumentRepo for fx injection.
func NewDocumentRepo() *DocumentRepo {
	return &DocumentRepo{}
}

// ListDocuments returns all non-deleted documents for a tenant.
func (r *DocumentRepo) ListDocuments(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) ([]pub.Document, error) {
	const q = `
		SELECT id, tenant_id, document_type, file_key, file_name, file_size, mime_type,
		       status, rejection_note, valid_from, valid_until, created_at
		FROM tenant_documents
		WHERE tenant_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC`

	rows, err := tx.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("tenant/repo: list documents: %w", err)
	}
	defer rows.Close()

	var docs []pub.Document
	for rows.Next() {
		d, err := scanDocument(rows)
		if err != nil {
			return nil, fmt.Errorf("tenant/repo: scan document: %w", err)
		}
		docs = append(docs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenant/repo: list documents rows: %w", err)
	}
	if docs == nil {
		docs = []pub.Document{}
	}
	return docs, nil
}

// GetDocument fetches a single non-deleted document by tenant and document IDs.
func (r *DocumentRepo) GetDocument(ctx context.Context, tx pgx.Tx, tenantID, docID uuid.UUID) (pub.Document, error) {
	const q = `
		SELECT id, tenant_id, document_type, file_key, file_name, file_size, mime_type,
		       status, rejection_note, valid_from, valid_until, created_at
		FROM tenant_documents
		WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL`

	row := tx.QueryRow(ctx, q, tenantID, docID)
	d, err := scanDocument(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pub.Document{}, pub.ErrNotFound
		}
		return pub.Document{}, fmt.Errorf("tenant/repo: get document: %w", err)
	}
	return d, nil
}

// CreateDocument inserts a document metadata record.
func (r *DocumentRepo) CreateDocument(ctx context.Context, tx pgx.Tx, doc pub.Document) (pub.Document, error) {
	const q = `
		INSERT INTO tenant_documents (
			tenant_id, document_type, file_key, file_name, file_size, mime_type,
			status, rejection_note, valid_from, valid_until
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, tenant_id, document_type, file_key, file_name, file_size, mime_type,
		          status, rejection_note, valid_from, valid_until, created_at`

	row := tx.QueryRow(ctx, q,
		doc.TenantID, string(doc.DocumentType), doc.FileKey, doc.FileName, doc.FileSize, doc.MimeType,
		string(doc.Status), doc.RejectionNote, doc.ValidFrom, doc.ValidUntil,
	)

	created, err := scanDocument(row)
	if err != nil {
		return pub.Document{}, fmt.Errorf("tenant/repo: create document: %w", err)
	}
	return created, nil
}

// UpdateDocumentStatus changes the verification status and optional rejection note.
func (r *DocumentRepo) UpdateDocumentStatus(
	ctx context.Context, tx pgx.Tx,
	tenantID, docID uuid.UUID,
	status pub.DocumentStatus, note string,
) error {
	const q = `
		UPDATE tenant_documents
		SET status = $1, rejection_note = $2, updated_at = NOW()
		WHERE tenant_id = $3 AND id = $4 AND deleted_at IS NULL`

	ct, err := tx.Exec(ctx, q, string(status), note, tenantID, docID)
	if err != nil {
		return fmt.Errorf("tenant/repo: update document status: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return pub.ErrNotFound
	}
	return nil
}

// DeleteDocument performs a soft delete by setting deleted_at to the current timestamp.
func (r *DocumentRepo) DeleteDocument(ctx context.Context, tx pgx.Tx, tenantID, docID uuid.UUID) error {
	const q = `
		UPDATE tenant_documents
		SET deleted_at = NOW(), updated_at = NOW()
		WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL`

	ct, err := tx.Exec(ctx, q, tenantID, docID)
	if err != nil {
		return fmt.Errorf("tenant/repo: delete document: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return pub.ErrNotFound
	}
	return nil
}

// ListBranchDocuments returns all non-deleted documents for a specific branch.
func (r *DocumentRepo) ListBranchDocuments(ctx context.Context, tx pgx.Tx, tenantID, branchID uuid.UUID) ([]pub.BranchDocument, error) {
	const q = `
		SELECT id, tenant_id, branch_id, document_type, file_key, file_name, file_size, mime_type,
		       status, rejection_note, valid_from, valid_until, created_at
		FROM branch_documents
		WHERE tenant_id = $1 AND branch_id = $2 AND deleted_at IS NULL
		ORDER BY created_at DESC`

	rows, err := tx.Query(ctx, q, tenantID, branchID)
	if err != nil {
		return nil, fmt.Errorf("tenant/repo: list branch documents: %w", err)
	}
	defer rows.Close()

	var docs []pub.BranchDocument
	for rows.Next() {
		d, err := scanBranchDocument(rows)
		if err != nil {
			return nil, fmt.Errorf("tenant/repo: scan branch document: %w", err)
		}
		docs = append(docs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenant/repo: list branch documents rows: %w", err)
	}
	if docs == nil {
		docs = []pub.BranchDocument{}
	}
	return docs, nil
}

// CreateBranchDocument inserts a branch document metadata record.
func (r *DocumentRepo) CreateBranchDocument(ctx context.Context, tx pgx.Tx, doc pub.BranchDocument) (pub.BranchDocument, error) {
	const q = `
		INSERT INTO branch_documents (
			tenant_id, branch_id, document_type, file_key, file_name, file_size, mime_type,
			status, rejection_note, valid_from, valid_until
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, tenant_id, branch_id, document_type, file_key, file_name, file_size, mime_type,
		          status, rejection_note, valid_from, valid_until, created_at`

	row := tx.QueryRow(ctx, q,
		doc.TenantID, doc.BranchID, string(doc.DocumentType),
		doc.FileKey, doc.FileName, doc.FileSize, doc.MimeType,
		string(doc.Status), doc.RejectionNote, doc.ValidFrom, doc.ValidUntil,
	)

	created, err := scanBranchDocument(row)
	if err != nil {
		return pub.BranchDocument{}, fmt.Errorf("tenant/repo: create branch document: %w", err)
	}
	return created, nil
}

// UpdateBranchDocumentStatus changes the verification status and optional rejection note.
// branchID is required to prevent cross-branch mutations within the same tenant.
func (r *DocumentRepo) UpdateBranchDocumentStatus(
	ctx context.Context, tx pgx.Tx,
	tenantID, branchID, docID uuid.UUID,
	status pub.DocumentStatus, note string,
) error {
	const q = `
		UPDATE branch_documents
		SET status = $1, rejection_note = $2, updated_at = NOW()
		WHERE tenant_id = $3 AND branch_id = $4 AND id = $5 AND deleted_at IS NULL`

	ct, err := tx.Exec(ctx, q, string(status), note, tenantID, branchID, docID)
	if err != nil {
		return fmt.Errorf("tenant/repo: update branch document status: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return pub.ErrNotFound
	}
	return nil
}

// DeleteBranchDocument performs a soft delete on a branch document.
// branchID is required to prevent cross-branch mutations within the same tenant.
func (r *DocumentRepo) DeleteBranchDocument(ctx context.Context, tx pgx.Tx, tenantID, branchID, docID uuid.UUID) error {
	const q = `
		UPDATE branch_documents
		SET deleted_at = NOW(), updated_at = NOW()
		WHERE tenant_id = $1 AND branch_id = $2 AND id = $3 AND deleted_at IS NULL`

	ct, err := tx.Exec(ctx, q, tenantID, branchID, docID)
	if err != nil {
		return fmt.Errorf("tenant/repo: delete branch document: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return pub.ErrNotFound
	}
	return nil
}

func scanDocument(row rowScanner) (pub.Document, error) {
	var (
		d            pub.Document
		docType      string
		status       string
		validFrom    *time.Time
		validUntil   *time.Time
	)

	err := row.Scan(
		&d.ID, &d.TenantID, &docType, &d.FileKey, &d.FileName, &d.FileSize, &d.MimeType,
		&status, &d.RejectionNote, &validFrom, &validUntil, &d.CreatedAt,
	)
	if err != nil {
		return pub.Document{}, err
	}

	d.DocumentType = pub.DocumentType(docType)
	d.Status = pub.DocumentStatus(status)
	d.ValidFrom = validFrom
	d.ValidUntil = validUntil

	return d, nil
}

func scanBranchDocument(row rowScanner) (pub.BranchDocument, error) {
	var (
		d          pub.BranchDocument
		docType    string
		status     string
		validFrom  *time.Time
		validUntil *time.Time
	)

	err := row.Scan(
		&d.ID, &d.TenantID, &d.BranchID, &docType,
		&d.FileKey, &d.FileName, &d.FileSize, &d.MimeType,
		&status, &d.RejectionNote, &validFrom, &validUntil, &d.CreatedAt,
	)
	if err != nil {
		return pub.BranchDocument{}, err
	}

	d.DocumentType = pub.BranchDocumentType(docType)
	d.Status = pub.DocumentStatus(status)
	d.ValidFrom = validFrom
	d.ValidUntil = validUntil

	return d, nil
}
