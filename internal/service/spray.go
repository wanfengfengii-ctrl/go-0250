package service

import (
	"context"

	"github.com/windowproof/fenestration/internal/acquisition"
	"github.com/windowproof/fenestration/internal/catalog"
	"github.com/windowproof/fenestration/internal/codes"
	"github.com/windowproof/fenestration/internal/inspection"
	"github.com/windowproof/fenestration/internal/store"
)

// SprayCheckpointRequest submits one spray checkpoint observation.
type SprayCheckpointRequest struct {
	TaskID           string `json:"task_id"`
	OperationID      string `json:"operation_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	CheckpointID     string `json:"checkpoint_id"`
	ActualPa         int64  `json:"actual_pa"`
	Observation      string `json:"observation,omitempty"`
}

// SprayCheckpointResponse reports the coverage outcome.
type SprayCheckpointResponse struct {
	TaskID        string `json:"task_id"`
	Generation    int64  `json:"generation"`
	State         string `json:"state"`
	StateRevision int64  `json:"state_revision"`
	CheckpointID  string `json:"checkpoint_id"`
	Covered       bool   `json:"covered"`
	SensorStatus  string `json:"sensor_status"`
}

// SubmitSprayCheckpoint validates the checkpoint's pressure tolerance and time
// window, executes the spray-nozzle instrument call and, only on a fully valid
// receipt, joins the coverage set and advances the phase when complete.
func (s *Service) SubmitSprayCheckpoint(ctx context.Context, in SprayCheckpointRequest) ([]byte, error) {
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
		if task.State != inspection.StateWaterSpray {
			return rejected(Err(codes.InvalidPhase))
		}
		if in.ExpectedRevision != task.StateRevision {
			return rejected(Err(codes.StaleRevision))
		}

		rev, ok, err := tx.LoadCatalog(ctx, task.LockedCatalogRevision)
		if err != nil {
			return nil, err
		}
		if !ok {
			return rejected(Err(codes.CatalogMismatch))
		}
		tp, ok := rev.PlanByID(task.LockedPlanID)
		if !ok {
			return rejected(Err(codes.CatalogMismatch))
		}

		var target *catalog.SprayCheckpointSpec
		for _, c := range tp.SprayCheckpoints {
			if c.CheckpointID == in.CheckpointID {
				cp := c
				target = &cp
				break
			}
		}
		if target == nil {
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "checkpoint", in.CheckpointID))
		}

		// Time window check using the monotonic spray anchor.
		elapsed := s.clock.NowMillis() - task.SprayStartedAt
		if elapsed < target.StartOffsetSeconds*1000 {
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "checkpoint", in.CheckpointID))
		}
		if elapsed > target.EndOffsetSeconds*1000 {
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "checkpoint", in.CheckpointID))
		}

		// Pressure tolerance check.
		delta, err := acquisition.SafeSub(in.ActualPa, tp.SprayTargetPa)
		if err != nil {
			return rejected(Err(codes.ArithmeticError))
		}
		if delta < 0 {
			delta = -delta
		}
		within, err := acquisition.SafeSub(tp.SprayTolerancePa, delta)
		if err != nil {
			return rejected(Err(codes.ArithmeticError))
		}
		if within < 0 {
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "actual_pa", itoa(int(in.ActualPa))))
		}

		// Execute the spray-nozzle instrument call.
		attempt := acquisition.InstrumentAttempt{
			AttemptID:      newAttemptID(),
			OperationID:    in.OperationID,
			TaskID:         in.TaskID,
			Generation:     int64(task.Generation),
			Kind:           "spray",
			RequestHash:    HashRequest(in),
			RequestPayload: string(mustJSON(in)),
			Status:         acquisition.AttemptPending,
		}
		if err := tx.SaveAttempt(ctx, attempt); err != nil {
			return nil, err
		}
		res, callErr := s.instrument.Call(ctx, "spray")
		if callErr != nil {
			attempt.Status = acquisition.AttemptFailed
			attempt.FailureCode = string(codes.InstrumentDisconnected)
			// The failure-status write must not be swallowed: if the context is
			// already cancelled (client disconnect) the update fails and we must
			// roll back the whole transaction, including the pending insert,
			// rather than commit a stuck pending attempt that retry cannot handle.
			if err := tx.SaveAttempt(ctx, attempt); err != nil {
				return nil, err
			}
			return rejected(Err(codes.InstrumentDisconnected))
		}
		if res.Status != "ok" {
			attempt.Status = acquisition.AttemptFailed
			attempt.FailureCode = instrumentCode(res.Status)
			attempt.ResponsePayload = res.Payload
			if err := tx.SaveAttempt(ctx, attempt); err != nil {
				return nil, err
			}
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

		// Join the coverage set (idempotent via the unique constraint).
		rec := acquisition.SprayRecord{
			TaskID:       in.TaskID,
			Generation:   int64(task.Generation),
			CheckpointID: in.CheckpointID,
			ActualPa:     in.ActualPa,
			SensorStatus: acquisition.SensorOK,
			Observation:  in.Observation,
			Covered:      true,
		}
		if err := tx.SaveSprayRecord(ctx, rec); err != nil {
			return nil, err
		}

		// When all checkpoints are covered, advance to wind review.
		if s.coverageComplete(ctx, tx, tp, in.TaskID, int64(task.Generation)) {
			task.State = inspection.StateWindReview
			task.CurrentPhase = inspection.PhaseWind
		}
		task.StateRevision++
		task.UpdatedAt = s.clock.NowMillis()
		if err := tx.SaveTask(ctx, task); err != nil {
			return nil, err
		}

		payload := mustJSON(SprayCheckpointResponse{
			TaskID: task.TaskID, Generation: int64(task.Generation), State: string(task.State),
			StateRevision: task.StateRevision, CheckpointID: in.CheckpointID, Covered: true,
			SensorStatus: string(acquisition.SensorOK),
		})
		return &outcome{payload: payload}, nil
	})
}

// coverageComplete reports whether every checkpoint in the plan is covered in
// the current generation.
func (s *Service) coverageComplete(ctx context.Context, tx store.Tx, tp catalog.TestPlan, taskID string, generation int64) bool {
	recs, err := tx.LoadSprayRecords(ctx, taskID, generation)
	if err != nil {
		return false
	}
	covered := map[string]bool{}
	for _, r := range recs {
		if r.Covered {
			covered[r.CheckpointID] = true
		}
	}
	for _, c := range tp.SprayCheckpoints {
		if !covered[c.CheckpointID] {
			return false
		}
	}
	return len(tp.SprayCheckpoints) > 0
}
