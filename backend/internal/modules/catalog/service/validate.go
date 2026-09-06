package service

import (
	"strings"

	pub "onlinemenu.tr/internal/modules/catalog/public"
)

// requireName trims surrounding whitespace from name and rejects it if the
// trimmed result is empty. Callers must persist the returned (trimmed) value
// rather than the original — trimming happens here so blank-looking names
// (e.g. "   ") can never reach storage.
func requireName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", &pub.ValidationError{Msg: "name is required"}
	}
	return trimmed, nil
}
