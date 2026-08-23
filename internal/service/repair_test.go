package service

import (
	"context"
	"testing"

	"github.com/windowproof/fenestration/internal/codes"
)

func TestRepairGenerationReopensOnlyAffected(t *testing.T) {
	svc, _ := newTestService(t, StaticInstrument{})
	id := nextTaskID()
	rev := lockAndInstall(t, svc, id)  // rev 2
	rev = submitAir(t, svc, id, rev)   // rev 4, water_spray
	rev = submitSpray(t, svc, id, rev) // rev 5, wind_review

	// Observe a water-leak defect during wind review.
	body, err := svc.AddDefect(context.Background(), DefectRequest{
		TaskID: id, OperationID: id + "-defect", ExpectedRevision: rev,
		DefectKind: "water_leak", WindowUnitID: "W-E-01", Observation: "leak at sill",
	})
	if err != nil {
		t.Fatalf("defect: %v", err)
	}
	var dresp DefectResponse
	decodeResponse(t, body, &dresp)
	rev = dresp.StateRevision

	// Open a repair generation reopening only the water checkpoint.
	_, err = svc.StartRepair(context.Background(), RepairRequest{
		TaskID: id, OperationID: id + "-repair", ExpectedRevision: rev,
		AffectedCheckpoints: []string{"c1"}, ReasonEvidenceIDs: []string{dresp.EvidenceID},
	})
	if err != nil {
		t.Fatalf("repair: %v", err)
	}

	snap := snapshot(t, svc, id)
	if snap.Generation != 2 {
		t.Fatalf("generation = %d, want 2", snap.Generation)
	}
	if snap.State != "water_spray" {
		t.Fatalf("state = %s, want water_spray", snap.State)
	}
	// The unaffected air steps survive into the new generation.
	if len(snap.StepRecords) != 2 {
		t.Fatalf("air step records = %d, want 2", len(snap.StepRecords))
	}
	// The affected water checkpoint is reopened (no coverage).
	if len(snap.SprayCoverage) != 0 {
		t.Fatalf("coverage = %v, want reopened (empty)", snap.SprayCoverage)
	}
}

func TestStaleGenerationReceiptRejected(t *testing.T) {
	script := NewScriptedInstrument([]Result{
		{Status: "ok"},       // air negative
		{Status: "ok"},       // air positive
		{Status: "rejected"}, // spray submit fails in generation 1
		{Status: "ok"},       // spray retry in generation 2 (not reached)
	})
	svc, _ := newTestService(t, script)
	id := nextTaskID()
	rev := lockAndInstall(t, svc, id)
	rev = submitAir(t, svc, id, rev) // water_spray

	_, err := svc.SubmitSprayCheckpoint(context.Background(), SprayCheckpointRequest{
		TaskID: id, OperationID: id + "-s0", ExpectedRevision: rev, CheckpointID: "c1", ActualPa: 100,
	})
	if codeOf(t, err) != codes.InstrumentRejected {
		t.Fatalf("spray code = %v", codeOf(t, err))
	}
	oldAttempt := latestAttemptID(t, svc, id)

	// Observe a defect and open a repair generation.
	dbody, err := svc.AddDefect(context.Background(), DefectRequest{
		TaskID: id, OperationID: id + "-defect", ExpectedRevision: rev,
		DefectKind: "water_leak", WindowUnitID: "W-E-01",
	})
	if err != nil {
		t.Fatalf("defect: %v", err)
	}
	var dresp DefectResponse
	decodeResponse(t, dbody, &dresp)
	if _, err := svc.StartRepair(context.Background(), RepairRequest{
		TaskID: id, OperationID: id + "-repair", ExpectedRevision: dresp.StateRevision,
		AffectedCheckpoints: []string{"c1"}, ReasonEvidenceIDs: []string{dresp.EvidenceID},
	}); err != nil {
		t.Fatalf("repair: %v", err)
	}

	// The stale generation-1 receipt must be rejected without touching coverage.
	_, err = svc.RetryAttempt(context.Background(), RetryAttemptRequest{
		TaskID: id, AttemptID: oldAttempt, OperationID: id + "-retry-stale",
	})
	if codeOf(t, err) != codes.StaleGeneration {
		t.Fatalf("stale code = %v, want STALE_GENERATION", codeOf(t, err))
	}
	snap := snapshot(t, svc, id)
	if len(snap.SprayCoverage) != 0 {
		t.Fatalf("coverage changed on stale receipt: %v", snap.SprayCoverage)
	}
	// The evidence chain is immutable and still present.
	if len(snap.Evidence) != 1 {
		t.Fatalf("evidence = %d, want 1", len(snap.Evidence))
	}
}
