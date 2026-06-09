// Package identity manages platform persons and their branch-level role assignments.
// All persistence goes through platform/db.WithTenantTx; direct pool access is forbidden.
package identity

import (
	"context"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/fx"

	"onlinemenu.tr/internal/modules/identity/events"
	identityhttp "onlinemenu.tr/internal/modules/identity/http"
	pub "onlinemenu.tr/internal/modules/identity/public"
	"onlinemenu.tr/internal/modules/identity/repo"
	"onlinemenu.tr/internal/modules/identity/service"
)

// Module is the fx module definition for the identity domain.
var Module = fx.Module("identity",
	fx.Provide(
		repo.NewPersonRepo,
		repo.NewRoleRepo,
		repo.NewMembershipRepo,
		repo.NewPermissionRepo,
		service.NewPersonService,
		service.NewRoleService,
		service.NewMembershipService,
		service.NewContextService,
		events.NewSubscriber,
		identityhttp.NewHandler,
		// Adapters expose the services through the public interfaces consumed by other modules.
		fx.Annotate(newPersonReader, fx.As(new(pub.PersonReader))),
		fx.Annotate(newMembershipResolver, fx.As(new(pub.MembershipResolver))),
	),
	fx.Invoke(func(h *identityhttp.Handler, r *chi.Mux) {
		h.RegisterRoutes(r)
	}),
	fx.Invoke(func(lc fx.Lifecycle, s *events.Subscriber) {
		lc.Append(fx.Hook{
			OnStart: func(ctx context.Context) error {
				return s.Register()
			},
		})
	}),
)

// personReaderAdapter projects domain.Person values to pub.Person, satisfying pub.PersonReader.
type personReaderAdapter struct{ svc *service.PersonService }

func newPersonReader(svc *service.PersonService) *personReaderAdapter {
	return &personReaderAdapter{svc: svc}
}

func (a *personReaderAdapter) GetByID(ctx context.Context, personID uuid.UUID) (pub.Person, error) {
	p, err := a.svc.GetByID(ctx, personID)
	if err != nil {
		return pub.Person{}, err
	}
	return pub.Person{ID: p.ID, Email: p.Email, FullName: p.FullName}, nil
}

func (a *personReaderAdapter) GetByKeycloakSub(ctx context.Context, sub string) (pub.Person, error) {
	p, err := a.svc.GetByKeycloakSub(ctx, sub)
	if err != nil {
		return pub.Person{}, err
	}
	return pub.Person{ID: p.ID, Email: p.Email, FullName: p.FullName}, nil
}

// membershipResolverAdapter delegates to MembershipService, satisfying pub.MembershipResolver.
type membershipResolverAdapter struct{ svc *service.MembershipService }

func newMembershipResolver(svc *service.MembershipService) *membershipResolverAdapter {
	return &membershipResolverAdapter{svc: svc}
}

func (a *membershipResolverAdapter) ActiveRoleIDsAt(ctx context.Context, tenantID, personID, branchID uuid.UUID) ([]uuid.UUID, error) {
	return a.svc.ActiveRoleIDsAt(ctx, tenantID, personID, branchID)
}
