// Package service implements the WindowProof application layer. It owns the
// command handlers that turn raw requests into durable domain state, composing
// validation, safe arithmetic, occupancy, acquisition, evidence and terminal
// arbitration inside a single SQLite transaction. Every write command is
// idempotent by operation_id and returns a deterministic response that survives
// a process restart.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/windowproof/fenestration/internal/catalog"
	"github.com/windowproof/fenestration/internal/codes"
	"github.com/windowproof/fenestration/internal/inspection"
	"github.com/windowproof/fenestration/internal/store"
)

// Clock supplies monotonic ticks used to anchor spray checkpoint time windows.
// The production clock is wall time; tests inject a fake monotonic clock.
type Clock interface {
	NowMillis() int64
}

// WallClock is the production clock.
type WallClock struct{}

// NowMillis returns the current wall-clock time in milliseconds.
func (WallClock) NowMillis() int64 { return time.Now().UnixMilli() }

// Instrument executes external instrument calls (pressure sensors and spray
// nozzles) after their intent has been persisted. A controllable adapter lets
// tests script reject/disconnect/timeout/malformed/success sequences.
type Instrument interface {
	Call(ctx context.Context, kind string) (Result, error)
}

// Result is a single instrument-call outcome.
type Result struct {
	Status  string
	Payload string
}

// Reason is a single stable rejection reason.
type Reason struct {
	Code  codes.Code `json:"code"`
	Field string     `json:"field,omitempty"`
	Value string     `json:"value,omitempty"`
}

// ServiceError is a domain rejection carrying a stable code and a sorted reason
// list. It is also the JSON error body returned by the HTTP API.
type ServiceError struct {
	Code          codes.Code `json:"code"`
	TaskID        string     `json:"task_id,omitempty"`
	StateRevision int64      `json:"state_revision,omitempty"`
	Reasons       []Reason   `json:"reasons"`
}

func (e *ServiceError) Error() string { return string(e.Code) }

// Err returns a bare ServiceError with the given code.
func Err(code codes.Code) *ServiceError { return &ServiceError{Code: code, Reasons: []Reason{}} }

// WithReason appends a reason and returns the error.
func (e *ServiceError) WithReason(code codes.Code, field, value string) *ServiceError {
	e.Reasons = append(e.Reasons, Reason{Code: code, Field: field, Value: value})
	return e
}

// Service is the application facade.
type Service struct {
	store      store.Store
	clock      Clock
	instrument Instrument
	catalogMu  sync.RWMutex
	catalog    map[string]catalog.CatalogRevision
}

// New constructs a Service.
func New(st store.Store, clock Clock, instrument Instrument) *Service {
	if clock == nil {
		clock = WallClock{}
	}
	if instrument == nil {
		instrument = StaticInstrument{}
	}
	return &Service{
		store:      st,
		clock:      clock,
		instrument: instrument,
		catalog:    map[string]catalog.CatalogRevision{},
	}
}

// Store exposes the persistence boundary for tests and the recovery CLI.
func (s *Service) Store() store.Store { return s.store }

// LoadCatalogRevision returns a cached (read-only) catalog revision, loading and
// caching it on first use.
func (s *Service) LoadCatalogRevision(ctx context.Context, revisionID string) (catalog.CatalogRevision, bool, error) {
	s.catalogMu.RLock()
	if rev, ok := s.catalog[revisionID]; ok {
		s.catalogMu.RUnlock()
		return rev, true, nil
	}
	s.catalogMu.RUnlock()

	rev, ok, err := s.store.LoadCatalog(ctx, revisionID)
	if err != nil || !ok {
		return rev, ok, err
	}
	s.catalogMu.Lock()
	s.catalog[revisionID] = rev
	s.catalogMu.Unlock()
	return rev, true, nil
}

// SeedCatalog stores a catalog revision (used by the CLI and tests).
func (s *Service) SeedCatalog(ctx context.Context, rev catalog.CatalogRevision) error {
	return s.store.Write(ctx, func(tx store.Tx) error {
		return tx.SaveCatalog(ctx, rev)
	})
}

// outcome is the final result of a write command inside its transaction.
type outcome struct {
	payload []byte        // serialized success response or serialized error body
	serr    *ServiceError // non-nil when the command was rejected
}

// okCode is the response code recorded for a persisted success. Any other code
// marks a persisted rejection whose replay must rebuild the original error.
const okCode codes.Code = "OK"

// run executes a write command with operation-level idempotency. It returns the
// serialized JSON response on success or a *ServiceError on rejection.
func (s *Service) run(ctx context.Context, opID string, req any, fn func(tx store.Tx) (*outcome, error)) ([]byte, error) {
	hash := HashRequest(req)

	// Fast-path idempotency check before taking the write transaction.
	if stored, ok, err := s.store.LoadOperationResult(ctx, opID); err == nil && ok {
		if stored.RequestHash == hash {
			payload, serr := replayResult(stored)
			if serr != nil {
				return nil, serr
			}
			return payload, nil
		}
		return nil, Err(codes.OperationContentConflict)
	}

	var out []byte
	var outErr *ServiceError
	err := s.store.Write(ctx, func(tx store.Tx) error {
		stored, replayed, err := tx.LoadOperationResult(ctx, opID)
		if err != nil {
			return err
		}
		if replayed {
			if stored.RequestHash == hash {
				rpayload, rerr := replayResult(stored)
				out = rpayload
				outErr = rerr
				return nil
			}
			outErr = Err(codes.OperationContentConflict)
			return nil
		}

		oc, err := fn(tx)
		if err != nil {
			return err
		}
		var payload []byte
		code := okCode
		if oc.serr != nil {
			payload, _ = json.Marshal(oc.serr)
			code = oc.serr.Code
			outErr = oc.serr
		} else {
			payload = oc.payload
		}
		if saveErr := tx.SaveOperationResult(ctx, store.OperationResult{
			OperationID:  opID,
			RequestHash:  hash,
			ResponseCode: string(code),
			Payload:      payload,
		}); saveErr != nil {
			return saveErr
		}
		if oc.serr == nil {
			out = payload
		}
		return nil
	})
	if err != nil {
		return nil, Err(codes.StoreUnavailable)
	}
	if outErr != nil {
		return nil, outErr
	}
	return out, nil
}

// replayResult reconstructs the response for an idempotent replay of an
// operation whose result is already persisted. A recorded success returns its
// payload unchanged; a recorded rejection is rebuilt into the original
// *ServiceError so the replay keeps the first attempt's error code and state
// instead of being mistaken for a success.
func replayResult(stored store.OperationResult) ([]byte, *ServiceError) {
	if stored.ResponseCode == string(okCode) {
		return append([]byte(nil), stored.Payload...), nil
	}
	var serr ServiceError
	if err := json.Unmarshal(stored.Payload, &serr); err != nil {
		// A well-formed persisted rejection always decodes; fall back to the
		// recorded code so a corrupt payload can never replay as a success.
		return nil, Err(codes.Code(stored.ResponseCode))
	}
	if serr.Code == "" {
		serr.Code = codes.Code(stored.ResponseCode)
	}
	if serr.Reasons == nil {
		serr.Reasons = []Reason{}
	}
	return nil, &serr
}

// HashRequest returns the deterministic normalized digest of a request value.
func HashRequest(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// SortReasons orders reasons by the fixed stable key required by the domain:
// window-unit, pressure-step, measurement-point, checkpoint, then error code.
func SortReasons(reasons []Reason) {
	rank := func(f string) int {
		switch f {
		case "window_unit":
			return 0
		case "pressure_step":
			return 1
		case "measurement_point":
			return 2
		case "checkpoint":
			return 3
		default:
			return 4
		}
	}
	sort.Slice(reasons, func(i, j int) bool {
		if rank(reasons[i].Field) != rank(reasons[j].Field) {
			return rank(reasons[i].Field) < rank(reasons[j].Field)
		}
		if reasons[i].Value != reasons[j].Value {
			return reasons[i].Value < reasons[j].Value
		}
		return reasons[i].Code < reasons[j].Code
	})
}

// terminalError is returned when a write is attempted against a terminal task.
func terminalError(task inspection.InspectionTask) *ServiceError {
	e := Err(codes.TerminalReached)
	e.TaskID = task.TaskID
	e.StateRevision = task.StateRevision
	return e
}
