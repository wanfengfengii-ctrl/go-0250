package service

import (
	"context"

	"github.com/windowproof/fenestration/internal/acquisition"
	"github.com/windowproof/fenestration/internal/catalog"
	"github.com/windowproof/fenestration/internal/codes"
	"github.com/windowproof/fenestration/internal/inspection"
	"github.com/windowproof/fenestration/internal/store"
)

// PressureStepRequest submits one graded air or wind pressure level.
type PressureStepRequest struct {
	TaskID             string `json:"task_id"`
	OperationID        string `json:"operation_id"`
	ExpectedRevision   int64  `json:"expected_revision"`
	Phase              string `json:"phase"`
	Polarity           string `json:"polarity"`
	Ordinal            int    `json:"ordinal"`
	ActualPa           int64  `json:"actual_pa"`
	AirflowCCPerSec    int64  `json:"airflow_cc_per_sec,omitempty"`
	DisplacementMicron int64  `json:"displacement_micron,omitempty"`
}

// PressureStepResponse reports the recorded and judged level.
type PressureStepResponse struct {
	TaskID          string `json:"task_id"`
	Generation      int64  `json:"generation"`
	State           string `json:"state"`
	StateRevision   int64  `json:"state_revision"`
	Phase           string `json:"phase"`
	Polarity        string `json:"polarity"`
	Ordinal         int    `json:"ordinal"`
	Passed          bool   `json:"passed"`
	FlowLitresPerHr int64  `json:"flow_litres_per_hour,omitempty"`
}

// phaseForState maps a task state to the pressure phase it accepts.
func phaseForState(state inspection.State) inspection.Phase {
	switch state {
	case inspection.StateAirLoading:
		return inspection.PhaseAir
	case inspection.StateWindReview:
		return inspection.PhaseWind
	default:
		return ""
	}
}

// SubmitPressureStep validates the non-skippable prefix, executes the instrument
// call, performs safe integer judgment and, only on success, records the level
// and advances the phase cursor.
func (s *Service) SubmitPressureStep(ctx context.Context, in PressureStepRequest) ([]byte, error) {
	return s.run(ctx, in.OperationID, in, func(tx store.Tx) (*outcome, error) {
		task, ok, err := tx.LoadTask(ctx, in.TaskID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "task_id", in.TaskID))
		}
		if task.State.IsTerminal() {
			return rejected(terminalError(task))
		}
		wantPhase := phaseForState(task.State)
		if wantPhase == "" || string(wantPhase) != in.Phase {
			return rejected(Err(codes.InvalidPhase))
		}
		if in.ExpectedRevision != task.StateRevision {
			return rejected(Err(codes.StaleRevision))
		}

		plan, ok, err := tx.LoadCatalog(ctx, task.LockedCatalogRevision)
		if err != nil {
			return nil, err
		}
		if !ok {
			return rejected(Err(codes.CatalogMismatch))
		}
		tp, ok := plan.PlanByID(task.LockedPlanID)
		if !ok {
			return rejected(Err(codes.CatalogMismatch))
		}
		steps := tp.PressureStepsFor(in.Phase)

		// Locate the target step and validate the non-skippable prefix.
		var target *catalog.PressureStepSpec
		for i := range steps {
			if steps[i].Polarity == in.Polarity && steps[i].Ordinal == in.Ordinal {
				target = &steps[i]
				break
			}
		}
		if target == nil {
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "pressure_step", stepKey(in.Phase, in.Polarity, in.Ordinal)))
		}

		completed, err := tx.LoadStepRecords(ctx, in.TaskID, int64(task.Generation))
		if err != nil {
			return nil, err
		}
		cursor := 0
		for _, r := range completed {
			if r.Phase == in.Phase && r.Passed {
				cursor++
			}
		}
		if cursor >= len(steps) {
			return rejected(Err(codes.InvalidPhase))
		}
		next := steps[cursor]
		if next.Polarity != in.Polarity || next.Ordinal != in.Ordinal {
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "pressure_step", stepKey(in.Phase, in.Polarity, in.Ordinal)))
		}

		// Execute the external instrument call: persist intent, call, persist result.
		attempt := acquisition.InstrumentAttempt{
			AttemptID:      newAttemptID(),
			OperationID:    in.OperationID,
			TaskID:         in.TaskID,
			Generation:     int64(task.Generation),
			Kind:           "pressure",
			RequestHash:    HashRequest(in),
			RequestPayload: string(mustJSON(in)),
			Status:         acquisition.AttemptPending,
		}
		if err := tx.SaveAttempt(ctx, attempt); err != nil {
			return nil, err
		}
		res, callErr := s.instrument.Call(ctx, "pressure")
		if callErr != nil {
			attempt.Status = acquisition.AttemptFailed
			attempt.FailureCode = string(codes.InstrumentDisconnected)
			_ = tx.SaveAttempt(ctx, attempt)
			return rejected(Err(codes.InstrumentDisconnected))
		}
		if res.Status != "ok" {
			attempt.Status = acquisition.AttemptFailed
			attempt.FailureCode = instrumentCode(res.Status)
			attempt.ResponsePayload = res.Payload
			_ = tx.SaveAttempt(ctx, attempt)
			e := Err(codes.Code(instrumentCode(res.Status)))
			e.TaskID = task.TaskID
			e.StateRevision = task.StateRevision
			return rejected(e)
		}
		attempt.Status = acquisition.AttemptSucceeded
		attempt.ResponsePayload = res.Payload
		if err := tx.SaveAttempt(ctx, attempt); err != nil {
			return nil, err
		}

		// Safe integer judgment. Any arithmetic failure must not write a
		// conclusion or advance state.
		passed, flow, err := judgePressure(*target, tp, in)
		if err != nil {
			return rejected(Err(codes.ArithmeticError))
		}
		// A reading outside the locked tolerance is a failed level. Like the
		// spray tolerance check, it is rejected before any step record is
		// written and before the phase fence can advance: a non-passing level
		// must never open the next level or reach the terminal. Persisting it
		// would also pin the step (step records are immutable by key), so the
		// operator must resubmit a compliant reading under a new operation id.
		if !passed {
			e := Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "actual_pa", itoa(int(in.ActualPa)))
			e.TaskID = task.TaskID
			e.StateRevision = task.StateRevision
			return rejected(e)
		}

		rec := acquisition.StepRecord{
			TaskID:             in.TaskID,
			Generation:         int64(task.Generation),
			Phase:              in.Phase,
			Polarity:           acquisition.Polarity(in.Polarity),
			Ordinal:            in.Ordinal,
			ActualPa:           in.ActualPa,
			AirflowCCPerSec:    in.AirflowCCPerSec,
			DisplacementMicron: in.DisplacementMicron,
			OperationID:        in.OperationID,
			ContentHash:        HashRequest(in),
			Passed:             passed,
		}
		if err := tx.SaveStepRecord(ctx, rec); err != nil {
			return nil, err
		}

		// Advance the phase when this level completes the prefix.
		if cursor+1 == len(steps) {
			if err := s.advancePhase(ctx, tx, &task); err != nil {
				return nil, err
			}
		}
		task.StateRevision++
		task.UpdatedAt = s.clock.NowMillis()
		if err := tx.SaveTask(ctx, task); err != nil {
			return nil, err
		}

		payload := mustJSON(PressureStepResponse{
			TaskID: task.TaskID, Generation: int64(task.Generation), State: string(task.State),
			StateRevision: task.StateRevision, Phase: in.Phase, Polarity: in.Polarity,
			Ordinal: in.Ordinal, Passed: passed, FlowLitresPerHr: flow,
		})
		return &outcome{payload: payload}, nil
	})
}

// advancePhase moves the task to the next phase when a prefix completes, and to
// pending-review when the wind prefix completes with no open defects.
func (s *Service) advancePhase(ctx context.Context, tx store.Tx, task *inspection.InspectionTask) error {
	switch task.State {
	case inspection.StateAirLoading:
		task.State = inspection.StateWaterSpray
		task.CurrentPhase = inspection.PhaseWater
		task.SprayStartedAt = s.clock.NowMillis()
	case inspection.StateWindReview:
		open, err := s.openDefects(ctx, tx, task.TaskID, int64(task.Generation))
		if err != nil {
			return err
		}
		if !open {
			task.State = inspection.StatePendingReview
			task.CurrentPhase = inspection.PhaseWind
		}
	}
	return nil
}

// judgePressure applies the safe integer tolerance rules for air and wind.
func judgePressure(target catalog.PressureStepSpec, tp catalog.TestPlan, in PressureStepRequest) (passed bool, flow int64, err error) {
	delta, err := acquisition.SafeSub(in.ActualPa, target.TargetPa)
	if err != nil {
		return false, 0, err
	}
	if delta < 0 {
		delta = -delta
	}
	within, err := acquisition.SafeSub(target.TolerancePa, delta)
	if err != nil {
		return false, 0, err
	}
	if within < 0 {
		return false, 0, nil
	}

	switch in.Phase {
	case string(inspection.PhaseAir):
		// Convert cc/s to litres/hour using half-away-from-zero rounding.
		flow, err = acquisition.ScaleNonNegative(in.AirflowCCPerSec, 3600, 1000)
		if err != nil {
			return false, 0, err
		}
		return true, flow, nil
	case string(inspection.PhaseWind):
		remain, err := acquisition.SafeSub(tp.DisplacementThresholdMicrons, in.DisplacementMicron)
		if err != nil {
			return false, 0, err
		}
		if remain < 0 {
			return false, 0, nil
		}
		return true, 0, nil
	default:
		return false, 0, nil
	}
}

// instrumentCode maps an instrument status to its stable error code.
func instrumentCode(status string) string {
	switch status {
	case "rejected":
		return string(codes.InstrumentRejected)
	case "disconnected":
		return string(codes.InstrumentDisconnected)
	case "timeout":
		return string(codes.InstrumentTimeout)
	case "malformed":
		return string(codes.InstrumentMalformed)
	default:
		return string(codes.InstrumentRejected)
	}
}

func stepKey(phase, polarity string, ordinal int) string {
	return phase + "/" + polarity + "/" + itoa(ordinal)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

var attemptCounter uint64

func newAttemptID() string {
	attemptCounter++
	return "attempt-" + itoa(int(attemptCounter))
}
