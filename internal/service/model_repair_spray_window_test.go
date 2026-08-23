package service

import (
	"context"
	"testing"

	"github.com/windowproof/fenestration/internal/codes"
)

func TestModel_RepairReopenedSprayWindowAnchorsCurrentGeneration(t *testing.T) {
	ctx := context.Background()

	setup := func(t *testing.T) (*Service, *fakeClock, string, int64, string) {
		t.Helper()
		script := NewScriptedInstrument([]Result{
			{Status: "ok"},       // air negative
			{Status: "ok"},       // air positive
			{Status: "rejected"}, // generation-1 spray attempt left retryable
			{Status: "ok"},       // generation-1 retry covers the original obligation
			{Status: "ok"},       // generation-2 reopened spray
			{Status: "ok"},       // generation-2 wind step
		})
		svc, clock := newTestService(t, script)
		id := nextTaskID()
		rev := lockAndInstall(t, svc, id)
		rev = submitAir(t, svc, id, rev)

		_, err := svc.SubmitSprayCheckpoint(ctx, SprayCheckpointRequest{
			TaskID: id, OperationID: id + "-spray-failed", ExpectedRevision: rev,
			CheckpointID: "c1", ActualPa: 100,
		})
		if codeOf(t, err) != codes.InstrumentRejected {
			t.Fatalf("initial spray code = %v, want INSTRUMENT_REJECTED", codeOf(t, err))
		}
		oldAttempt := latestAttemptID(t, svc, id)

		body, err := svc.RetryAttempt(ctx, RetryAttemptRequest{
			TaskID: id, AttemptID: oldAttempt, OperationID: id + "-spray-retry-ok",
		})
		if err != nil {
			t.Fatalf("retry original spray: %v", err)
		}
		var spray SprayCheckpointResponse
		decodeResponse(t, body, &spray)
		rev = spray.StateRevision

		body, err = svc.AddDefect(ctx, DefectRequest{
			TaskID: id, OperationID: id + "-leak", ExpectedRevision: rev,
			DefectKind: "water_leak", WindowUnitID: "W-E-01", Observation: "leak found during wind stage",
		})
		if err != nil {
			t.Fatalf("add defect: %v", err)
		}
		var defect DefectResponse
		decodeResponse(t, body, &defect)

		clock.set(30000)
		body, err = svc.StartRepair(ctx, RepairRequest{
			TaskID: id, OperationID: id + "-repair", ExpectedRevision: defect.StateRevision,
			AffectedCheckpoints: []string{"c1"}, ReasonEvidenceIDs: []string{defect.EvidenceID},
		})
		if err != nil {
			t.Fatalf("start repair: %v", err)
		}
		var repair RepairResponse
		decodeResponse(t, body, &repair)
		if repair.Generation != 2 || repair.State != "water_spray" {
			t.Fatalf("repair reopened generation/state = %d/%s, want 2/water_spray", repair.Generation, repair.State)
		}
		return svc, clock, id, repair.StateRevision, oldAttempt
	}

	cases := []struct {
		name string
		run  func(t *testing.T, svc *Service, clock *fakeClock, id string, repairRev int64, oldAttempt string)
	}{
		{
			name: "current spray submitted at repair time advances to wind and accepts wind step",
			run: func(t *testing.T, svc *Service, clock *fakeClock, id string, repairRev int64, oldAttempt string) {
				body, err := svc.SubmitSprayCheckpoint(ctx, SprayCheckpointRequest{
					TaskID: id, OperationID: id + "-repair-spray", ExpectedRevision: repairRev,
					CheckpointID: "c1", ActualPa: 100,
				})
				if err != nil {
					t.Fatalf("reopened spray at %dms: %v", clock.NowMillis(), err)
				}
				var spray SprayCheckpointResponse
				decodeResponse(t, body, &spray)
				if spray.Generation != 2 || spray.State != "wind_review" {
					t.Fatalf("spray response generation/state = %d/%s, want 2/wind_review", spray.Generation, spray.State)
				}

				body, err = svc.SubmitPressureStep(ctx, PressureStepRequest{
					TaskID: id, OperationID: id + "-wind-current", ExpectedRevision: spray.StateRevision,
					Phase: "wind", Polarity: "positive", Ordinal: 1, ActualPa: 1000, DisplacementMicron: 100,
				})
				if err != nil {
					t.Fatalf("current generation wind step: %v", err)
				}
				var wind PressureStepResponse
				decodeResponse(t, body, &wind)
				if wind.Generation != 2 || wind.State != "pending_review" {
					t.Fatalf("wind response generation/state = %d/%s, want 2/pending_review", wind.Generation, wind.State)
				}
			},
		},
		{
			name: "stale generation spray retry is isolated before current spray",
			run: func(t *testing.T, svc *Service, clock *fakeClock, id string, repairRev int64, oldAttempt string) {
				_, err := svc.RetryAttempt(ctx, RetryAttemptRequest{
					TaskID: id, AttemptID: oldAttempt, OperationID: id + "-stale-retry",
				})
				if codeOf(t, err) != codes.StaleGeneration {
					t.Fatalf("stale retry code = %v, want STALE_GENERATION", codeOf(t, err))
				}
				snap := snapshot(t, svc, id)
				if snap.Generation != 2 || snap.State != "water_spray" || len(snap.SprayCoverage) != 0 {
					t.Fatalf("after stale retry generation/state/coverage = %d/%s/%v, want 2/water_spray/empty", snap.Generation, snap.State, snap.SprayCoverage)
				}

				body, err := svc.SubmitSprayCheckpoint(ctx, SprayCheckpointRequest{
					TaskID: id, OperationID: id + "-repair-spray-after-stale", ExpectedRevision: repairRev,
					CheckpointID: "c1", ActualPa: 100,
				})
				if err != nil {
					t.Fatalf("reopened spray after stale retry at %dms: %v", clock.NowMillis(), err)
				}
				var spray SprayCheckpointResponse
				decodeResponse(t, body, &spray)
				if spray.Generation != 2 || spray.State != "wind_review" {
					t.Fatalf("spray response generation/state = %d/%s, want 2/wind_review", spray.Generation, spray.State)
				}
				snap = snapshot(t, svc, id)
				if len(snap.SprayCoverage) != 1 || snap.SprayCoverage[0].CheckpointID != "c1" {
					t.Fatalf("current generation coverage = %v, want exactly c1", snap.SprayCoverage)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, clock, id, repairRev, oldAttempt := setup(t)
			tc.run(t, svc, clock, id, repairRev, oldAttempt)
		})
	}
}
