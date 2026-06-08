package domain

import (
	"time"

	"github.com/google/uuid"
)

// Category groups products in a menu hierarchy.
// A category may have a parent for nested groupings (e.g. Başlangıçlar → Çorbalar).
type Category struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	BranchID    *uuid.UUID // nil = tenant-wide; non-nil = branch-specific override
	ParentID    *uuid.UUID
	Name        string
	Description string
	ImageKey    string
	IsActive    bool
	SortOrder   int16
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
