// Package codes defines the stable, machine-readable error codes returned by
// the WindowProof service. Codes are used across the domain, the persistence
// boundary and the HTTP API so that a rejected operation can be traced back to
// a single, deterministic reason regardless of where the failure was detected.
package codes

// Code is a stable error code returned to callers.
type Code string

const (
	// CatalogMismatch indicates a facade zone, window unit, orientation or
	// material batch did not match the frozen catalog rules.
	CatalogMismatch Code = "CATALOG_MISMATCH"
	// InvalidState indicates an operation was requested in a task state that
	// does not permit it.
	InvalidState Code = "INVALID_STATE"
	// InvalidPhase indicates an operation targeted a phase outside the current
	// ordered progression.
	InvalidPhase Code = "INVALID_PHASE"
	// StaleRevision indicates an expected_revision no longer matches the task.
	StaleRevision Code = "STALE_REVISION"
	// StaleGeneration indicates a callback or evidence belonged to a superseded
	// task generation.
	StaleGeneration Code = "STALE_GENERATION"
	// OccupancyConflict indicates a resource token could not be acquired because
	// another open task already holds it.
	OccupancyConflict Code = "OCCUPANCY_CONFLICT"
	// OperationContentConflict indicates an operation_id was replayed with a
	// different normalized request than the first recorded execution.
	OperationContentConflict Code = "OPERATION_CONTENT_CONFLICT"
	// ArithmeticError indicates a safe integer computation overflowed, divided
	// by zero, or was otherwise outside the representable range.
	ArithmeticError Code = "ARITHMETIC_ERROR"
	// InstrumentRejected indicates an external instrument refused the call.
	InstrumentRejected Code = "INSTRUMENT_REJECTED"
	// InstrumentTimeout indicates an external instrument did not answer in time.
	InstrumentTimeout Code = "INSTRUMENT_TIMEOUT"
	// InstrumentDisconnected indicates the connection to an external instrument
	// was dropped.
	InstrumentDisconnected Code = "INSTRUMENT_DISCONNECTED"
	// InstrumentMalformed indicates an external instrument returned an
	// unparsable payload.
	InstrumentMalformed Code = "INSTRUMENT_MALFORMED"
	// QualificationInvalid indicates a reviewer is not qualified under the
	// frozen qualification revision.
	QualificationInvalid Code = "QUALIFICATION_INVALID"
	// ReviewConflict indicates a natural person already occupies a review seat
	// or two reviews are not distinct.
	ReviewConflict Code = "REVIEW_CONFLICT"
	// VerdictMismatch indicates two reviews did not bind the same verdict hash.
	VerdictMismatch Code = "VERDICT_MISMATCH"
	// TerminalReached indicates a write operation was attempted after the task
	// reached a terminal state.
	TerminalReached Code = "TERMINAL_REACHED"
	// StoreUnavailable indicates the SQLite transaction could not commit.
	StoreUnavailable Code = "STORE_UNAVAILABLE"
	// RecoveryError indicates startup invariant validation failed.
	RecoveryError Code = "RECOVERY_ERROR"
	// InvalidRequest indicates a malformed or unnormalizable request.
	InvalidRequest Code = "INVALID_REQUEST"
)

// String implements fmt.Stringer.
func (c Code) String() string { return string(c) }
