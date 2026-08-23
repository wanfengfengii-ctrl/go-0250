package service

import (
	"context"
	"testing"

	"github.com/windowproof/fenestration/internal/codes"
)

func TestModel_RepairRejectsPreRunStatesWithoutMutation(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name                string
		setup               func(t *testing.T, svc *Service, taskID string) int64
		wantState           string
		wantGeneration      int64
		wantRevision        int64
		continueAfterReject func(t *testing.T, svc *Service, taskID string)
	}{
		{
			name: "pending lock task remains lockable",
			setup: func(t *testing.T, svc *Service, taskID string) int64 {
				t.Helper()
				return mustCreate(t, svc, taskID)
			},
			wantState:      "pending_lock",
			wantGeneration: 1,
			wantRevision:   0,
			continueAfterReject: func(t *testing.T, svc *Service, taskID string) {
				t.Helper()
				if _, err := svc.Lock(ctx, lockReq(taskID)); err != nil {
					t.Fatalf("lock after rejected repair: %v", err)
				}
				snap := snapshot(t, svc, taskID)
				if snap.State != "installing" || snap.Generation != 1 || snap.StateRevision != 1 {
					t.Fatalf("post-lock snapshot = state %s generation %d revision %d, want installing/1/1", snap.State, snap.Generation, snap.StateRevision)
				}
				if snap.LockedCatalogRevision == "" || snap.ChamberID == "" || len(snap.MeasurementPointIDs) == 0 {
					t.Fatalf("lock did not bind required resources: catalog %q chamber %q points %v", snap.LockedCatalogRevision, snap.ChamberID, snap.MeasurementPointIDs)
				}
			},
		},
		{
			name: "installing task remains installable",
			setup: func(t *testing.T, svc *Service, taskID string) int64 {
				t.Helper()
				mustCreate(t, svc, taskID)
				if _, err := svc.Lock(ctx, lockReq(taskID)); err != nil {
					t.Fatalf("lock: %v", err)
				}
				return 1
			},
			wantState:      "installing",
			wantGeneration: 1,
			wantRevision:   1,
			continueAfterReject: func(t *testing.T, svc *Service, taskID string) {
				t.Helper()
				if _, err := svc.ConfirmInstallation(ctx, InstallationRequest{
					TaskID: taskID, OperationID: taskID + "-install", ExpectedRevision: 1,
					Orientation: "east", Sealed: true, PointsZeroed: true,
				}); err != nil {
					t.Fatalf("install after rejected repair: %v", err)
				}
				snap := snapshot(t, svc, taskID)
				if snap.State != "air_loading" || snap.Generation != 1 || snap.StateRevision != 2 {
					t.Fatalf("post-install snapshot = state %s generation %d revision %d, want air_loading/1/2", snap.State, snap.Generation, snap.StateRevision)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newTestService(t, StaticInstrument{})
			taskID := nextTaskID()
			rev := tc.setup(t, svc, taskID)

			_, err := svc.StartRepair(ctx, RepairRequest{
				TaskID: taskID, OperationID: taskID + "-repair-before-run", ExpectedRevision: rev,
				AffectedCheckpoints: []string{"c1"},
			})
			if err == nil {
				t.Fatalf("StartRepair accepted %s task without current-generation evidence", tc.wantState)
			}
			if got := codeOf(t, err); got != codes.InvalidState {
				t.Fatalf("StartRepair code = %v, want %v", got, codes.InvalidState)
			}

			snap := snapshot(t, svc, taskID)
			if snap.State != tc.wantState || snap.Generation != tc.wantGeneration || snap.StateRevision != tc.wantRevision {
				t.Fatalf("rejected repair mutated snapshot = state %s generation %d revision %d, want %s/%d/%d",
					snap.State, snap.Generation, snap.StateRevision, tc.wantState, tc.wantGeneration, tc.wantRevision)
			}

			tc.continueAfterReject(t, svc, taskID)
		})
	}
}
