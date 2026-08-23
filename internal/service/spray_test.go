package service

import (
	"context"
	"testing"

	"github.com/windowproof/fenestration/internal/catalog"
	"github.com/windowproof/fenestration/internal/codes"
)

// sprayCatalog returns a demo-like catalog whose single checkpoint opens at 5s
// and closes at 10s, so early/late submissions can be exercised deterministically.
func sprayCatalog() catalog.CatalogRevision {
	rev := catalog.DemoRevision()
	rev.RevisionID = "rev-spray"
	rev.TestPlans[0].SprayCheckpoints = []catalog.SprayCheckpointSpec{
		{CheckpointID: "c1", PressureOrdinal: 1, Zone: "a", StartOffsetSeconds: 5, EndOffsetSeconds: 10},
	}
	return rev
}

func driveToWater(t *testing.T, svc *Service, taskID string) int64 {
	t.Helper()
	mustCreate(t, svc, taskID)
	req := lockReq(taskID)
	req.CatalogRevisionID = "rev-spray"
	if _, err := svc.Lock(context.Background(), req); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if _, err := svc.ConfirmInstallation(context.Background(), InstallationRequest{
		TaskID: taskID, OperationID: taskID + "-install", ExpectedRevision: 1,
		Orientation: "east", Sealed: true, PointsZeroed: true,
	}); err != nil {
		t.Fatalf("install: %v", err)
	}
	return submitAir(t, svc, taskID, 2)
}

func TestSprayTimingAndPressureRejected(t *testing.T) {
	st, err := storeOpen()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	clock := &fakeClock{}
	svc := New(st, clock, StaticInstrument{})
	if err := svc.SeedCatalog(context.Background(), sprayCatalog()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	id := nextTaskID()
	rev := driveToWater(t, svc, id) // rev after air = water_spray at clock 0

	// Too early (checkpoint opens at 5s).
	_, err = svc.SubmitSprayCheckpoint(context.Background(), SprayCheckpointRequest{
		TaskID: id, OperationID: id + "-early", ExpectedRevision: rev, CheckpointID: "c1", ActualPa: 100,
	})
	if codeOf(t, err) != codes.InvalidRequest {
		t.Fatalf("early code = %v", codeOf(t, err))
	}

	// Wrong pressure at a valid time.
	clock.set(6000)
	_, err = svc.SubmitSprayCheckpoint(context.Background(), SprayCheckpointRequest{
		TaskID: id, OperationID: id + "-pressure", ExpectedRevision: rev, CheckpointID: "c1", ActualPa: 500,
	})
	if codeOf(t, err) != codes.InvalidRequest {
		t.Fatalf("pressure code = %v", codeOf(t, err))
	}

	// Timeout (checkpoint closes at 10s).
	clock.set(11000)
	_, err = svc.SubmitSprayCheckpoint(context.Background(), SprayCheckpointRequest{
		TaskID: id, OperationID: id + "-late", ExpectedRevision: rev, CheckpointID: "c1", ActualPa: 100,
	})
	if codeOf(t, err) != codes.InvalidRequest {
		t.Fatalf("late code = %v", codeOf(t, err))
	}

	// Coverage set must remain empty after all rejections.
	snap := snapshot(t, svc, id)
	if len(snap.SprayCoverage) != 0 {
		t.Fatalf("coverage = %v, want empty", snap.SprayCoverage)
	}
	if snap.State != "water_spray" {
		t.Fatalf("state = %s, want water_spray", snap.State)
	}
}

func TestSprayScriptedInstrumentFaults(t *testing.T) {
	script := NewScriptedInstrument([]Result{
		{Status: "ok"}, // air negative
		{Status: "ok"}, // air positive
		{Status: "rejected"},
		{Status: "disconnected"},
		{Status: "timeout"},
		{Status: "malformed"},
		{Status: "ok", Payload: "ok"},
	})
	st, err := storeOpen()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	clock := &fakeClock{}
	svc := New(st, clock, script)
	if err := svc.SeedCatalog(context.Background(), sprayCatalog()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	id := nextTaskID()
	rev := driveToWater(t, svc, id)
	clock.set(5000) // inside the checkpoint window

	// First submission: instrument rejects.
	_, err = svc.SubmitSprayCheckpoint(context.Background(), SprayCheckpointRequest{
		TaskID: id, OperationID: id + "-s0", ExpectedRevision: rev, CheckpointID: "c1", ActualPa: 100,
	})
	if codeOf(t, err) != codes.InstrumentRejected {
		t.Fatalf("first code = %v", codeOf(t, err))
	}

	// Retry the sequence through disconnect, timeout and malformed.
	attemptID := latestAttemptID(t, svc, id)
	for _, want := range []codes.Code{codes.InstrumentDisconnected, codes.InstrumentTimeout, codes.InstrumentMalformed} {
		_, err = svc.RetryAttempt(context.Background(), RetryAttemptRequest{
			TaskID: id, AttemptID: attemptID, OperationID: id + "-retry-" + string(want),
		})
		if codeOf(t, err) != want {
			t.Fatalf("retry code = %v, want %v", codeOf(t, err), want)
		}
		attemptID = latestAttemptID(t, svc, id)
	}

	// Final retry succeeds and covers the checkpoint exactly once.
	_, err = svc.RetryAttempt(context.Background(), RetryAttemptRequest{
		TaskID: id, AttemptID: attemptID, OperationID: id + "-retry-ok",
	})
	if err != nil {
		t.Fatalf("final retry: %v", err)
	}

	snap := snapshot(t, svc, id)
	if len(snap.SprayCoverage) != 1 || snap.SprayCoverage[0].CheckpointID != "c1" {
		t.Fatalf("coverage = %v, want exactly c1", snap.SprayCoverage)
	}
	// Two air pressure attempts plus five spray attempts (one submit + four
	// retries).
	if len(snap.Attempts) != 7 {
		t.Fatalf("attempts = %d, want 7", len(snap.Attempts))
	}
	// The four failed spray attempts remain in a retryable (failed) state.
	failed := 0
	for _, a := range snap.Attempts {
		if a.Kind == "spray" && a.Status == "failed" {
			failed++
		}
	}
	if failed != 4 {
		t.Fatalf("failed spray attempts = %d, want 4", failed)
	}
}

func latestAttemptID(t *testing.T, svc *Service, id string) string {
	t.Helper()
	snap := snapshot(t, svc, id)
	if len(snap.Attempts) == 0 {
		t.Fatalf("no attempts recorded")
	}
	return snap.Attempts[len(snap.Attempts)-1].AttemptID
}
