package service

import (
	"context"

	"github.com/windowproof/fenestration/internal/codes"
	"github.com/windowproof/fenestration/internal/inspection"
	"github.com/windowproof/fenestration/internal/occupancy"
	"github.com/windowproof/fenestration/internal/store"
)

// InstallationRequest confirms orientation, sealing and point zeroing.
type InstallationRequest struct {
	TaskID           string `json:"task_id"`
	OperationID      string `json:"operation_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Orientation      string `json:"orientation"`
	Sealed           bool   `json:"sealed"`
	PointsZeroed     bool   `json:"points_zeroed"`
}

// InstallationResponse reports the new task state.
type InstallationResponse struct {
	TaskID        string `json:"task_id"`
	Generation    int64  `json:"generation"`
	State         string `json:"state"`
	StateRevision int64  `json:"state_revision"`
}

// ConfirmInstallation binds the locked orientation and, once sealing and point
// zeroing are confirmed, advances the task into air-loading.
func (s *Service) ConfirmInstallation(ctx context.Context, in InstallationRequest) ([]byte, error) {
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
		if task.State != inspection.StateInstalling {
			return rejected(Err(codes.InvalidState))
		}
		if in.ExpectedRevision != task.StateRevision {
			return rejected(Err(codes.StaleRevision))
		}
		if !in.Sealed || !in.PointsZeroed {
			e := Err(codes.InvalidRequest)
			if !in.Sealed {
				e.Reasons = append(e.Reasons, Reason{Code: codes.InvalidRequest, Field: "sealed", Value: "false"})
			}
			if !in.PointsZeroed {
				e.Reasons = append(e.Reasons, Reason{Code: codes.InvalidRequest, Field: "points_zeroed", Value: "false"})
			}
			return rejected(e)
		}

		// Orientation must match the frozen window unit orientation.
		rev, ok, err := tx.LoadCatalog(ctx, task.LockedCatalogRevision)
		if err != nil {
			return nil, err
		}
		if !ok {
			return rejected(Err(codes.CatalogMismatch))
		}
		unit, ok := rev.WindowUnitByID(task.WindowUnitID)
		if !ok || string(unit.Orientation) != in.Orientation {
			e := Err(codes.CatalogMismatch).WithReason(codes.CatalogMismatch, "orientation", in.Orientation)
			e.TaskID = task.TaskID
			e.StateRevision = task.StateRevision
			return rejected(e)
		}

		task.OrientationConfirmed = true
		task.SealedConfirmed = in.Sealed
		task.PointsZeroed = in.PointsZeroed
		task.State = inspection.StateAirLoading
		task.StateRevision++
		task.CurrentPhase = inspection.PhaseAir
		task.UpdatedAt = s.clock.NowMillis()
		if err := tx.SaveTask(ctx, task); err != nil {
			return nil, err
		}
		payload := mustJSON(InstallationResponse{
			TaskID: task.TaskID, Generation: int64(task.Generation),
			State: string(task.State), StateRevision: task.StateRevision,
		})
		return &outcome{payload: payload}, nil
	})
}

// ReplaceChamberRequest atomically swaps the test chamber.
type ReplaceChamberRequest struct {
	TaskID           string `json:"task_id"`
	OperationID      string `json:"operation_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	NewChamberID     string `json:"new_chamber_id"`
}

// ReplaceChamberResponse reports the new chamber.
type ReplaceChamberResponse struct {
	TaskID        string `json:"task_id"`
	State         string `json:"state"`
	StateRevision int64  `json:"state_revision"`
	ChamberID     string `json:"chamber_id"`
}

// ReplaceChamber releases the old chamber token and acquires the new one in a
// single transaction, only while the task is still installing.
func (s *Service) ReplaceChamber(ctx context.Context, in ReplaceChamberRequest) ([]byte, error) {
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
		if task.State != inspection.StateInstalling {
			return rejected(Err(codes.InvalidState))
		}
		if in.ExpectedRevision != task.StateRevision {
			return rejected(Err(codes.StaleRevision))
		}
		if in.NewChamberID == "" || in.NewChamberID == task.ChamberID {
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "chamber_id", in.NewChamberID))
		}

		// Release the old chamber, then acquire the new one.
		if err := tx.ReleaseToken(ctx, in.TaskID, occupancy.ResourceChamber, task.ChamberID, int64(task.Generation)); err != nil {
			return nil, err
		}
		if _, err := tx.AcquireTokens(ctx, in.TaskID, int64(task.Generation), []occupancy.Request{
			{ResourceKind: occupancy.ResourceChamber, ResourceID: in.NewChamberID},
		}); err != nil {
			if oc, isOC := err.(store.ErrOccupancyConflict); isOC {
				e := Err(codes.OccupancyConflict).WithReason(codes.OccupancyConflict, string(oc.Kind), oc.ResourceID)
				e.TaskID = task.TaskID
				e.StateRevision = task.StateRevision
				return rejected(e)
			}
			return nil, err
		}

		task.ChamberID = in.NewChamberID
		task.StateRevision++
		task.UpdatedAt = s.clock.NowMillis()
		if err := tx.SaveTask(ctx, task); err != nil {
			return nil, err
		}
		payload := mustJSON(ReplaceChamberResponse{
			TaskID: task.TaskID, State: string(task.State),
			StateRevision: task.StateRevision, ChamberID: task.ChamberID,
		})
		return &outcome{payload: payload}, nil
	})
}
