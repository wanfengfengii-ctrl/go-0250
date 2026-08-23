package service

import (
	"context"
	"testing"

	"github.com/windowproof/fenestration/internal/catalog"
	"github.com/windowproof/fenestration/internal/codes"
	"github.com/windowproof/fenestration/internal/occupancy"
)

func TestLockSucceedsAndFillsSnapshot(t *testing.T) {
	svc, _ := newTestService(t, StaticInstrument{})
	id := nextTaskID()
	mustCreate(t, svc, id)

	body, err := svc.Lock(context.Background(), lockReq(id))
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	var resp LockResponse
	decodeResponse(t, body, &resp)

	if resp.State != "installing" {
		t.Fatalf("state = %s, want installing", resp.State)
	}
	if resp.StateRevision != 1 {
		t.Fatalf("state_revision = %d, want 1", resp.StateRevision)
	}
	wantHash := catalog.DemoRevision().SnapshotHash()
	if resp.LockedSnapshotHash != wantHash {
		t.Fatalf("snapshot hash = %s, want %s", resp.LockedSnapshotHash, wantHash)
	}
	if resp.WindowUnitID != "W-E-01" {
		t.Fatalf("window unit = %s", resp.WindowUnitID)
	}
	if len(resp.Tokens) != 4 {
		t.Fatalf("tokens = %d, want 4 (unit+chamber+2 points)", len(resp.Tokens))
	}
	// All four resource families must be occupied.
	kinds := map[string]int{}
	for _, tk := range resp.Tokens {
		kinds[tk.ResourceKind]++
	}
	if kinds[string(occupancy.ResourceWindowUnit)] != 1 || kinds[string(occupancy.ResourceChamber)] != 1 || kinds[string(occupancy.ResourceMeasurementPoint)] != 2 {
		t.Fatalf("unexpected token kinds: %v", kinds)
	}
}

func TestLockCatalogMismatchRejected(t *testing.T) {
	svc, _ := newTestService(t, StaticInstrument{})
	id := nextTaskID()
	mustCreate(t, svc, id)

	req := lockReq(id)
	req.OperationID = id + "-lock-bad"
	req.GlassBatch = "g-mismatch"
	_, err := svc.Lock(context.Background(), req)
	if codeOf(t, err) != codes.CatalogMismatch {
		t.Fatalf("code = %v, want CATALOG_MISMATCH", codeOf(t, err))
	}
	se := asServiceError(t, err)
	if len(se.Reasons) == 0 {
		t.Fatalf("expected sorted reasons")
	}
	// Reasons must be sorted by stable key.
	SortReasons(se.Reasons)
	for i := 1; i < len(se.Reasons); i++ {
		if fieldRank(se.Reasons[i].Field) < fieldRank(se.Reasons[i-1].Field) {
			t.Fatalf("reasons not sorted: %v", se.Reasons)
		}
	}
	// The task must not have advanced and must hold no tokens.
	snap := snapshot(t, svc, id)
	if snap.State != "pending_lock" || snap.StateRevision != 0 {
		t.Fatalf("task advanced on mismatch: %s rev %d", snap.State, snap.StateRevision)
	}
	tokens, err := svc.Store().LoadTokens(context.Background(), id)
	if err != nil {
		t.Fatalf("load tokens: %v", err)
	}
	if len(tokens) != 0 {
		t.Fatalf("partial occupancy left behind: %d tokens", len(tokens))
	}
}

func fieldRank(f string) int {
	switch f {
	case "window_unit":
		return 0
	case "pressure_step":
		return 1
	case "measurement_point":
		return 2
	case "checkpoint":
		return 3
	default:
		return 4
	}
}
