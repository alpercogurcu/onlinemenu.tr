package domain

import (
	"time"

	"github.com/google/uuid"
)

// Person is the platform-level identity entity. A person exists independently
// of any tenant and may hold memberships in multiple chains and branches.
type Person struct {
	ID          uuid.UUID
	KeycloakSub string
	Email       string
	FullName    string
	Phone       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
