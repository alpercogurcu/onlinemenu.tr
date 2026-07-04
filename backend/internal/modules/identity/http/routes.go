package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	pub "onlinemenu.tr/internal/modules/identity/public"
	"onlinemenu.tr/internal/modules/identity/service"
	"onlinemenu.tr/internal/platform/auth"
)

const maxBodyBytes = 1 << 20

type Handler struct {
	persons     *service.PersonService
	roles       *service.RoleService
	memberships *service.MembershipService
	contexts    *service.ContextService
	logger      *zap.Logger
	engine      *auth.Engine
}

func NewHandler(
	persons *service.PersonService,
	roles *service.RoleService,
	memberships *service.MembershipService,
	contexts *service.ContextService,
	logger *zap.Logger,
	engine *auth.Engine,
) *Handler {
	return &Handler{
		persons:     persons,
		roles:       roles,
		memberships: memberships,
		contexts:    contexts,
		logger:      logger,
		engine:      engine,
	}
}

// permit builds per-route OPA authorization middleware (ADR-AUTH-001, layer 2).
func (h *Handler) permit(action string) func(http.Handler) http.Handler {
	return auth.RequirePermission(h.engine, action)
}

// RegisterRoutes mounts identity endpoints on the provided router.
//
// /me, /me/contexts and /auth/context are the pre-context flow (ADR-AUTH-001
// step 1-3): the caller only holds a Keycloak-verified Principal with no
// TenantID/RoleIDs yet, so OPA authorization does not apply — authentication
// alone (the global auth.Middleware) gates these three routes. Every other
// route requires a resolved context token and is behind RequirePermission.
func (h *Handler) RegisterRoutes(r *chi.Mux) {
	r.Route("/v1/identity", func(r chi.Router) {
		r.Get("/me", h.GetMe)
		r.Get("/me/contexts", h.ListContexts)
		r.Post("/auth/context", h.SelectContext)

		// TODO(platform-admin): restrict to platform-admin role before enabling in production.
		// Disabled until a dedicated platform-admin path with proper authz is implemented.
		// r.Get("/persons/{personID}", h.GetPerson)
		// r.Post("/persons", h.CreatePerson)

		r.Route("/{tenantID}", func(r chi.Router) {
			r.Use(h.tenantAccessMiddleware)

			r.With(h.permit("identity.role.read")).Get("/roles", h.ListRoles)
			r.With(h.permit("identity.role.create")).Post("/roles", h.CreateRole)
			r.With(h.permit("identity.role.delete")).Delete("/roles/{roleID}", h.DeleteRole)

			r.With(h.permit("identity.membership.read")).Get("/memberships", h.ListMemberships)
			r.With(h.permit("identity.membership.create")).Post("/memberships", h.CreateMembership)
			r.With(h.permit("identity.membership.update")).Put("/memberships/{membershipID}", h.UpdateMembershipStatus)
		})
	})
}

func (h *Handler) tenantAccessMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID, err := pathUUID(r, "tenantID")
		if err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid tenant id")
			return
		}
		// Reject uuid.Nil in the path: it would activate the platform-admin RLS bypass.
		if tenantID == (uuid.UUID{}) {
			h.writeError(w, http.StatusBadRequest, "invalid tenant id")
			return
		}
		principal, err := auth.FromContext(r.Context())
		if err != nil {
			h.writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		// Only staff principals with a matching tenant context may access tenant-scoped routes.
		if !principal.IsStaff() || principal.TenantID != tenantID {
			h.writeError(w, http.StatusForbidden, "tenant access denied")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		h.logger.Warn("response encode failed", zap.Error(err))
	}
}

func (h *Handler) writeError(w http.ResponseWriter, status int, msg string) {
	h.writeJSON(w, status, map[string]string{"error": msg})
}

func (h *Handler) handleErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pub.ErrNotFound):
		h.writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, pub.ErrInvalid):
		h.writeError(w, http.StatusBadRequest, "invalid input")
	default:
		h.logger.Error("internal service error", zap.Error(err))
		h.writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func pathUUID(r *http.Request, param string) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, param))
}
