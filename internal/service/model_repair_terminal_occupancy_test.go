package service

import (
	"context"
	"testing"

	"github.com/windowproof/fenestration/internal/codes"
)

func TestModel_RepairTerminalReleasesInheritedOccupancy(t *testing.T) {
	ctx := context.Background()

	openRepairAtWater := func(t *testing.T, svc *Service, taskID string) int64 {
		t.Helper()
		rev := lockAndInstall(t, svc, taskID)
		rev = submitAir(t, svc, taskID, rev)
		rev = submitSpray(t, svc, taskID, rev)

		body, err := svc.AddDefect(ctx, DefectRequest{
			TaskID:           taskID,
			OperationID:      taskID + "-defect",
			ExpectedRevision: rev,
			DefectKind:       "water_leak",
			WindowUnitID:     "W-E-01",
			Observation:      "repair generation must keep occupancy releasable",
		})
		if err != nil {
			t.Fatalf("add defect: %v", err)
		}
		var defect DefectResponse
		decodeResponse(t, body, &defect)

		body, err = svc.StartRepair(ctx, RepairRequest{
			TaskID:              taskID,
			OperationID:         taskID + "-repair",
			ExpectedRevision:    defect.StateRevision,
			AffectedCheckpoints: []string{"c1"},
			ReasonEvidenceIDs:   []string{defect.EvidenceID},
		})
		if err != nil {
			t.Fatalf("start repair: %v", err)
		}
		var repair RepairResponse
		decodeResponse(t, body, &repair)
		if repair.Generation != 2 || repair.State != "water_spray" {
			t.Fatalf("repair generation/state = %d/%s, want 2/water_spray", repair.Generation, repair.State)
		}
		return repair.StateRevision
	}

	cases := []struct {
		name      string
		prepare   func(*testing.T, *Service, string) int64
		terminal  func(*Service, string, int64) error
		wantState string
	}{
		{
			name:    "quarantine",
			prepare: openRepairAtWater,
			terminal: func(svc *Service, taskID string, rev int64) error {
				_, err := svc.Quarantine(ctx, TerminalRequest{
					TaskID: taskID, OperationID: taskID + "-quarantine", ExpectedRevision: rev,
				})
				return err
			},
			wantState: "quarantined",
		},
		{
			name:    "cancel",
			prepare: openRepairAtWater,
			terminal: func(svc *Service, taskID string, rev int64) error {
				_, err := svc.Cancel(ctx, TerminalRequest{
					TaskID: taskID, OperationID: taskID + "-cancel", ExpectedRevision: rev,
				})
				return err
			},
			wantState: "cancelled",
		},
		{
			name: "release",
			prepare: func(t *testing.T, svc *Service, taskID string) int64 {
				t.Helper()
				rev := openRepairAtWater(t, svc, taskID)
				if _, err := svc.SubmitSprayCheckpoint(ctx, SprayCheckpointRequest{
					TaskID: taskID, OperationID: taskID + "-repair-spray", ExpectedRevision: rev,
					CheckpointID: "c1", ActualPa: 100,
				}); err != nil {
					t.Fatalf("repair spray: %v", err)
				}
				rev++
				rev = submitWind(t, svc, taskID, rev)
				hash, err := svc.VerdictHash(ctx, taskID)
				if err != nil {
					t.Fatalf("verdict hash: %v", err)
				}
				for i, reviewer := range []string{"reviewer-a", "reviewer-b"} {
					if _, err := svc.SubmitReview(ctx, ReviewRequest{
						TaskID: taskID, OperationID: taskID + "-repair-review-" + itoa(i),
						ReviewerID: reviewer, QualificationRevision: "q1", Decision: "pass", VerdictHash: hash,
					}); err != nil {
						t.Fatalf("review %s: %v", reviewer, err)
					}
					rev++
				}
				return rev
			},
			terminal: func(svc *Service, taskID string, rev int64) error {
				_, err := svc.Release(ctx, TerminalRequest{
					TaskID: taskID, OperationID: taskID + "-release", ExpectedRevision: rev,
				})
				return err
			},
			wantState: "released",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newTestService(t, StaticInstrument{})
			firstID := nextTaskID()
			terminalRev := tc.prepare(t, svc, firstID)

			if err := tc.terminal(svc, firstID, terminalRev); err != nil {
				t.Fatalf("%s terminal: %v", tc.name, err)
			}
			if got := snapshot(t, svc, firstID).State; got != tc.wantState {
				t.Fatalf("terminal state = %s, want %s", got, tc.wantState)
			}

			tokens, err := svc.Store().LoadTokens(ctx, firstID)
			if err != nil {
				t.Fatalf("load tokens: %v", err)
			}
			active := 0
			for _, token := range tokens {
				if token.Active {
					active++
				}
			}
			if active != 0 {
				t.Errorf("active tokens after repair %s = %d, want 0", tc.name, active)
			}

			nextID := nextTaskID()
			mustCreate(t, svc, nextID)
			req := lockReq(nextID)
			if _, err := svc.Lock(ctx, req); err != nil {
				if codeOf(t, err) == codes.OccupancyConflict {
					t.Fatalf("new task saw stale OCCUPANCY_CONFLICT after repair %s: %v", tc.name, err)
				}
				t.Fatalf("lock next task after repair %s: %v", tc.name, err)
			}
		})
	}
}
