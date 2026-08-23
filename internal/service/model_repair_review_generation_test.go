package service

import (
	"context"
	"encoding/json"
	"testing"
)

func TestModel_RepairGenerationRebindsReviewSeats(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, StaticInstrument{})
	taskID := "model-repair-review-seats"

	mustSnapshot := func() *Snapshot {
		t.Helper()
		snap, err := svc.GetTask(ctx, taskID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		return snap
	}
	mustWrite := func(action string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}

	_, err := svc.CreateTask(ctx, CreateTaskRequest{TaskID: taskID, OperationID: taskID + "-create"})
	mustWrite("create", err)
	_, err = svc.Lock(ctx, LockRequest{
		TaskID: taskID, OperationID: taskID + "-lock", ExpectedRevision: mustSnapshot().StateRevision,
		CatalogRevisionID: "rev-demo", WindowUnitID: "W-E-01",
		ProfileBatch: "p1", GlassBatch: "g1", SealBatch: "s1",
		PlanID: "plan-1", ChamberID: "chamber-1", MeasurementPointIDs: []string{"mp-1", "mp-2"},
	})
	mustWrite("lock", err)
	_, err = svc.ConfirmInstallation(ctx, InstallationRequest{
		TaskID: taskID, OperationID: taskID + "-install", ExpectedRevision: mustSnapshot().StateRevision,
		Orientation: "east", Sealed: true, PointsZeroed: true,
	})
	mustWrite("install", err)

	for _, step := range []struct {
		name     string
		polarity string
		actualPa int64
	}{
		{name: "negative", polarity: "negative", actualPa: -100},
		{name: "positive", polarity: "positive", actualPa: 100},
	} {
		_, err = svc.SubmitPressureStep(ctx, PressureStepRequest{
			TaskID: taskID, OperationID: taskID + "-air-" + step.name, ExpectedRevision: mustSnapshot().StateRevision,
			Phase: "air", Polarity: step.polarity, Ordinal: 1, ActualPa: step.actualPa, AirflowCCPerSec: 10,
		})
		mustWrite("air "+step.name, err)
	}
	_, err = svc.SubmitSprayCheckpoint(ctx, SprayCheckpointRequest{
		TaskID: taskID, OperationID: taskID + "-spray", ExpectedRevision: mustSnapshot().StateRevision,
		CheckpointID: "c1", ActualPa: 100,
	})
	mustWrite("spray", err)
	_, err = svc.SubmitPressureStep(ctx, PressureStepRequest{
		TaskID: taskID, OperationID: taskID + "-wind", ExpectedRevision: mustSnapshot().StateRevision,
		Phase: "wind", Polarity: "positive", Ordinal: 1, ActualPa: 1000, DisplacementMicron: 100,
	})
	mustWrite("wind", err)

	firstHash, err := svc.VerdictHash(ctx, taskID)
	mustWrite("initial verdict hash", err)
	for _, reviewer := range []string{"reviewer-a", "reviewer-b"} {
		_, err = svc.SubmitReview(ctx, ReviewRequest{
			TaskID: taskID, OperationID: taskID + "-g0-" + reviewer, ReviewerID: reviewer,
			QualificationRevision: "q1", Decision: "pass", VerdictHash: firstHash,
		})
		mustWrite("initial review "+reviewer, err)
	}
	beforeRepair := mustSnapshot()
	if beforeRepair.State != "releasable" || len(beforeRepair.Reviews) != 2 {
		t.Fatalf("pre-repair state=%s reviews=%d, want releasable with 2 reviews", beforeRepair.State, len(beforeRepair.Reviews))
	}

	body, err := svc.AddDefect(ctx, DefectRequest{
		TaskID: taskID, OperationID: taskID + "-defect", ExpectedRevision: beforeRepair.StateRevision,
		DefectKind: "residual_deformation", WindowUnitID: "W-E-01", PressureOrdinal: 1,
		MeasurementPointID: "mp-1", Observation: "repair requires a fresh wind conclusion",
	})
	mustWrite("defect", err)
	var defect DefectResponse
	if err := json.Unmarshal(body, &defect); err != nil {
		t.Fatalf("decode defect response: %v", err)
	}

	_, err = svc.StartRepair(ctx, RepairRequest{
		TaskID: taskID, OperationID: taskID + "-repair", ExpectedRevision: defect.StateRevision,
		AffectedSteps: []string{"wind/positive/1"}, ReasonEvidenceIDs: []string{defect.EvidenceID},
	})
	mustWrite("repair", err)
	repairSnap := mustSnapshot()
	if repairSnap.Generation != beforeRepair.Generation+1 {
		t.Fatalf("generation=%d, want %d", repairSnap.Generation, beforeRepair.Generation+1)
	}
	if repairSnap.State != "wind_review" {
		t.Fatalf("state=%s, want wind_review after repair", repairSnap.State)
	}
	if len(repairSnap.Reviews) != 0 {
		t.Fatalf("stale prior-generation reviews visible after repair: %+v", repairSnap.Reviews)
	}

	_, err = svc.CloseDefect(ctx, CloseDefectRequest{
		TaskID: taskID, OperationID: taskID + "-close", ExpectedRevision: repairSnap.StateRevision,
		EvidenceID: defect.EvidenceID, Verification: "repaired and ready for wind retest",
	})
	mustWrite("close defect", err)
	_, err = svc.SubmitPressureStep(ctx, PressureStepRequest{
		TaskID: taskID, OperationID: taskID + "-wind-repair", ExpectedRevision: mustSnapshot().StateRevision,
		Phase: "wind", Polarity: "positive", Ordinal: 1, ActualPa: 1000, DisplacementMicron: 90,
	})
	mustWrite("repair wind", err)
	pending := mustSnapshot()
	if pending.State != "pending_review" || len(pending.Reviews) != 0 {
		t.Fatalf("post-repair state=%s reviews=%d, want pending_review with no carried seats", pending.State, len(pending.Reviews))
	}

	nextHash, err := svc.VerdictHash(ctx, taskID)
	mustWrite("repair verdict hash", err)
	if nextHash == firstHash {
		t.Fatalf("repair verdict hash did not change across generation")
	}

	for _, tc := range []struct {
		name        string
		reviewerID  string
		wantState   string
		wantReviews int
	}{
		{name: "first original reviewer binds new summary", reviewerID: "reviewer-a", wantState: "pending_review", wantReviews: 1},
		{name: "second original reviewer makes task releasable", reviewerID: "reviewer-b", wantState: "releasable", wantReviews: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.SubmitReview(ctx, ReviewRequest{
				TaskID: taskID, OperationID: taskID + "-g1-" + tc.reviewerID, ReviewerID: tc.reviewerID,
				QualificationRevision: "q1", Decision: "pass", VerdictHash: nextHash,
			})
			if err != nil {
				t.Fatalf("repair-generation review by %s: %v", tc.reviewerID, err)
			}
			snap := mustSnapshot()
			if snap.State != tc.wantState || len(snap.Reviews) != tc.wantReviews {
				t.Fatalf("state=%s reviews=%d, want %s with %d reviews", snap.State, len(snap.Reviews), tc.wantState, tc.wantReviews)
			}
		})
	}
}
