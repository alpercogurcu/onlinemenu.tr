// Package http is the storefront's transport layer. Its public half serves
// anonymous QR diners, so every response here is written on the assumption
// that the caller may be hostile.
package http

import (
	"encoding/json"
	"net/http"
)

// problemDetail is the RFC 7807 body every public endpoint answers errors
// with (ADR-OPS-003 uses the same shape for 429s, so a diner-facing client
// parses exactly one error format on this surface).
type problemDetail struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
	// Code is a stable machine-readable discriminator. Several conditions
	// share a status here (409 covers both "table busy" and "table being
	// cleaned"), and the human-readable Turkish text must stay changeable
	// without breaking clients that branch on it.
	Code string `json:"code,omitempty"`
}

// Stable error codes. The menu app branches on these, never on Detail.
const (
	codeNotFound       = "not_found"
	codeUnauthorized   = "unauthorized"
	codeInvalidRequest = "invalid_request"
	codeValidation     = "validation_failed"
	codeTableNotReady  = "table_not_ready"
	codeTableOccupied  = "table_occupied"
	codeTableMismatch  = "table_branch_mismatch"
	codeInternal       = "internal_error"
)

var problemTitles = map[int]string{
	http.StatusBadRequest:          "Bad Request",
	http.StatusUnauthorized:        "Unauthorized",
	http.StatusNotFound:            "Not Found",
	http.StatusConflict:            "Conflict",
	http.StatusUnprocessableEntity: "Unprocessable Entity",
	http.StatusInternalServerError: "Internal Server Error",
}

// writeProblem emits an RFC 7807 problem detail.
//
// Detail is always a diner-facing Turkish sentence and never carries the
// underlying error: on a surface with no authentication, an error string is a
// free description of the system's internals to anyone who can send a
// request. Server-side diagnosis happens through the logged error instead.
func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	title, ok := problemTitles[status]
	if !ok {
		title = http.StatusText(status)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problemDetail{
		Type:   "https://errors.onlinemenu.tr/" + code,
		Title:  title,
		Status: status,
		Detail: detail,
		Code:   code,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
