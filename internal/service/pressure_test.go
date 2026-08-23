package service

import (
	"bytes"
	"context"
	"testing"

	"github.com/windowproof/fenestration/internal/codes"
)

func airReq(taskID string, op string, rev int64, polarity string, pa int64) PressureStepRequest {
	return PressureStepRequest{
		TaskID: taskID, OperationID: op, ExpectedRevision: rev,
		Phase: "air", Polarity: polarity, Ordinal: 1, ActualPa: pa, AirflowCCPerSec: 10,
	}
}

func TestPressurePrefixRejectsSkipDuplicateStale(t *testing.T) {
	svc, _ := newTestService(t, StaticInstrument{})
	id := nextTaskID()
	rev := lockAndInstall(t, svc, id) // rev = 2, state air_loading

	// Skip: positive before negative is rejected.
	_, err := svc.SubmitPressureStep(context.Background(), airReq(id, id+"-skip", rev, "positive", 100))
	if codeOf(t, err) != codes.InvalidRequest {
		t.Fatalf("skip code = %v", codeOf(t, err))
	}

	// Valid negative level.
	if _, err := svc.SubmitPressureStep(context.Background(), airReq(id, id+"-neg", rev, "negative", -100)); err != nil {
		t.Fatalf("negative: %v", err)
	}
	rev++

	// Duplicate negative (now cursor is positive) is rejected.
	_, err = svc.SubmitPressureStep(context.Background(), airReq(id, id+"-dup", rev, "negative", -100))
	if codeOf(t, err) != codes.InvalidRequest {
		t.Fatalf("duplicate code = %v", codeOf(t, err))
	}

	// Stale revision on the valid next level.
	_, err = svc.SubmitPressureStep(context.Background(), airReq(id, id+"-stale", rev-1, "positive", 100))
	if codeOf(t, err) != codes.StaleRevision {
		t.Fatalf("stale code = %v", codeOf(t, err))
	}

	// Cursor, record count and state must be unchanged after all rejections.
	snap := snapshot(t, svc, id)
	if len(snap.StepRecords) != 1 {
		t.Fatalf("step records = %d, want 1", len(snap.StepRecords))
	}
	if snap.State != "air_loading" {
		t.Fatalf("state = %s, want air_loading", snap.State)
	}
}

func TestPressureIdempotentReplayAndContentConflict(t *testing.T) {
	svc, _ := newTestService(t, StaticInstrument{})
	id := nextTaskID()
	rev := lockAndInstall(t, svc, id)

	req := airReq(id, id+"-op", rev, "negative", -100)
	first, err := svc.SubmitPressureStep(context.Background(), req)
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	// Identical replay returns byte-identical result.
	second, err := svc.SubmitPressureStep(context.Background(), req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("replay result differs:\nfirst=%s\nsecond=%s", first, second)
	}

	// Same operation id with a different reading returns a stable conflict code.
	altered := req
	altered.ActualPa = -90
	_, err = svc.SubmitPressureStep(context.Background(), altered)
	if codeOf(t, err) != codes.OperationContentConflict {
		t.Fatalf("content conflict code = %v", codeOf(t, err))
	}
}
