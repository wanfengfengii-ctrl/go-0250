package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/windowproof/fenestration/internal/codes"
	"github.com/windowproof/fenestration/internal/service"
)

// maxBodyBytes is the fixed request-body limit applied to every JSON endpoint.
const maxBodyBytes = 1 << 20 // 1 MiB

// decodeJSON strictly decodes a JSON request body, rejecting unknown fields,
// trailing data and oversized bodies.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var syntaxErr *json.SyntaxError
		var unmarshalErr *json.UnmarshalTypeError
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			writeError(w, http.StatusRequestEntityTooLarge, errorBody(codes.InvalidRequest, "", 0))
		case errors.As(err, &syntaxErr), errors.As(err, &unmarshalErr):
			writeError(w, http.StatusBadRequest, errorBody(codes.InvalidRequest, "", 0))
		default:
			writeError(w, http.StatusBadRequest, errorBody(codes.InvalidRequest, "", 0))
		}
		return false
	}
	// Reject trailing data after the single JSON value.
	if dec.More() {
		writeError(w, http.StatusBadRequest, errorBody(codes.InvalidRequest, "", 0))
		return false
	}
	return true
}

// writeResult writes a service result or error as a JSON response.
func writeResult(w http.ResponseWriter, body []byte, err error) {
	if err != nil {
		var se *service.ServiceError
		if errors.As(err, &se) {
			writeError(w, httpStatusFor(se.Code), errorBody(se.Code, se.TaskID, se.StateRevision, se.Reasons...))
			return
		}
		writeError(w, http.StatusInternalServerError, errorBody(codes.StoreUnavailable, "", 0))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	_, _ = w.Write([]byte("\n"))
}

func errorBody(code codes.Code, taskID string, rev int64, reasons ...service.Reason) ErrorBody {
	body := ErrorBody{Code: code, TaskID: taskID, StateRevision: rev, Reasons: []Reason{}}
	for _, r := range reasons {
		body.Reasons = append(body.Reasons, Reason{Code: r.Code, Field: r.Field})
	}
	return body
}

func httpStatusFor(code codes.Code) int {
	switch code {
	case codes.InvalidRequest:
		return http.StatusBadRequest
	case codes.CatalogMismatch, codes.ArithmeticError, codes.QualificationInvalid, codes.VerdictMismatch:
		return http.StatusUnprocessableEntity
	case codes.InvalidState, codes.InvalidPhase, codes.StaleRevision, codes.StaleGeneration,
		codes.OccupancyConflict, codes.OperationContentConflict, codes.ReviewConflict, codes.TerminalReached:
		return http.StatusConflict
	case codes.InstrumentRejected, codes.InstrumentTimeout, codes.InstrumentDisconnected, codes.InstrumentMalformed:
		return http.StatusBadGateway
	case codes.StoreUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

var _ = io.EOF
