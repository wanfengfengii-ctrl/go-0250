package service

import (
	"context"
	"testing"

	"github.com/windowproof/fenestration/internal/codes"
)

func TestModel_AirFailurePhaseFence(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "failed final level can be retried without advancing",
			run: func(t *testing.T) {
				svc, _ := newTestService(t, StaticInstrument{})
				id := nextTaskID()
				rev := lockAndInstall(t, svc, id)

				if _, err := svc.SubmitPressureStep(context.Background(), airReq(id, id+"-negative", rev, "negative", -100)); err != nil {
					t.Fatalf("negative level: %v", err)
				}
				rev++

				_, err := svc.SubmitPressureStep(context.Background(), airReq(id, id+"-out-of-tolerance", rev, "positive", 115))
				if codeOf(t, err) != codes.InvalidRequest {
					t.Fatalf("failed final level code = %v, want %v", codeOf(t, err), codes.InvalidRequest)
				}
				afterFailure := snapshot(t, svc, id)
				if afterFailure.State != "air_loading" || afterFailure.CurrentPhase != "air" {
					t.Fatalf("failed final level advanced task to %s/%s", afterFailure.State, afterFailure.CurrentPhase)
				}
				if afterFailure.StateRevision != rev || len(afterFailure.StepRecords) != 1 {
					t.Fatalf("failed final level changed revision/records: revision=%d records=%d", afterFailure.StateRevision, len(afterFailure.StepRecords))
				}

				if _, err := svc.SubmitPressureStep(context.Background(), airReq(id, id+"-positive-retry", rev, "positive", 100)); err != nil {
					t.Fatalf("retry final level: %v", err)
				}
				afterRetry := snapshot(t, svc, id)
				if afterRetry.State != "water_spray" || len(afterRetry.StepRecords) != 2 {
					t.Fatalf("successful retry did not complete air phase: state=%s records=%d", afterRetry.State, len(afterRetry.StepRecords))
				}
			},
		},
		{
			name: "failed final level fences every downstream command",
			run: func(t *testing.T) {
				svc, _ := newTestService(t, StaticInstrument{})
				id := nextTaskID()
				rev := lockAndInstall(t, svc, id)

				if _, err := svc.SubmitPressureStep(context.Background(), airReq(id, id+"-negative", rev, "negative", -100)); err != nil {
					t.Fatalf("negative level: %v", err)
				}
				rev++
				if _, err := svc.SubmitPressureStep(context.Background(), airReq(id, id+"-out-of-tolerance", rev, "positive", 115)); codeOf(t, err) != codes.InvalidRequest {
					t.Fatalf("failed final level code = %v, want %v", codeOf(t, err), codes.InvalidRequest)
				}

				attempts := []struct {
					name string
					call func() error
				}{
					{name: "spray", call: func() error {
						_, err := svc.SubmitSprayCheckpoint(context.Background(), SprayCheckpointRequest{
							TaskID: id, OperationID: id + "-spray-after-failure", ExpectedRevision: rev,
							CheckpointID: "c1", ActualPa: 100,
						})
						return err
					}},
					{name: "wind", call: func() error {
						_, err := svc.SubmitPressureStep(context.Background(), PressureStepRequest{
							TaskID: id, OperationID: id + "-wind-after-failure", ExpectedRevision: rev,
							Phase: "wind", Polarity: "positive", Ordinal: 1, ActualPa: 1000, DisplacementMicron: 100,
						})
						return err
					}},
					{name: "review", call: func() error {
						_, err := svc.SubmitReview(context.Background(), ReviewRequest{
							TaskID: id, OperationID: id + "-review-after-failure", ReviewerID: "reviewer-a",
							QualificationRevision: "q1", Decision: "pass", VerdictHash: "unearned",
						})
						return err
					}},
					{name: "release", call: func() error {
						_, err := svc.Release(context.Background(), TerminalRequest{
							TaskID: id, OperationID: id + "-release-after-failure", ExpectedRevision: rev,
						})
						return err
					}},
				}
				for _, attempt := range attempts {
					t.Run(attempt.name, func(t *testing.T) {
						if err := attempt.call(); err == nil {
							t.Fatalf("%s succeeded after a failed air level", attempt.name)
						}
					})
				}

				after := snapshot(t, svc, id)
				if after.State != "air_loading" || after.CurrentPhase != "air" || after.Credential != nil {
					t.Fatalf("downstream commands escaped failed-level fence: state=%s phase=%s credential=%v", after.State, after.CurrentPhase, after.Credential)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}
