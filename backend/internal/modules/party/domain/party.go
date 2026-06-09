// Package domain contains the party module's core types.
// A party is any external entity (supplier or customer) the tenant trades with.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// PartyType classifies the relationship direction.
type PartyType string

const (
	PartyTypeSupplier PartyType = "supplier"
	PartyTypeCustomer PartyType = "customer"
	PartyTypeBoth     PartyType = "both"
)

// Valid reports whether the party type is a recognised value.
func (t PartyType) Valid() bool {
	return t == PartyTypeSupplier || t == PartyTypeCustomer || t == PartyTypeBoth
}

// Party is the aggregate root for a trading partner.
type Party struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	PartyType PartyType
	Name      string
	ShortName string
	// Legal / GİB fields
	TaxNo     string
	TaxOffice string
	GibAlias  string // GİB posta kutusu alias for e-invoice
	// Contact
	Phone   string
	Email   string
	Website string
	// Address
	AddressLine string
	City        string
	District    string
	PostalCode  string
	// Financials
	PaymentTermsDays  int
	CreditLimitAmount int64 // kuruş
	Currency          string
	// Status
	IsActive  bool
	Notes     string
	CreatedAt time.Time
	UpdatedAt time.Time
	// Eager-loaded contacts
	Contacts []Contact
}

// Contact is an additional person associated with a party.
type Contact struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	PartyID   uuid.UUID
	Name      string
	Role      string
	Phone     string
	Email     string
	IsPrimary bool
	CreatedAt time.Time
}
