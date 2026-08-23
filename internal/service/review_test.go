package service

import (
	"context"
	"testing"

	"github.com/windowproof/fenestration/internal/codes"
)

func driveToPendingReview(t *testing.T, svc *Service, id string) int64 {
	t.Helper()
	rev := lockAndInstall(t, svc, id)
	rev = submitAir(t, svc, id, rev)
	rev = submitSpray(t, svc, id, rev)
	return submitWind(t, svc, id, rev)
}

func TestReviewQualificationIdentityAndVerdict(t *testing.T) {
	svc, _ := newTestService(t, StaticInstrument{})
	id := nextTaskID()
	driveToPendingReview(t, svc, id)

	hash, err := svc.VerdictHash(context.Background(), id)
	if err != nil {
		t.Fatalf("verdict hash: %v", err)
	}

	reviewReq := func(reviewer, qual, op, vhash string) ReviewRequest {
		return ReviewRequest{TaskID: id, OperationID: op, ReviewerID: reviewer, QualificationRevision: qual, Decision: "pass", VerdictHash: vhash}
	}

	// Expired/invalid qualification is rejected.
	_, err = svc.SubmitReview(context.Background(), reviewReq("reviewer-a", "q-expired", id+"-r0", hash))
	if codeOf(t, err) != codes.QualificationInvalid {
		t.Fatalf("expired qual code = %v", codeOf(t, err))
	}

	// Verdict mismatch is rejected.
	_, err = svc.SubmitReview(context.Background(), reviewReq("reviewer-a", "q1", id+"-r1", "wrong-hash"))
	if codeOf(t, err) != codes.VerdictMismatch {
		t.Fatalf("verdict mismatch code = %v", codeOf(t, err))
	}

	// First valid review.
	if _, err := svc.SubmitReview(context.Background(), reviewReq("reviewer-a", "q1", id+"-r2", hash)); err != nil {
		t.Fatalf("review a: %v", err)
	}
	if snap := snapshot(t, svc, id); snap.State != "pending_review" {
		t.Fatalf("after one review state = %s", snap.State)
	}

	// Same reviewer again is rejected.
	_, err = svc.SubmitReview(context.Background(), reviewReq("reviewer-a", "q1", id+"-r3", hash))
	if codeOf(t, err) != codes.ReviewConflict {
		t.Fatalf("duplicate reviewer code = %v", codeOf(t, err))
	}

	// Second distinct qualified reviewer advances to releasable.
	if _, err := svc.SubmitReview(context.Background(), reviewReq("reviewer-b", "q1", id+"-r4", hash)); err != nil {
		t.Fatalf("review b: %v", err)
	}
	if snap := snapshot(t, svc, id); snap.State != "releasable" {
		t.Fatalf("after two reviews state = %s, want releasable", snap.State)
	}
}

// TestRepairGenerationFreesReviewSeats reproduces the reported defect: after a
// task reaches releasable, a repair that increments the generation changes the
// verdict hash, so the two reviewers must be able to re-submit against the new
// conclusion. Previously the review seats were keyed by (task_id, reviewer_id)
// across the whole task lifetime, so re-submitting returned REVIEW_CONFLICT
// ("seat already occupied") and the task could never return to releasable.
// Seats are now scoped per generation, mirroring the evidence/step/spray
// records and the generation-bound verdict hash.
func TestRepairGenerationFreesReviewSeats(t *testing.T) {
	svc, clock := newTestService(t, StaticInstrument{})
	id := nextTaskID()
	driveToReleasable(t, svc, id)
	clock.set(0)

	before := snapshot(t, svc, id)
	if before.State != "releasable" {
		t.Fatalf("state = %s, want releasable", before.State)
	}
	oldGen := before.Generation

	// A defect found during the released/reviewable conclusion triggers a repair
	// that reopens only the affected water checkpoint and bumps the generation.
	dbody, err := svc.AddDefect(context.Background(), DefectRequest{
		TaskID: id, OperationID: id + "-defect2", ExpectedRevision: before.StateRevision,
		DefectKind: "water_leak", WindowUnitID: "W-E-01", Observation: "post-release leak",
	})
	if err != nil {
		t.Fatalf("defect: %v", err)
	}
	var dresp DefectResponse
	decodeResponse(t, dbody, &dresp)

	_, err = svc.StartRepair(context.Background(), RepairRequest{
		TaskID: id, OperationID: id + "-repair2", ExpectedRevision: dresp.StateRevision,
		AffectedSteps:       []string{"wind/positive/1"},
		AffectedCheckpoints: []string{"c1"},
		ReasonEvidenceIDs:   []string{dresp.EvidenceID},
	})
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	repairSnap := snapshot(t, svc, id)
	if repairSnap.Generation != oldGen+1 {
		t.Fatalf("generation = %d, want %d", repairSnap.Generation, oldGen+1)
	}
	if repairSnap.State != "water_spray" {
		t.Fatalf("state = %s, want water_spray", repairSnap.State)
	}
	// The affected wind step is reopened in the new generation (air steps stay).
	if len(repairSnap.StepRecords) != 2 {
		t.Fatalf("air steps not preserved: %d", len(repairSnap.StepRecords))
	}
	// Prior-generation seats must not appear in the current snapshot.
	if len(repairSnap.Reviews) != 0 {
		t.Fatalf("prior-generation reviews carried over: %+v", repairSnap.Reviews)
	}

	// Re-drive the reopened obligations back to pending_review with fresh
	// operation ids (operation_results are keyed by operation_id across the
	// task lifetime, so the replayed ids would otherwise collide).
	rev := repairSnap.StateRevision
	if _, err := svc.SubmitSprayCheckpoint(context.Background(), SprayCheckpointRequest{
		TaskID: id, OperationID: id + "-spray2", ExpectedRevision: rev, CheckpointID: "c1", ActualPa: 100,
	}); err != nil {
		t.Fatalf("spray after repair: %v", err)
	}
	sprayRev := snapshot(t, svc, id).StateRevision
	if _, err := svc.SubmitPressureStep(context.Background(), PressureStepRequest{
		TaskID: id, OperationID: id + "-wind2", ExpectedRevision: sprayRev,
		Phase: "wind", Polarity: "positive", Ordinal: 1, ActualPa: 1000, DisplacementMicron: 100,
	}); err != nil {
		t.Fatalf("wind after repair: %v", err)
	}
	if snap := snapshot(t, svc, id); snap.State != "pending_review" {
		t.Fatalf("state = %s, want pending_review after repair", snap.State)
	}

	// The new conclusion differs from the prior generation's because the
	// generation (and reopened evidence) changed, so the verdict hash must be
	// re-fetched for the new generation.
	newHash, err := svc.VerdictHash(context.Background(), id)
	if err != nil {
		t.Fatalf("verdict hash: %v", err)
	}

	// The same two reviewers must be able to re-submit against the new verdict.
	for i, reviewer := range []string{"reviewer-a", "reviewer-b"} {
		if _, err := svc.SubmitReview(context.Background(), ReviewRequest{
			TaskID: id, OperationID: id + "-r2-" + itoa(i), ReviewerID: reviewer,
			QualificationRevision: "q1", Decision: "pass", VerdictHash: newHash,
		}); err != nil {
			t.Fatalf("re-review %s: %v", reviewer, err)
		}
	}
	after := snapshot(t, svc, id)
	if after.State != "releasable" {
		t.Fatalf("state = %s, want releasable after re-review", after.State)
	}
	// Exactly the two current-generation seats are reported.
	if len(after.Reviews) != 2 {
		t.Fatalf("reviews = %d, want 2", len(after.Reviews))
	}

	// Close the defect so the final release succeeds, then release.
	cbody, err := svc.CloseDefect(context.Background(), CloseDefectRequest{
		TaskID: id, OperationID: id + "-close2", ExpectedRevision: after.StateRevision,
		EvidenceID: dresp.EvidenceID,
	})
	if err != nil {
		t.Fatalf("close defect: %v", err)
	}
	var cres CloseDefectResponse
	decodeResponse(t, cbody, &cres)
	if _, err := svc.Release(context.Background(), TerminalRequest{
		TaskID: id, OperationID: id + "-release2", ExpectedRevision: cres.StateRevision,
	}); err != nil {
		t.Fatalf("release after repair: %v", err)
	}
	if snap := snapshot(t, svc, id); snap.State != "released" || snap.Credential == nil {
		t.Fatalf("release after repair failed: state=%s cred=%v", snap.State, snap.Credential)
	}
}

func TestReleaseIssuesUniqueCredential(t *testing.T) {
	svc, _ := newTestService(t, StaticInstrument{})
	id := nextTaskID()
	driveToPendingReview(t, svc, id)
	hash, _ := svc.VerdictHash(context.Background(), id)
	for i, reviewer := range []string{"reviewer-a", "reviewer-b"} {
		if _, err := svc.SubmitReview(context.Background(), ReviewRequest{
			TaskID: id, OperationID: id + "-r" + itoa(i), ReviewerID: reviewer,
			QualificationRevision: "q1", Decision: "pass", VerdictHash: hash,
		}); err != nil {
			t.Fatalf("review %s: %v", reviewer, err)
		}
	}
	snap := snapshot(t, svc, id)
	body, err := svc.Release(context.Background(), TerminalRequest{
		TaskID: id, OperationID: id + "-release", ExpectedRevision: snap.StateRevision,
	})
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	var resp TerminalResponse
	decodeResponse(t, body, &resp)
	if resp.Credential == nil || resp.Credential.SerialNumber == "" {
		t.Fatalf("release did not produce a credential: %+v", resp)
	}
	after := snapshot(t, svc, id)
	if after.State != "released" {
		t.Fatalf("state = %s, want released", after.State)
	}
	if after.Credential == nil || after.Credential.SerialNumber != resp.Credential.SerialNumber {
		t.Fatalf("credential not persisted")
	}
}
