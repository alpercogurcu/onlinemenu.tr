// Package service implements hr-core business logic.
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/hr-core/domain"
	"onlinemenu.tr/internal/modules/hr-core/repo"
	"onlinemenu.tr/internal/platform/db"
)

// ErrNotFound is returned when an employee profile cannot be found.
var ErrNotFound = errors.New("hr-core/service: not found")

// ErrInvalidInput is returned when request validation fails.
var ErrInvalidInput = errors.New("hr-core/service: invalid input")

// ErrDuplicateEmployee is returned when a profile already exists for the person.
var ErrDuplicateEmployee = errors.New("hr-core/service: employee profile already exists")

// HRService orchestrates employee profile operations.
type HRService struct {
	db           *db.Pool
	employeeRepo *repo.EmployeeRepo
	logger       *zap.Logger
}

// Params groups fx-injected dependencies.
type Params struct {
	fx.In

	DB           *db.Pool
	EmployeeRepo *repo.EmployeeRepo
	Logger       *zap.Logger
}

// New constructs an HRService.
func New(p Params) *HRService {
	return &HRService{
		db:           p.DB,
		employeeRepo: p.EmployeeRepo,
		logger:       p.Logger,
	}
}

// CreateEmployee creates a new employee profile for a person within a tenant.
// The person must already exist in the identity module.
func (s *HRService) CreateEmployee(ctx context.Context, tenantID uuid.UUID, e domain.Employee) (domain.Employee, error) {
	if e.PersonID == uuid.Nil {
		return domain.Employee{}, fmt.Errorf("%w: person_id required", ErrInvalidInput)
	}
	if e.HireDate.IsZero() {
		return domain.Employee{}, fmt.Errorf("%w: hire_date required", ErrInvalidInput)
	}
	if e.EmploymentType != "" && !e.EmploymentType.Valid() {
		return domain.Employee{}, fmt.Errorf("%w: invalid employment_type %q", ErrInvalidInput, e.EmploymentType)
	}
	if e.EmploymentType == "" {
		e.EmploymentType = domain.EmploymentTypeFull
	}
	e.TenantID = tenantID
	e.Status = domain.EmployeeStatusActive

	var created domain.Employee
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var txErr error
		created, txErr = s.employeeRepo.Create(ctx, tx, e)
		return txErr
	})
	if errors.Is(err, repo.ErrDuplicateEmployee) {
		return domain.Employee{}, ErrDuplicateEmployee
	}
	return created, err
}

// GetEmployee returns an employee profile by ID.
func (s *HRService) GetEmployee(ctx context.Context, tenantID, employeeID uuid.UUID) (domain.Employee, error) {
	var e domain.Employee
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var txErr error
		e, txErr = s.employeeRepo.GetByID(ctx, tx, employeeID)
		return txErr
	})
	if errors.Is(err, repo.ErrNotFound) {
		return domain.Employee{}, ErrNotFound
	}
	return e, err
}

// GetEmployeeByPerson returns the profile for a specific person within a tenant.
func (s *HRService) GetEmployeeByPerson(ctx context.Context, tenantID, personID uuid.UUID) (domain.Employee, error) {
	var e domain.Employee
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var txErr error
		e, txErr = s.employeeRepo.GetByPersonID(ctx, tx, tenantID, personID)
		return txErr
	})
	if errors.Is(err, repo.ErrNotFound) {
		return domain.Employee{}, ErrNotFound
	}
	return e, err
}

// UpdateEmployee modifies mutable fields of an employee profile.
func (s *HRService) UpdateEmployee(ctx context.Context, tenantID uuid.UUID, e domain.Employee) (domain.Employee, error) {
	if !e.EmploymentType.Valid() {
		return domain.Employee{}, fmt.Errorf("%w: invalid employment_type %q", ErrInvalidInput, e.EmploymentType)
	}
	if !e.Status.Valid() {
		return domain.Employee{}, fmt.Errorf("%w: invalid status %q", ErrInvalidInput, e.Status)
	}
	e.TenantID = tenantID

	var updated domain.Employee
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var txErr error
		updated, txErr = s.employeeRepo.Update(ctx, tx, e)
		return txErr
	})
	if errors.Is(err, repo.ErrNotFound) {
		return domain.Employee{}, ErrNotFound
	}
	return updated, err
}

// TerminateEmployee marks an employee as terminated.
func (s *HRService) TerminateEmployee(ctx context.Context, tenantID, employeeID uuid.UUID, req TerminateRequest) (domain.Employee, error) {
	if req.TerminationDate.IsZero() {
		return domain.Employee{}, fmt.Errorf("%w: termination_date required", ErrInvalidInput)
	}

	var updated domain.Employee
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		e, txErr := s.employeeRepo.GetByID(ctx, tx, employeeID)
		if txErr != nil {
			return txErr
		}
		e.Status = domain.EmployeeStatusTerminated
		e.TerminationDate = &req.TerminationDate
		if req.Notes != "" {
			e.Notes = req.Notes
		}
		updated, txErr = s.employeeRepo.Update(ctx, tx, e)
		return txErr
	})
	if errors.Is(err, repo.ErrNotFound) {
		return domain.Employee{}, ErrNotFound
	}
	return updated, err
}

// ListEmployees returns employees for a tenant with optional status filter.
func (s *HRService) ListEmployees(ctx context.Context, tenantID uuid.UUID, status domain.EmployeeStatus, limit, offset int) ([]domain.Employee, error) {
	var employees []domain.Employee
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var txErr error
		employees, txErr = s.employeeRepo.List(ctx, tx, tenantID, status, limit, offset)
		return txErr
	})
	return employees, err
}
