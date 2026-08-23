package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/windowproof/fenestration/internal/catalog"
	"github.com/windowproof/fenestration/internal/codes"
	"github.com/windowproof/fenestration/internal/occupancy"
	"github.com/windowproof/fenestration/internal/store"
)

func TestModel_RestartTokenIDsStayUnique(t *testing.T) {
	ctx := context.Background()
	if phase := os.Getenv("WINDOWPROOF_RESTART_TOKEN_PHASE"); phase != "" {
		dbPath := os.Getenv("WINDOWPROOF_RESTART_TOKEN_DB")
		if dbPath == "" {
			t.Fatal("WINDOWPROOF_RESTART_TOKEN_DB is required")
		}
		switch phase {
		case "seed":
			modelSeedRestartTokenDB(t, ctx, dbPath)
		case "disjoint-lock":
			modelLockDisjointResourcesAfterRestart(t, ctx, dbPath)
		case "conflict":
			modelVerifyConflictAfterRestart(t, ctx, dbPath)
		default:
			t.Fatalf("unknown phase %q", phase)
		}
		return
	}

	dbPath := filepath.Join(t.TempDir(), "windowproof-token-restart.db")
	cases := []struct {
		name  string
		phase string
	}{
		{name: "seed open task before restart", phase: "seed"},
		{name: "restart locks disjoint full resource set", phase: "disjoint-lock"},
		{name: "restart reports concrete conflict without partial tokens", phase: "conflict"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run", "^TestModel_RestartTokenIDsStayUnique$")
			cmd.Env = append(os.Environ(),
				"WINDOWPROOF_RESTART_TOKEN_PHASE="+tc.phase,
				"WINDOWPROOF_RESTART_TOKEN_DB="+dbPath,
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("phase %q failed: %v\n%s", tc.phase, err, out)
			}
		})
	}
}

func modelSeedRestartTokenDB(t *testing.T, ctx context.Context, dbPath string) {
	t.Helper()
	svc, closeStore := modelOpenRestartTokenService(t, dbPath)
	defer closeStore()
	if err := svc.SeedCatalog(ctx, modelRestartTokenCatalog()); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	modelCreateTask(t, ctx, svc, "task-before-restart")
	modelLockTask(t, ctx, svc, "task-before-restart", "W-E-01", "chamber-1", []string{"mp-1", "mp-2"})
	modelRequireTaskTokens(t, ctx, svc, "task-before-restart", 4)
}

func modelLockDisjointResourcesAfterRestart(t *testing.T, ctx context.Context, dbPath string) {
	t.Helper()
	svc, closeStore := modelOpenRestartTokenService(t, dbPath)
	defer closeStore()
	if err := svc.Store().Recover(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}
	modelCreateTask(t, ctx, svc, "task-after-restart")
	modelLockTask(t, ctx, svc, "task-after-restart", "W-E-02", "chamber-2", []string{"mp-3", "mp-4"})
	modelRequireTaskTokens(t, ctx, svc, "task-after-restart", 4)
	modelRequireActiveTokenSet(t, ctx, svc, 8, "")
}

func modelVerifyConflictAfterRestart(t *testing.T, ctx context.Context, dbPath string) {
	t.Helper()
	svc, closeStore := modelOpenRestartTokenService(t, dbPath)
	defer closeStore()
	if err := svc.Store().Recover(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}
	modelCreateTask(t, ctx, svc, "task-conflicting-after-restart")
	_, err := svc.Lock(ctx, modelLockRequest("task-conflicting-after-restart", "W-E-03", "chamber-2", []string{"mp-5", "mp-6"}))
	if err == nil {
		t.Fatal("conflicting lock succeeded")
	}
	se := asServiceError(t, err)
	if se.Code != codes.OccupancyConflict {
		t.Fatalf("conflicting lock code = %v, want %v", se.Code, codes.OccupancyConflict)
	}
	if len(se.Reasons) != 1 || se.Reasons[0].Field != string(occupancy.ResourceChamber) || se.Reasons[0].Value != "chamber-2" {
		t.Fatalf("conflicting lock reasons = %+v, want chamber chamber-2", se.Reasons)
	}
	if tokens, err := svc.Store().LoadTokens(ctx, "task-conflicting-after-restart"); err != nil {
		t.Fatalf("load conflicting task tokens: %v", err)
	} else if len(tokens) != 0 {
		t.Fatalf("conflicting task left %d partial tokens, want 0", len(tokens))
	}
	snap := snapshot(t, svc, "task-conflicting-after-restart")
	if snap.State != "pending_lock" || snap.StateRevision != 0 {
		t.Fatalf("conflicting task advanced to state=%s rev=%d", snap.State, snap.StateRevision)
	}
	modelRequireActiveTokenSet(t, ctx, svc, 8, "mp-5")
}

func modelOpenRestartTokenService(t *testing.T, dbPath string) (*Service, func()) {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return New(st, WallClock{}, StaticInstrument{}), func() {
		if err := st.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}
}

func modelRestartTokenCatalog() catalog.CatalogRevision {
	rev := catalog.DemoRevision()
	rev.RevisionID = "rev-token-restart"
	rev.WindowUnits = []catalog.WindowUnit{
		{UnitID: "W-E-01", ZoneID: "zone-a", Orientation: "east", ProfileBatch: "p1", GlassBatch: "g1", SealBatch: "s1"},
		{UnitID: "W-E-02", ZoneID: "zone-a", Orientation: "east", ProfileBatch: "p1", GlassBatch: "g1", SealBatch: "s1"},
		{UnitID: "W-E-03", ZoneID: "zone-a", Orientation: "east", ProfileBatch: "p1", GlassBatch: "g1", SealBatch: "s1"},
	}
	return rev
}

func modelCreateTask(t *testing.T, ctx context.Context, svc *Service, taskID string) {
	t.Helper()
	if _, err := svc.CreateTask(ctx, CreateTaskRequest{TaskID: taskID, OperationID: taskID + "-create"}); err != nil {
		t.Fatalf("create %s: %v", taskID, err)
	}
}

func modelLockTask(t *testing.T, ctx context.Context, svc *Service, taskID, unitID, chamberID string, pointIDs []string) {
	t.Helper()
	if _, err := svc.Lock(ctx, modelLockRequest(taskID, unitID, chamberID, pointIDs)); err != nil {
		t.Fatalf("lock %s: %v", taskID, err)
	}
}

func modelLockRequest(taskID, unitID, chamberID string, pointIDs []string) LockRequest {
	return LockRequest{
		TaskID:              taskID,
		OperationID:         taskID + "-lock",
		ExpectedRevision:    0,
		CatalogRevisionID:   "rev-token-restart",
		WindowUnitID:        unitID,
		ProfileBatch:        "p1",
		GlassBatch:          "g1",
		SealBatch:           "s1",
		PlanID:              "plan-1",
		ChamberID:           chamberID,
		MeasurementPointIDs: append([]string(nil), pointIDs...),
	}
}

func modelRequireTaskTokens(t *testing.T, ctx context.Context, svc *Service, taskID string, want int) {
	t.Helper()
	tokens, err := svc.Store().LoadTokens(ctx, taskID)
	if err != nil {
		t.Fatalf("load tokens for %s: %v", taskID, err)
	}
	if len(tokens) != want {
		t.Fatalf("%s token count = %d, want %d", taskID, len(tokens), want)
	}
	for _, token := range tokens {
		if token.TokenID == "" || !token.Active {
			t.Fatalf("%s has invalid token: %+v", taskID, token)
		}
	}
}

func modelRequireActiveTokenSet(t *testing.T, ctx context.Context, svc *Service, want int, forbiddenResourceID string) {
	t.Helper()
	tokens, err := svc.Store().LoadAllActiveTokens(ctx)
	if err != nil {
		t.Fatalf("load active tokens: %v", err)
	}
	if len(tokens) != want {
		t.Fatalf("active token count = %d, want %d: %+v", len(tokens), want, tokens)
	}
	seenIDs := map[string]bool{}
	for _, token := range tokens {
		if token.TokenID == "" {
			t.Fatalf("active token has empty id: %+v", token)
		}
		if seenIDs[token.TokenID] {
			t.Fatalf("duplicate token id %q in active set: %+v", token.TokenID, tokens)
		}
		seenIDs[token.TokenID] = true
		if forbiddenResourceID != "" && token.ResourceID == forbiddenResourceID {
			t.Fatalf("partial token for %s remained active: %+v", forbiddenResourceID, tokens)
		}
	}
}
