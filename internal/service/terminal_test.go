package service

import (
	"context"
	"sync"
	"testing"

	"github.com/windowproof/fenestration/internal/codes"
)

func driveToReleasable(t *testing.T, svc *Service, id string) {
	t.Helper()
	driveToPendingReview(t, svc, id)
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

func TestTerminalRaceSingleWinner(t *testing.T) {
	for round := 0; round < 5; round++ {
		svc, _ := newTestService(t, StaticInstrument{})
		id := nextTaskID()
		driveToReleasable(t, svc, id)
		rev := snapshot(t, svc, id).StateRevision

		type op struct {
			name string
			fn   func() error
		}
		ops := []op{
			{"release", func() error {
				_, err := svc.Release(context.Background(), TerminalRequest{TaskID: id, OperationID: id + "-rel", ExpectedRevision: rev})
				return err
			}},
			{"quarantine", func() error {
				_, err := svc.Quarantine(context.Background(), TerminalRequest{TaskID: id, OperationID: id + "-q", ExpectedRevision: rev})
				return err
			}},
			{"cancel", func() error {
				_, err := svc.Cancel(context.Background(), TerminalRequest{TaskID: id, OperationID: id + "-c", ExpectedRevision: rev})
				return err
			}},
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, len(ops))
		for i := range ops {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				errs[i] = ops[i].fn()
			}(i)
		}
		close(start)
		wg.Wait()

		winners := 0
		for i, err := range errs {
			if err == nil {
				winners++
				continue
			}
			if codeOf(t, err) != codes.TerminalReached {
				t.Fatalf("round %d %s code = %v, want TERMINAL_REACHED", round, ops[i].name, codeOf(t, err))
			}
		}
		if winners != 1 {
			t.Fatalf("round %d: %d winners, want exactly 1", round, winners)
		}
		snap := snapshot(t, svc, id)
		switch snap.State {
		case "released", "quarantined", "cancelled":
		default:
			t.Fatalf("round %d: state = %s, want terminal", round, snap.State)
		}
	}
}

func TestPostTerminalOperationsRejected(t *testing.T) {
	svc, _ := newTestService(t, StaticInstrument{})
	id := nextTaskID()
	driveToReleasable(t, svc, id)
	rev := snapshot(t, svc, id).StateRevision
	if _, err := svc.Release(context.Background(), TerminalRequest{TaskID: id, OperationID: id + "-rel", ExpectedRevision: rev}); err != nil {
		t.Fatalf("release: %v", err)
	}

	before := snapshot(t, svc, id)
	beforeRecords := len(before.StepRecords)

	attempts := []struct {
		name string
		fn   func() error
	}{
		{"pressure", func() error {
			_, err := svc.SubmitPressureStep(context.Background(), PressureStepRequest{
				TaskID: id, OperationID: id + "-p", ExpectedRevision: before.StateRevision,
				Phase: "wind", Polarity: "positive", Ordinal: 1, ActualPa: 1000, DisplacementMicron: 10,
			})
			return err
		}},
		{"spray", func() error {
			_, err := svc.SubmitSprayCheckpoint(context.Background(), SprayCheckpointRequest{
				TaskID: id, OperationID: id + "-s", ExpectedRevision: before.StateRevision, CheckpointID: "c1", ActualPa: 100,
			})
			return err
		}},
		{"defect", func() error {
			_, err := svc.AddDefect(context.Background(), DefectRequest{
				TaskID: id, OperationID: id + "-d", ExpectedRevision: before.StateRevision, DefectKind: "water_leak",
			})
			return err
		}},
		{"repair", func() error {
			_, err := svc.StartRepair(context.Background(), RepairRequest{
				TaskID: id, OperationID: id + "-r", ExpectedRevision: before.StateRevision, AffectedCheckpoints: []string{"c1"},
			})
			return err
		}},
		{"install", func() error {
			_, err := svc.ConfirmInstallation(context.Background(), InstallationRequest{
				TaskID: id, OperationID: id + "-i", ExpectedRevision: before.StateRevision, Orientation: "east", Sealed: true, PointsZeroed: true,
			})
			return err
		}},
		{"release", func() error {
			_, err := svc.Release(context.Background(), TerminalRequest{TaskID: id, OperationID: id + "-rel2", ExpectedRevision: before.StateRevision})
			return err
		}},
	}
	for _, a := range attempts {
		if codeOf(t, a.fn()) != codes.TerminalReached {
			t.Fatalf("%s: expected TERMINAL_REACHED", a.name)
		}
	}

	after := snapshot(t, svc, id)
	if after.State != before.State || after.StateRevision != before.StateRevision {
		t.Fatalf("state changed post-terminal: %s/%d -> %s/%d", before.State, before.StateRevision, after.State, after.StateRevision)
	}
	if len(after.StepRecords) != beforeRecords {
		t.Fatalf("records changed post-terminal: %d -> %d", beforeRecords, len(after.StepRecords))
	}
	tokens, _ := svc.Store().LoadTokens(context.Background(), id)
	active := 0
	for _, tk := range tokens {
		if tk.Active {
			active++
		}
	}
	if active != 0 {
		t.Fatalf("active tokens post-terminal = %d, want 0", active)
	}
}
