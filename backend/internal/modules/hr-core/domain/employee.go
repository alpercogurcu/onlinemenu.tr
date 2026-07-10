// Package domain contains the hr-core module's core types.
// HR-core manages the tenant-scoped employment record for a person.
// Person identity (keycloak_sub, email, full_name) lives in the identity module.
// Branch assignment and role management live in identity/memberships.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// EmploymentType classifies the working arrangement.
type EmploymentType string

const (
	EmploymentTypeFull       EmploymentType = "full_time"
	EmploymentTypePart       EmploymentType = "part_time"
	EmploymentTypeSeasonal   EmploymentType = "seasonal"
	EmploymentTypeContractor EmploymentType = "contractor"
)

// Valid reports whether the employment type is a recognised value.
func (t EmploymentType) Valid() bool {
	switch t {
	case EmploymentTypeFull, EmploymentTypePart, EmploymentTypeSeasonal, EmploymentTypeContractor:
		return true
	}
	return false
}

// EmployeeStatus represents the current employment status.
type EmployeeStatus string

const (
	EmployeeStatusActive     EmployeeStatus = "active"
	EmployeeStatusOnLeave    EmployeeStatus = "on_leave"
	EmployeeStatusTerminated EmployeeStatus = "terminated"
)

// Valid reports whether the status is a recognised value.
func (s EmployeeStatus) Valid() bool {
	switch s {
	case EmployeeStatusActive, EmployeeStatusOnLeave, EmployeeStatusTerminated:
		return true
	}
	return false
}

// ContactInfo holds personal contact details stored as flexible JSON.
type ContactInfo struct {
	Phone   string `json:"phone,omitempty"`
	Address string `json:"address,omitempty"`
	City    string `json:"city,omitempty"`
}

// EmergencyContact holds emergency contact data stored as flexible JSON.
type EmergencyContact struct {
	Name     string `json:"name,omitempty"`
	Phone    string `json:"phone,omitempty"`
	Relation string `json:"relation,omitempty"`
}

// Employee is the aggregate root for an employment record.
// It links a platform-level person to a specific tenant's HR data.
type Employee struct {
	ID               uuid.UUID
	PersonID         uuid.UUID
	TenantID         uuid.UUID
	Department       string
	JobTitle         string
	EmploymentType   EmploymentType
	TCKimlikHash     string // KVKK: salted hash only — never store plaintext
	HireDate         time.Time
	TerminationDate  *time.Time
	ContactInfo      ContactInfo
	EmergencyContact EmergencyContact
	Status           EmployeeStatus
	Notes            string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
