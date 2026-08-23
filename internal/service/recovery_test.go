package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/windowproof/fenestration/internal/catalog"
	"github.com/windowproof/fenestration/internal/codes"
	"github.com/windowproof/fenestration/internal/store"
)

func openFileStore(t *testing.T) store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "windowproof-test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestCommitFailureRollsBackAtomically(t *testing.T) {
	st := openFileStore(t)
	fp, ok := st.(store.CommitFailpoint)
	if !ok {
		t.Fatalf("store does not implement CommitFailpoint")
	}
	svc := New(st, WallClock{}, StaticInstrument{})
	if err := svc.SeedCatalog(context.Background(), catalog.DemoRevision()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Lock commit failure leaves no tokens and no task advance.
	id := nextTaskID()
	mustCreate(t, svc, id)
	fp.FailNextCommits(1)
	_, err := svc.Lock(context.Background(), lockReq(id))
	if codeOf(t, err) != codes.StoreUnavailable {
		t.Fatalf("lock fail code = %v", codeOf(t, err))
	}
	if snap := snapshot(t, svc, id); snap.State != "pending_lock" || snap.StateRevision != 0 {
		t.Fatalf("lock partially applied: %s rev %d", snap.State, snap.StateRevision)
	}
	if tokens, _ := svc.Store().LoadTokens(context.Background(), id); len(tokens) != 0 {
		t.Fatalf("partial tokens after failed lock: %d", len(tokens))
	}

	// A clean lock now succeeds and drives to water_spray.
	if _, err := svc.Lock(context.Background(), lockReq(id)); err != nil {
		t.Fatalf("relock: %v", err)
	}
	rev := lockAndInstallRemaining(t, svc, id)
	rev = submitAir(t, svc, id, rev)

	// Spray commit failure leaves no coverage and no state advance.
	fp.FailNextCommits(1)
	_, err = svc.SubmitSprayCheckpoint(context.Background(), SprayCheckpointRequest{
		TaskID: id, OperationID: id + "-spray", ExpectedRevision: rev, CheckpointID: "c1", ActualPa: 100,
	})
	if codeOf(t, err) != codes.StoreUnavailable {
		t.Fatalf("spray fail code = %v", codeOf(t, err))
	}
	if snap := snapshot(t, svc, id); len(snap.SprayCoverage) != 0 || snap.State != "water_spray" {
		t.Fatalf("spray partially applied: coverage=%v state=%s", snap.SprayCoverage, snap.State)
	}

	// Drive to releasable and fail the release commit.
	rev = submitSpray(t, svc, id, rev)
	rev = submitWind(t, svc, id, rev)
	driveReviews(t, svc, id)
	fp.FailNextCommits(1)
	_, err = svc.Release(context.Background(), TerminalRequest{
		TaskID: id, OperationID: id + "-release", ExpectedRevision: snapshot(t, svc, id).StateRevision,
	})
	if codeOf(t, err) != codes.StoreUnavailable {
		t.Fatalf("release fail code = %v", codeOf(t, err))
	}
	if snap := snapshot(t, svc, id); snap.State != "releasable" || snap.Credential != nil {
		t.Fatalf("release partially applied: state=%s cred=%v", snap.State, snap.Credential)
	}
}

func lockAndInstallRemaining(t *testing.T, svc *Service, id string) int64 {
	t.Helper()
	if _, err := svc.ConfirmInstallation(context.Background(), InstallationRequest{
		TaskID: id, OperationID: id + "-install", ExpectedRevision: 1,
		Orientation: "east", Sealed: true, PointsZeroed: true,
	}); err != nil {
		t.Fatalf("install: %v", err)
	}
	return 2
}

func driveReviews(t *testing.T, svc *Service, id string) {
	t.Helper()
	hash, err := svc.VerdictHash(context.Background(), id)
	if err != nil {
		t.Fatalf("verdict hash: %v", err)
	}
	for i, reviewer := range []string{"reviewer-a", "reviewer-b"} {
		if _, err := svc.SubmitReview(context.Background(), ReviewRequest{
			TaskID: id, OperationID: id + "-r" + itoa(i), ReviewerID: reviewer,
			QualificationRevision: "q1", Decision: "pass", VerdictHash: hash,
		}); err != nil {
			t.Fatalf("review: %v", err)
		}
	}
}

func TestRestartRecoversTerminalAndOpenTask(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "windowproof-recover2.db")

	// Seed a catalog with two window units so two tasks can lock independently.
	rev := catalog.DemoRevision()
	rev.RevisionID = "rev-two"
	rev.WindowUnits = append(rev.WindowUnits, catalog.WindowUnit{
		UnitID: "W-E-02", ZoneID: "zone-a", Orientation: "east", ProfileBatch: "p1", GlassBatch: "g1", SealBatch: "s1",
	})

	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	scriptA := NewScriptedInstrument([]Result{{Status: "ok"}, {Status: "rejected"}})
	svcA := New(st, WallClock{}, scriptA)
	svcB := New(st, WallClock{}, StaticInstrument{})
	if err := svcA.SeedCatalog(context.Background(), rev); err != nil {
		t.Fatalf("seed: %v", err)
	}

	lockUnit := func(svc *Service, id, unit, chamber string, mps []string) int64 {
		mustCreate(t, svc, id)
		if _, err := svc.Lock(context.Background(), LockRequest{
			TaskID: id, OperationID: id + "-lock", ExpectedRevision: 0,
			CatalogRevisionID: "rev-two", WindowUnitID: unit,
			ProfileBatch: "p1", GlassBatch: "g1", SealBatch: "s1",
			PlanID: "plan-1", ChamberID: chamber, MeasurementPointIDs: mps,
		}); err != nil {
			t.Fatalf("lock %s: %v", id, err)
		}
		if _, err := svc.ConfirmInstallation(context.Background(), InstallationRequest{
			TaskID: id, OperationID: id + "-install", ExpectedRevision: 1,
			Orientation: "east", Sealed: true, PointsZeroed: true,
		}); err != nil {
			t.Fatalf("install %s: %v", id, err)
		}
		return 2
	}

	// Task A (open): partial prefix, failed attempt, evidence.
	idA := nextTaskID()
	revA := lockUnit(svcA, idA, "W-E-01", "chamber-1", []string{"mp-1", "mp-2"})
	if _, err := svcA.SubmitPressureStep(context.Background(), airReq(idA, idA+"-neg", revA, "negative", -100)); err != nil {
		t.Fatalf("air neg: %v", err)
	}
	if _, err := svcA.SubmitPressureStep(context.Background(), airReq(idA, idA+"-pos", revA+1, "positive", 100)); codeOf(t, err) != codes.InstrumentRejected {
		t.Fatalf("air pos should be rejected, got %v", err)
	}
	if _, err := svcA.AddDefect(context.Background(), DefectRequest{
		TaskID: idA, OperationID: idA + "-defect", ExpectedRevision: revA + 1, DefectKind: "water_leak", WindowUnitID: "W-E-01",
	}); err != nil {
		t.Fatalf("defect A: %v", err)
	}

	// Task B (terminal): fully released.
	idB := nextTaskID()
	revB := lockUnit(svcB, idB, "W-E-02", "chamber-2", []string{"mp-3", "mp-4"})
	revB = submitAir(t, svcB, idB, revB)
	revB = submitSpray(t, svcB, idB, revB)
	revB = submitWind(t, svcB, idB, revB)
	driveReviews(t, svcB, idB)
	if _, err := svcB.Release(context.Background(), TerminalRequest{
		TaskID: idB, OperationID: idB + "-release", ExpectedRevision: snapshot(t, svcB, idB).StateRevision,
	}); err != nil {
		t.Fatalf("release B: %v", err)
	}
	st.Close()

	// Reopen and recover.
	st2, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { st2.Close() })
	if err := st2.Recover(context.Background()); err != nil {
		t.Fatalf("recover: %v", err)
	}
	svc := New(st2, WallClock{}, StaticInstrument{})

	snapA := snapshot(t, svc, idA)
	if snapA.State != "air_loading" || snapA.Generation != 1 {
		t.Fatalf("task A state=%s gen=%d", snapA.State, snapA.Generation)
	}
	if len(snapA.StepRecords) != 1 || snapA.StepRecords[0].Polarity != "negative" {
		t.Fatalf("task A prefix not recovered: %+v", snapA.StepRecords)
	}
	failedA := 0
	for _, a := range snapA.Attempts {
		if a.Status == "failed" {
			failedA++
		}
	}
	if failedA != 1 {
		t.Fatalf("task A failed attempts = %d, want 1", failedA)
	}
	if len(snapA.Evidence) != 1 || snapA.Evidence[0].Generation != 1 {
		t.Fatalf("task A evidence not recovered: %+v", snapA.Evidence)
	}

	snapB := snapshot(t, svc, idB)
	if snapB.State != "released" {
		t.Fatalf("task B state = %s, want released", snapB.State)
	}
	if snapB.Credential == nil || snapB.Credential.SerialNumber == "" {
		t.Fatalf("task B credential not recovered")
	}
	if len(snapB.StepRecords) != 3 {
		t.Fatalf("task B steps = %d, want 3", len(snapB.StepRecords))
	}
	if len(snapB.SprayCoverage) != 1 {
		t.Fatalf("task B coverage = %d, want 1", len(snapB.SprayCoverage))
	}
}
