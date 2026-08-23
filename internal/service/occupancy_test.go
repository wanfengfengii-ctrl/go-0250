package service

import (
	"context"
	"sync"
	"testing"

	"github.com/windowproof/fenestration/internal/codes"
	"github.com/windowproof/fenestration/internal/occupancy"
)

func TestConcurrentLockContention(t *testing.T) {
	svc, _ := newTestService(t, StaticInstrument{})
	id1, id2 := nextTaskID(), nextTaskID()
	mustCreate(t, svc, id1)
	mustCreate(t, svc, id2)

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]error, 2)
	ids := []string{id1, id2}
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := svc.Lock(context.Background(), lockReq(ids[i]))
			results[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	ok := 0
	loser := ""
	for i, err := range results {
		if err == nil {
			ok++
			continue
		}
		if codeOf(t, err) != codes.OccupancyConflict {
			t.Fatalf("loser code = %v, want OCCUPANCY_CONFLICT", codeOf(t, err))
		}
		loser = ids[i]
	}
	if ok != 1 {
		t.Fatalf("exactly one task should win, got %d", ok)
	}
	// The loser must hold zero occupancy.
	tokens, err := svc.Store().LoadTokens(context.Background(), loser)
	if err != nil {
		t.Fatalf("load tokens: %v", err)
	}
	if len(tokens) != 0 {
		t.Fatalf("loser has %d tokens, want 0", len(tokens))
	}
}

func TestConcurrentReplaceChamberAndLoad(t *testing.T) {
	svc, _ := newTestService(t, StaticInstrument{})
	id := nextTaskID()
	mustCreate(t, svc, id)
	if _, err := svc.Lock(context.Background(), lockReq(id)); err != nil {
		t.Fatalf("lock: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var installErr, replaceErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, installErr = svc.ConfirmInstallation(context.Background(), InstallationRequest{
			TaskID: id, OperationID: id + "-install", ExpectedRevision: 1,
			Orientation: "east", Sealed: true, PointsZeroed: true,
		})
	}()
	go func() {
		defer wg.Done()
		<-start
		_, replaceErr = svc.ReplaceChamber(context.Background(), ReplaceChamberRequest{
			TaskID: id, OperationID: id + "-replace", ExpectedRevision: 1, NewChamberID: "chamber-2",
		})
	}()
	close(start)
	wg.Wait()

	// Exactly one active chamber token must remain, and the old chamber is never
	// double-released into a conflicting state.
	tokens, err := svc.Store().LoadTokens(context.Background(), id)
	if err != nil {
		t.Fatalf("load tokens: %v", err)
	}
	activeChambers := 0
	for _, tk := range tokens {
		if tk.ResourceKind == occupancy.ResourceChamber && tk.Active {
			activeChambers++
		}
	}
	if activeChambers != 1 {
		t.Fatalf("active chamber tokens = %d, want exactly 1", activeChambers)
	}

	// One of the two operations must have won; the outcome must be consistent.
	snap := snapshot(t, svc, id)
	if installErr == nil {
		if snap.State != "air_loading" {
			t.Fatalf("install won but state = %s", snap.State)
		}
	} else if replaceErr == nil {
		if snap.State != "installing" || snap.ChamberID != "chamber-2" {
			t.Fatalf("replace won but state=%s chamber=%s", snap.State, snap.ChamberID)
		}
	} else {
		t.Fatalf("both operations failed: install=%v replace=%v", installErr, replaceErr)
	}
}
