package service

import (
	"context"
	"testing"

	"github.com/windowproof/fenestration/internal/acquisition"
	"github.com/windowproof/fenestration/internal/catalog"
	"github.com/windowproof/fenestration/internal/codes"
	"github.com/windowproof/fenestration/internal/store"
)

type interruptedPressureInstrument struct {
	result    Result
	callError error
	calls     int
}

func (i *interruptedPressureInstrument) Call(_ context.Context, _ string) (Result, error) {
	i.calls++
	if i.calls == 1 {
		return i.result, i.callError
	}
	return Result{Status: "ok", Payload: "retry-ok"}, nil
}

type failedAttemptWriteFailStore struct {
	store.Store
	failed bool
}

func (s *failedAttemptWriteFailStore) Write(ctx context.Context, fn func(store.Tx) error) error {
	return s.Store.Write(ctx, func(tx store.Tx) error {
		return fn(&failedAttemptWriteFailTx{Tx: tx, parent: s})
	})
}

type failedAttemptWriteFailTx struct {
	store.Tx
	parent *failedAttemptWriteFailStore
}

func (tx *failedAttemptWriteFailTx) SaveAttempt(ctx context.Context, attempt acquisition.InstrumentAttempt) error {
	if attempt.Status == acquisition.AttemptFailed && !tx.parent.failed {
		tx.parent.failed = true
		return context.Canceled
	}
	return tx.Tx.SaveAttempt(ctx, attempt)
}

func TestModel_PressureCancellationLeavesNoUnretryableAttempt(t *testing.T) {
	cases := []struct {
		name      string
		result    Result
		callError error
	}{
		{
			name:      "failed_attempt_status_write_interrupted",
			result:    Result{Status: "ok", Payload: "device-ok"},
			callError: context.Canceled,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			instrument := &interruptedPressureInstrument{
				result:    tc.result,
				callError: tc.callError,
			}
			baseStore, err := store.Open(":memory:")
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			t.Cleanup(func() { baseStore.Close() })
			svc := New(&failedAttemptWriteFailStore{Store: baseStore}, &fakeClock{}, instrument)
			if err := svc.SeedCatalog(context.Background(), catalog.DemoRevision()); err != nil {
				t.Fatalf("seed catalog: %v", err)
			}
			id := nextTaskID()
			rev := lockAndInstall(t, svc, id)
			req := airReq(id, id+"-pressure-cancel", rev, "negative", -100)

			_, err = svc.SubmitPressureStep(context.Background(), req)
			if err == nil {
				t.Fatalf("cancelled pressure submission unexpectedly succeeded")
			}
			if got := codeOf(t, err); got != codes.StoreUnavailable {
				t.Fatalf("cancelled pressure code = %v", got)
			}

			snap := snapshot(t, svc, id)
			if snap.State != "air_loading" || snap.StateRevision != rev {
				t.Fatalf("cancelled pressure changed task state to %s rev %d, want air_loading rev %d", snap.State, snap.StateRevision, rev)
			}
			if len(snap.StepRecords) != 0 {
				t.Fatalf("cancelled pressure recorded %d pressure steps, want 0", len(snap.StepRecords))
			}

			for _, attempt := range snap.Attempts {
				t.Fatalf("cancelled pressure left attempt %s with status %s", attempt.AttemptID, attempt.Status)
			}

			if _, err := svc.SubmitPressureStep(context.Background(), req); err != nil {
				t.Fatalf("resubmitting rolled-back pressure operation: %v", err)
			}

			after := snapshot(t, svc, id)
			if len(after.StepRecords) != 1 {
				t.Fatalf("pressure retry recorded %d steps, want 1", len(after.StepRecords))
			}
			if after.StepRecords[0].Phase != "air" || after.StepRecords[0].Polarity != "negative" || after.StepRecords[0].Ordinal != 1 {
				t.Fatalf("pressure retry recorded wrong step: %+v", after.StepRecords[0])
			}
			if after.State != "air_loading" || after.StateRevision != rev+1 {
				t.Fatalf("pressure retry left state %s rev %d, want air_loading rev %d", after.State, after.StateRevision, rev+1)
			}
			for _, attempt := range after.Attempts {
				if attempt.Status == "pending" {
					t.Fatalf("pressure retry left pending attempt %s", attempt.AttemptID)
				}
			}
		})
	}
}
