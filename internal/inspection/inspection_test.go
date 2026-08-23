package inspection

import "testing"

func TestStateMachineHappyPath(t *testing.T) {
	path := []State{
		StatePendingLock,
		StateInstalling,
		StateAirLoading,
		StateWaterSpray,
		StateWindReview,
		StatePendingReview,
		StateReleasable,
		StateReleased,
	}
	for i := 0; i < len(path)-1; i++ {
		if !ValidateTransition(path[i], path[i+1]) {
			t.Fatalf("expected %s -> %s to be valid", path[i], path[i+1])
		}
	}
}

func TestTerminalStatesHaveNoOutgoingEdges(t *testing.T) {
	for _, terminal := range []State{StateReleased, StateQuarantined, StateCancelled} {
		if !terminal.IsTerminal() {
			t.Fatalf("%s should be terminal", terminal)
		}
		for _, to := range []State{StateInstalling, StateAirLoading, StatePendingReview, StateReleasable} {
			if ValidateTransition(terminal, to) {
				t.Fatalf("terminal %s must not transition to %s", terminal, to)
			}
		}
	}
}

func TestPhaseOrdering(t *testing.T) {
	if PhaseIndex(PhaseAir) != 0 || PhaseIndex(PhaseWater) != 1 || PhaseIndex(PhaseWind) != 2 {
		t.Fatalf("phase ordering incorrect")
	}
	if PhaseIndex("unknown") != -1 {
		t.Fatalf("unknown phase should index -1")
	}
}
