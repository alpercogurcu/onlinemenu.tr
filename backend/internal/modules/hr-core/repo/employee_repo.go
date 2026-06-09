// Package repo provides persistence for the hr-core module.
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/hr-core/domain"
)

// ErrNotFound is returned when an employee profile is not found.
var ErrNotFound = errors.New("hr-core/repo: not found")

// ErrDuplicateEmployee is returned when a person already has a profile for the tenant.
var ErrDuplicateEmployee = errors.New("hr-core/repo: employee profile already exists for this person")

// EmployeeRepo handles persistence for Employee aggregates.
type EmployeeRepo struct{}

// NewEmployeeRepo constructs an EmployeeRepo.
func NewEmployeeRepo() *EmployeeRepo { return &EmployeeRepo{} }

// Create inserts a new employee profile.
func (r *EmployeeRepo) Create(ctx context.Context, tx pgx.Tx, e domain.Employee) (domain.Employee, error) {
	e.ID = uuid.New()
	now := time.Now().UTC()
	e.CreatedAt = now
	e.UpdatedAt = now
	if e.Status == "" {
		e.Status = domain.EmployeeStatusActive
	}

	contactJSON, err := json.Marshal(e.ContactInfo)
	if err != nil {
		return domain.Employee{}, fmt.Errorf("hr-core/repo: marshal contact_info: %w", err)
	}
	emergencyJSON, err := json.Marshal(e.EmergencyContact)
	if err != nil {
		return domain.Employee{}, fmt.Errorf("hr-core/repo: marshal emergency_contact: %w", err)
	}

	var terminationDate *time.Time
	if e.TerminationDate != nil {
		t := e.TerminationDate.UTC()
		terminationDate = &t
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO employee_profiles (
			id, person_id, tenant_id,
			department, job_title, employment_type,
			tc_kimlik_hash, hire_date, termination_date,
			contact_info, emergency_contact,
			status, notes, created_at, updated_at
		) VALUES (
			$1,$2,$3,
			$4,$5,$6,
			$7,$8,$9,
			$10,$11,
			$12,$13,$14,$15
		)`,
		e.ID, e.PersonID, e.TenantID,
		e.Department, e.JobTitle, string(e.EmploymentType),
		e.TCKimlikHash, e.HireDate, terminationDate,
		string(contactJSON), string(emergencyJSON),
		string(e.Status), e.Notes, e.CreatedAt, e.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Employee{}, ErrDuplicateEmployee
		}
		return domain.Employee{}, fmt.Errorf("hr-core/repo: create: %w", err)
	}
	return e, nil
}

// GetByID returns an employee profile by its own ID.
func (r *EmployeeRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Employee, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, person_id, tenant_id,
		       department, job_title, employment_type,
		       tc_kimlik_hash, hire_date, termination_date,
		       contact_info, emergency_contact,
		       status, notes, created_at, updated_at
		FROM employee_profiles WHERE id = $1`, id)

	e, err := scanEmployee(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Employee{}, ErrNotFound
	}
	return e, err
}

// GetByPersonID returns the employee profile for a person within a tenant.
func (r *EmployeeRepo) GetByPersonID(ctx context.Context, tx pgx.Tx, tenantID, personID uuid.UUID) (domain.Employee, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, person_id, tenant_id,
		       department, job_title, employment_type,
		       tc_kimlik_hash, hire_date, termination_date,
		       contact_info, emergency_contact,
		       status, notes, created_at, updated_at
		FROM employee_profiles WHERE tenant_id = $1 AND person_id = $2`, tenantID, personID)

	e, err := scanEmployee(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Employee{}, ErrNotFound
	}
	return e, err
}

// Update modifies mutable fields of an employee profile.
func (r *EmployeeRepo) Update(ctx context.Context, tx pgx.Tx, e domain.Employee) (domain.Employee, error) {
	e.UpdatedAt = time.Now().UTC()

	contactJSON, err := json.Marshal(e.ContactInfo)
	if err != nil {
		return domain.Employee{}, fmt.Errorf("hr-core/repo: marshal contact_info: %w", err)
	}
	emergencyJSON, err := json.Marshal(e.EmergencyContact)
	if err != nil {
		return domain.Employee{}, fmt.Errorf("hr-core/repo: marshal emergency_contact: %w", err)
	}

	var terminationDate *time.Time
	if e.TerminationDate != nil {
		t := e.TerminationDate.UTC()
		terminationDate = &t
	}

	tag, err := tx.Exec(ctx, `
		UPDATE employee_profiles SET
			department = $2, job_title = $3, employment_type = $4,
			tc_kimlik_hash = $5, hire_date = $6, termination_date = $7,
			contact_info = $8, emergency_contact = $9,
			status = $10, notes = $11, updated_at = $12
		WHERE id = $1`,
		e.ID, e.Department, e.JobTitle, string(e.EmploymentType),
		e.TCKimlikHash, e.HireDate, terminationDate,
		string(contactJSON), string(emergencyJSON),
		string(e.Status), e.Notes, e.UpdatedAt,
	)
	if err != nil {
		return domain.Employee{}, fmt.Errorf("hr-core/repo: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Employee{}, ErrNotFound
	}
	return e, nil
}

// List returns employees for a tenant, optionally filtered by status.
// status="" returns all statuses.
func (r *EmployeeRepo) List(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, status domain.EmployeeStatus, limit, offset int) ([]domain.Employee, error) {
	if limit <= 0 {
		limit = 50
	}

	var rows pgx.Rows
	var err error

	if status == "" {
		rows, err = tx.Query(ctx, `
			SELECT id, person_id, tenant_id,
			       department, job_title, employment_type,
			       tc_kimlik_hash, hire_date, termination_date,
			       contact_info, emergency_contact,
			       status, notes, created_at, updated_at
			FROM employee_profiles WHERE tenant_id = $1
			ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
			tenantID, limit, offset)
	} else {
		rows, err = tx.Query(ctx, `
			SELECT id, person_id, tenant_id,
			       department, job_title, employment_type,
			       tc_kimlik_hash, hire_date, termination_date,
			       contact_info, emergency_contact,
			       status, notes, created_at, updated_at
			FROM employee_profiles WHERE tenant_id = $1 AND status = $2
			ORDER BY created_at DESC LIMIT $3 OFFSET $4`,
			tenantID, string(status), limit, offset)
	}
	if err != nil {
		return nil, fmt.Errorf("hr-core/repo: list: %w", err)
	}
	defer rows.Close()

	var employees []domain.Employee
	for rows.Next() {
		e, err := scanEmployee(rows)
		if err != nil {
			return nil, err
		}
		employees = append(employees, e)
	}
	return employees, rows.Err()
}

func scanEmployee(row interface{ Scan(dest ...any) error }) (domain.Employee, error) {
	var e domain.Employee
	var contactJSON, emergencyJSON []byte
	var employmentType, status string

	err := row.Scan(
		&e.ID, &e.PersonID, &e.TenantID,
		&e.Department, &e.JobTitle, &employmentType,
		&e.TCKimlikHash, &e.HireDate, &e.TerminationDate,
		&contactJSON, &emergencyJSON,
		&status, &e.Notes, &e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return domain.Employee{}, fmt.Errorf("hr-core/repo: scan: %w", err)
	}

	e.EmploymentType = domain.EmploymentType(employmentType)
	e.Status = domain.EmployeeStatus(status)

	if err := json.Unmarshal(contactJSON, &e.ContactInfo); err != nil {
		return domain.Employee{}, fmt.Errorf("hr-core/repo: unmarshal contact_info: %w", err)
	}
	if err := json.Unmarshal(emergencyJSON, &e.EmergencyContact); err != nil {
		return domain.Employee{}, fmt.Errorf("hr-core/repo: unmarshal emergency_contact: %w", err)
	}
	return e, nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "23505") || strings.Contains(msg, "unique_violation") || strings.Contains(msg, "duplicate key")
}
