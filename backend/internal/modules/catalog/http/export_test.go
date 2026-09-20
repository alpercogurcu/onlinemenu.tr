package http

import (
	"net/http"

	"github.com/google/uuid"
)

// BranchIDFromQuery exposes the unexported query resolver so its branch
// defaulting can be exercised behind the real permission middleware without
// standing up the DB-backed services.
func (h *Handler) BranchIDFromQuery(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	return h.branchIDFromQuery(w, r)
}
