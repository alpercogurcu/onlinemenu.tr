// Package public exposes the hr-core module's contract to other modules.
package public

import (
	"context"

	"github.com/google/uuid"
)

// EmploymentStatus is the read-only employment status other modules may reference.
type EmploymentStatus string

const (
	EmploymentStatusActive     EmploymentStatus = "active"
	EmploymentStatusOnLeave    EmploymentStatus = "on_leave"
	EmploymentStatusTerminated EmploymentStatus = "terminated"
)

// EmployeeReader allows other modules to check whether a person has an active
// employment profile for a given tenant (e.g. POS shift validation in Faz 3).
type EmployeeReader interface {
	GetEmploymentStatus(ctx context.Context, tenantID, personID uuid.UUID) (EmploymentStatus, error)
}

// ErrNotFound is returned when an employee profile does not exist.
var ErrNotFound = hrNotFoundError{}

type hrNotFoundError struct{}

func (hrNotFoundError) Error() string { return "hr-core: not found" }
