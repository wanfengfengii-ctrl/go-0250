// Package acquisition models the graded pressure and spray acquisition ledger:
// the ordered loading prefix, the spray coverage set, integer measurements,
// external instrument attempts and their retry state. Safe integer arithmetic is
// centralized here so that every derived conclusion is computed without silent
// overflow.
package acquisition

// Polarity is the sign of a pressure loading step.
type Polarity string

const (
	// PolarityPositive is a positive pressure level.
	PolarityPositive Polarity = "positive"
	// PolarityNegative is a negative pressure level.
	PolarityNegative Polarity = "negative"
)

// PressureStep is a frozen, ordered pressure loading level. Steps are uniquely
// ordered by phase, polarity and ordinal and must be completed as a
// non-skippable prefix.
type PressureStep struct {
	Phase                   string
	Polarity                Polarity
	Ordinal                 int
	TargetPa                int64
	TolerancePa             int64
	RequiredDurationSeconds int64
}

// StepRecord is a single submitted measurement against a pressure step.
type StepRecord struct {
	TaskID             string
	Generation         int64
	Phase              string
	Polarity           Polarity
	Ordinal            int
	ActualPa           int64
	AirflowCCPerSec    int64
	DisplacementMicron int64
	OperationID        string
	ContentHash        string
	Passed             bool
}

// SprayCheckpoint is a frozen spray checkpoint time window bound to a pressure
// ordinal.
type SprayCheckpoint struct {
	CheckpointID       string
	PressureOrdinal    int
	Zone               string
	StartOffsetSeconds int64
	EndOffsetSeconds   int64
}

// SensorStatus reports the instrument-side outcome of a spray observation.
type SensorStatus string

const (
	// SensorOK indicates a valid observation.
	SensorOK SensorStatus = "ok"
	// SensorRejected indicates the nozzle rejected the call.
	SensorRejected SensorStatus = "rejected"
	// SensorDisconnected indicates the nozzle connection dropped.
	SensorDisconnected SensorStatus = "disconnected"
	// SensorTimeout indicates the nozzle did not answer in time.
	SensorTimeout SensorStatus = "timeout"
	// SensorMalformed indicates the nozzle returned an unparsable payload.
	SensorMalformed SensorStatus = "malformed"
)

// SprayRecord is a single spray observation against a checkpoint.
type SprayRecord struct {
	TaskID       string
	Generation   int64
	CheckpointID string
	ActualPa     int64
	SensorStatus SensorStatus
	Observation  string
	Covered      bool
}

// AttemptStatus is the persisted lifecycle status of an external call.
type AttemptStatus string

const (
	// AttemptPending is an attempt registered but not yet executed.
	AttemptPending AttemptStatus = "pending"
	// AttemptSucceeded is an attempt that returned a valid payload.
	AttemptSucceeded AttemptStatus = "succeeded"
	// AttemptFailed is an attempt that returned a reject/timeout/disconnect or a
	// malformed payload and must be retried explicitly.
	AttemptFailed AttemptStatus = "failed"
)

// InstrumentAttempt is an auditable record of a single external instrument call.
type InstrumentAttempt struct {
	AttemptID       string
	OperationID     string
	TaskID          string
	Generation      int64
	Kind            string
	RequestHash     string
	RequestPayload  string
	Status          AttemptStatus
	FailureCode     string
	ResponsePayload string
}

// Failed reports whether the attempt ended in a retryable failure state.
func (a InstrumentAttempt) Failed() bool { return a.Status == AttemptFailed }
