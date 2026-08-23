package service

import (
	"context"
	"encoding/json"

	"github.com/windowproof/fenestration/internal/acquisition"
	"github.com/windowproof/fenestration/internal/codes"
)

// RetryAttemptRequest explicitly retries a failed instrument attempt.
type RetryAttemptRequest struct {
	TaskID      string `json:"task_id"`
	AttemptID   string `json:"attempt_id"`
	OperationID string `json:"operation_id"`
}

// RetryAttempt re-executes a failed instrument call. A stale-generation attempt
// is rejected without touching current coverage or records; a current attempt
// creates a fresh attempt and, on success, applies the result exactly once.
func (s *Service) RetryAttempt(ctx context.Context, in RetryAttemptRequest) ([]byte, error) {
	attempts, err := s.store.LoadAttempts(ctx, in.TaskID)
	if err != nil {
		return nil, Err(codes.StoreUnavailable)
	}
	var target *acquisition.InstrumentAttempt
	for i := range attempts {
		if attempts[i].AttemptID == in.AttemptID {
			target = &attempts[i]
			break
		}
	}
	if target == nil {
		return nil, Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "attempt_id", in.AttemptID)
	}
	if target.Status != acquisition.AttemptFailed {
		return nil, Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "attempt_id", in.AttemptID)
	}

	task, ok, err := s.store.LoadTask(ctx, in.TaskID)
	if err != nil {
		return nil, Err(codes.StoreUnavailable)
	}
	if !ok {
		return nil, Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "task_id", in.TaskID)
	}
	if task.State.IsTerminal() {
		return nil, terminalError(task)
	}
	if int64(task.Generation) != target.Generation {
		e := Err(codes.StaleGeneration).WithReason(codes.StaleGeneration, "generation", itoa(int(target.Generation)))
		e.TaskID = task.TaskID
		e.StateRevision = task.StateRevision
		return nil, e
	}

	switch target.Kind {
	case "pressure":
		var req PressureStepRequest
		if err := json.Unmarshal([]byte(target.RequestPayload), &req); err != nil {
			return nil, Err(codes.InvalidRequest)
		}
		req.OperationID = in.OperationID
		return s.SubmitPressureStep(ctx, req)
	case "spray":
		var req SprayCheckpointRequest
		if err := json.Unmarshal([]byte(target.RequestPayload), &req); err != nil {
			return nil, Err(codes.InvalidRequest)
		}
		req.OperationID = in.OperationID
		return s.SubmitSprayCheckpoint(ctx, req)
	default:
		return nil, Err(codes.InvalidRequest)
	}
}
