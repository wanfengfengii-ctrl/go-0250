package service

import (
	"context"

	"github.com/windowproof/fenestration/internal/codes"
	"github.com/windowproof/fenestration/internal/inspection"
	"github.com/windowproof/fenestration/internal/occupancy"
	"github.com/windowproof/fenestration/internal/store"
)

// CreateTaskRequest creates a pending-lock task.
type CreateTaskRequest struct {
	TaskID      string `json:"task_id"`
	OperationID string `json:"operation_id"`
}

// CreateTaskResponse is returned when a task is created.
type CreateTaskResponse struct {
	TaskID        string `json:"task_id"`
	Generation    int64  `json:"generation"`
	State         string `json:"state"`
	StateRevision int64  `json:"state_revision"`
}

// CreateTask creates a task in the pending-lock state.
func (s *Service) CreateTask(ctx context.Context, in CreateTaskRequest) ([]byte, error) {
	return s.run(ctx, in.OperationID, in, func(tx store.Tx) (*outcome, error) {
		if in.TaskID == "" {
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "task_id", in.TaskID))
		}
		if _, ok, err := tx.LoadTask(ctx, in.TaskID); err != nil {
			return nil, err
		} else if ok {
			// Creating an existing task is rejected with a deterministic code.
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "task_id", in.TaskID))
		}
		now := s.clock.NowMillis()
		task := inspection.InspectionTask{
			TaskID:        in.TaskID,
			Generation:    1,
			State:         inspection.StatePendingLock,
			StateRevision: 0,
			CurrentPhase:  "",
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := tx.SaveTask(ctx, task); err != nil {
			return nil, err
		}
		tx.AppendAudit(ctx, store.AuditEvent{TaskID: in.TaskID, Operation: "create_task", EventType: "task_created", Payload: []byte("{}")})
		payload := mustJSON(CreateTaskResponse{
			TaskID:        task.TaskID,
			Generation:    int64(task.Generation),
			State:         string(task.State),
			StateRevision: task.StateRevision,
		})
		return &outcome{payload: payload}, nil
	})
}

// LockRequest freezes the catalog snapshot and acquires all resource tokens.
type LockRequest struct {
	TaskID              string   `json:"task_id"`
	OperationID         string   `json:"operation_id"`
	ExpectedRevision    int64    `json:"expected_revision"`
	CatalogRevisionID   string   `json:"catalog_revision_id"`
	WindowUnitID        string   `json:"window_unit_id"`
	ProfileBatch        string   `json:"profile_batch"`
	GlassBatch          string   `json:"glass_batch"`
	SealBatch           string   `json:"seal_batch"`
	PlanID              string   `json:"plan_id"`
	ChamberID           string   `json:"chamber_id"`
	MeasurementPointIDs []string `json:"measurement_point_ids"`
}

// LockResponse is returned by a successful lock.
type LockResponse struct {
	TaskID                string      `json:"task_id"`
	Generation            int64       `json:"generation"`
	State                 string      `json:"state"`
	StateRevision         int64       `json:"state_revision"`
	LockedSnapshotHash    string      `json:"locked_snapshot_hash"`
	LockedCatalogRevision string      `json:"locked_catalog_revision"`
	WindowUnitID          string      `json:"window_unit_id"`
	Tokens                []TokenView `json:"tokens"`
}

// TokenView is the JSON view of an occupancy token.
type TokenView struct {
	ResourceKind string `json:"resource_kind"`
	ResourceID   string `json:"resource_id"`
}

// Lock freezes the catalog snapshot and acquires the window-unit, chamber and
// measurement-point tokens atomically. Any catalog mismatch rejects the whole
// operation without advancing the task or leaving partial occupancy.
func (s *Service) Lock(ctx context.Context, in LockRequest) ([]byte, error) {
	return s.run(ctx, in.OperationID, in, func(tx store.Tx) (*outcome, error) {
		task, ok, err := tx.LoadTask(ctx, in.TaskID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "task_id", in.TaskID))
		}
		if task.State != inspection.StatePendingLock {
			return rejected(Err(codes.InvalidState))
		}
		if in.ExpectedRevision != task.StateRevision {
			return rejected(Err(codes.StaleRevision))
		}

		rev, ok, err := tx.LoadCatalog(ctx, in.CatalogRevisionID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return rejected(Err(codes.CatalogMismatch).WithReason(codes.CatalogMismatch, "catalog_revision", in.CatalogRevisionID))
		}

		// Validate the window unit, orientation and all three material batches.
		if verrs := rev.ValidateLock(in.WindowUnitID, in.ProfileBatch, in.GlassBatch, in.SealBatch); len(verrs) > 0 {
			e := Err(codes.CatalogMismatch)
			for _, ve := range verrs {
				e.Reasons = append(e.Reasons, Reason{Code: codes.CatalogMismatch, Field: ve.Field, Value: ve.Value})
			}
			SortReasons(e.Reasons)
			e.TaskID = in.TaskID
			e.StateRevision = task.StateRevision
			return rejected(e)
		}
		if _, ok := rev.PlanByID(in.PlanID); !ok {
			return rejected(Err(codes.CatalogMismatch).WithReason(codes.CatalogMismatch, "plan_id", in.PlanID))
		}

		// Acquire all tokens in one transaction.
		reqs := []occupancy.Request{
			{ResourceKind: occupancy.ResourceWindowUnit, ResourceID: in.WindowUnitID},
			{ResourceKind: occupancy.ResourceChamber, ResourceID: in.ChamberID},
		}
		for _, mp := range in.MeasurementPointIDs {
			reqs = append(reqs, occupancy.Request{ResourceKind: occupancy.ResourceMeasurementPoint, ResourceID: mp})
		}
		tokens, err := tx.AcquireTokens(ctx, in.TaskID, int64(task.Generation), reqs)
		if err != nil {
			if oc, isOC := err.(store.ErrOccupancyConflict); isOC {
				e := Err(codes.OccupancyConflict).WithReason(codes.OccupancyConflict, string(oc.Kind), oc.ResourceID)
				e.TaskID = in.TaskID
				e.StateRevision = task.StateRevision
				return rejected(e)
			}
			return nil, err
		}

		now := s.clock.NowMillis()
		task.State = inspection.StateInstalling
		task.StateRevision++
		task.LockedCatalogRevision = in.CatalogRevisionID
		task.LockedSnapshotHash = rev.SnapshotHash()
		task.LockedPlanID = in.PlanID
		task.WindowUnitID = in.WindowUnitID
		task.ChamberID = in.ChamberID
		task.MeasurementPointIDs = append([]string(nil), in.MeasurementPointIDs...)
		task.CurrentPhase = ""
		task.UpdatedAt = now
		if err := tx.SaveTask(ctx, task); err != nil {
			return nil, err
		}

		views := make([]TokenView, 0, len(tokens))
		for _, tk := range tokens {
			views = append(views, TokenView{ResourceKind: string(tk.ResourceKind), ResourceID: tk.ResourceID})
		}
		payload := mustJSON(LockResponse{
			TaskID:                task.TaskID,
			Generation:            int64(task.Generation),
			State:                 string(task.State),
			StateRevision:         task.StateRevision,
			LockedSnapshotHash:    task.LockedSnapshotHash,
			LockedCatalogRevision: task.LockedCatalogRevision,
			WindowUnitID:          task.WindowUnitID,
			Tokens:                views,
		})
		return &outcome{payload: payload}, nil
	})
}
