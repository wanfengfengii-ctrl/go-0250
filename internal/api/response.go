// Package api defines the HTTP JSON surface of WindowProof: strict request
// decoding, stable error responses, and the route set required by the
// documented component traceability. The concrete handlers that mutate domain
// state are filled in by a later session against the store.Store boundary.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/windowproof/fenestration/internal/codes"
)

// Reason is a single, stable, sortable rejection reason.
type Reason struct {
	Code  codes.Code `json:"code"`
	Field string     `json:"field,omitempty"`
}

// ErrorBody is the stable error response shape returned for every rejection.
type ErrorBody struct {
	Code          codes.Code `json:"code"`
	TaskID        string     `json:"task_id,omitempty"`
	StateRevision int64      `json:"state_revision,omitempty"`
	Reasons       []Reason   `json:"reasons"`
}

// writeError writes an ErrorBody with the given HTTP status and an empty
// (non-nil) reasons array to keep JSON output deterministic.
func writeError(w http.ResponseWriter, status int, body ErrorBody) {
	if body.Reasons == nil {
		body.Reasons = []Reason{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
