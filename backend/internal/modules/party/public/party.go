// Package public exposes the party module's contract to other modules.
package public

import (
	"context"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/party/domain"
)

// PartyReader allows other modules (e.g. billing, inventory) to look up party data.
type PartyReader interface {
	GetParty(ctx context.Context, tenantID, partyID uuid.UUID) (domain.Party, error)
}
