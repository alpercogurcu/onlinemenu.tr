// Package storage provides the ObjectStore interface for MinIO-backed file operations.
// Concrete implementation (MinioStore) is wired by platform/storage/minio.go in Phase 1.
package storage

import "context"

// ObjectStore abstracts MinIO object operations used across the platform.
// Callers must not depend on MinIO-specific types; use this interface instead.
type ObjectStore interface {
	// PutObject uploads data to the given bucket under key and sets its content type.
	PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error

	// GetPresignedURL returns a temporary download URL valid for 15 minutes by default.
	GetPresignedURL(ctx context.Context, bucket, key string) (string, error)

	// DeleteObject removes the object identified by key from bucket.
	DeleteObject(ctx context.Context, bucket, key string) error
}

// Bucket name constants mirror the buckets initialised by docker-compose init scripts.
const (
	BucketInvoices   = "invoices"
	BucketMenuImages = "menu-images"
	BucketHRDocs     = "hr-documents"
	BucketTenantDocs = "tenant-documents"
)
