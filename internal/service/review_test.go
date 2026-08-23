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
