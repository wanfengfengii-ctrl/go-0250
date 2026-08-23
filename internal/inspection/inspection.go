// Package inspection models the joint-inspection task aggregate: the ten-state
// lifecycle, the non-reusable task generation, the locked snapshot summary, the
// phase guard and the terminal closure. State transitions are owned exclusively
// by the aggregate root; external callers validate their intent against the
// state machine exposed here.
package inspection

import "github.com/windowproof/fenestration/internal/codes"

// State is one of the ten lifecycle states of an inspection task.
type State string

const (
	// StatePendingLock is the initial state before a catalogue snapshot is locked.
	StatePendingLock State = "pending_lock"
	// StateInstalling is the state while the specimen is being sealed into a chamber.
	StateInstalling State = "installing"
	// StateAirLoading is the airtightness pressure-loading state.
	StateAirLoading State = "air_loading"
	// StateWaterSpray is the water-tightness timed spray state.
	StateWaterSpray State = "water_spray"
	// StateWindReview is the wind-pressure deflection/recovery review state.
	StateWindReview State = "wind_review"
	// StatePendingReview is the state awaiting two independent reviews.
	StatePendingReview State = "pending_review"
	// StateReleasable is the state where all obligations are met and release is allowed.
	StateReleasable State = "releasable"
	// StateReleased is the terminal released state.
	StateReleased State = "released"
	// StateQuarantined is the terminal repair-quarantine state.
	StateQuarantined State = "quarantined"
	// StateCancelled is the terminal cancelled state.
	StateCancelled State = "cancelled"
)

// Phase is a distinct experimental obligation block within the ordered
// progression.
type Phase string

const (
	// PhaseAir is the airtightness pressure phase.
	PhaseAir Phase = "air"
	// PhaseWater is the water-tightness spray phase.
	PhaseWater Phase = "water"
	// PhaseWind is the wind-pressure review phase.
	PhaseWind Phase = "wind"
)

// TerminalKind discriminates the three mutually exclusive terminal outcomes.
type TerminalKind string

const (
	// TerminalReleased marks a successfully released task.
	TerminalReleased TerminalKind = "released"
	// TerminalQuarantined marks a task isolated for repair.
	TerminalQuarantined TerminalKind = "quarantined"
	// TerminalCancelled marks a cancelled task.
	TerminalCancelled TerminalKind = "cancelled"
)

// Generation is a non-reusable task generation. Repairs increment it and reopen
// only the affected obligations.
type Generation int64

// InstallationConfirmation records the installation/sealing confirmations that
// must all agree before loading may begin.
type InstallationConfirmation struct {
	OrientationConfirmed bool
	SealedConfirmed      bool
	PointsZeroed         bool
}

// InspectionTask is the aggregate root.
type InspectionTask struct {
	TaskID                string
	Generation            Generation
	State                 State
	StateRevision         int64
	LockedCatalogRevision string
	LockedSnapshotHash    string
	// LockedPlanID is the selected test plan within the locked catalog revision.
	LockedPlanID string
	// WindowUnitID is the installed specimen selected at lock time.
	WindowUnitID string
	// ChamberID is the test chamber bound at lock time.
	ChamberID string
	// MeasurementPointIDs are the displacement points bound at lock time.
	MeasurementPointIDs []string
	// SprayStartedAt is the monotonic tick at which the water-spray phase began;
	// it anchors the checkpoint time windows.
	SprayStartedAt int64
	InstallationConfirmation
	CurrentPhase Phase
	TerminalKind TerminalKind
	CreatedAt    int64
	UpdatedAt    int64
}

// IsTerminal reports whether the task is in a terminal state with no outgoing
// transitions.
func (s State) IsTerminal() bool {
	switch s {
	case StateReleased, StateQuarantined, StateCancelled:
		return true
	default:
		return false
	}
}

// OrderedPhases is the fixed progression of the three experimental phases.
var OrderedPhases = []Phase{PhaseAir, PhaseWater, PhaseWind}

// PhaseIndex returns the position of p in OrderedPhases, or -1 when unknown.
func PhaseIndex(p Phase) int {
	for i, q := range OrderedPhases {
		if q == p {
			return i
		}
	}
	return -1
}

// ValidateTransition reports whether moving from -> to is permitted by the
// state machine. Terminal states have no outgoing edges.
func ValidateTransition(from, to State) bool {
	if from.IsTerminal() {
		return false
	}
	switch from {
	case StatePendingLock:
		return to == StateInstalling
	case StateInstalling:
		return to == StateAirLoading
	case StateAirLoading:
		return to == StateWaterSpray
	case StateWaterSpray:
		return to == StateWindReview
	case StateWindReview:
		return to == StatePendingReview
	case StatePendingReview:
		return to == StateReleasable || to == StateQuarantined || to == StateCancelled
	case StateReleasable:
		return to == StateReleased || to == StateQuarantined || to == StateCancelled
	default:
		return false
	}
}

// StateError describes a state-guard rejection with a stable code.
type StateError struct {
	Code codes.Code
	From State
	To   State
}

func (e *StateError) Error() string {
	return "invalid transition " + string(e.From) + " -> " + string(e.To)
}
