package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/windowproof/fenestration/internal/catalog"
	"github.com/windowproof/fenestration/internal/codes"
	"github.com/windowproof/fenestration/internal/store"
)

// fakeClock is a controllable monotonic clock for deterministic time-window tests.
type fakeClock struct {
	mu sync.Mutex
	t  int64
}

func (c *fakeClock) NowMillis() int64 { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) set(ms int64)     { c.mu.Lock(); c.t = ms; c.mu.Unlock() }

var taskSeq int

func nextTaskID() string { taskSeq++; return fmt.Sprintf("task-%d", taskSeq) }

func newTestService(t *testing.T, instrument Instrument) (*Service, *fakeClock) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	clock := &fakeClock{}
	svc := New(st, clock, instrument)
	if err := svc.SeedCatalog(context.Background(), catalog.DemoRevision()); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	return svc, clock
}

func mustCreate(t *testing.T, svc *Service, taskID string) int64 {
	t.Helper()
	_, err := svc.CreateTask(context.Background(), CreateTaskRequest{TaskID: taskID, OperationID: taskID + "-create"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return 0
}

func lockReq(taskID string) LockRequest {
	return LockRequest{
		TaskID: taskID, OperationID: taskID + "-lock", ExpectedRevision: 0,
		CatalogRevisionID: "rev-demo", WindowUnitID: "W-E-01",
		ProfileBatch: "p1", GlassBatch: "g1", SealBatch: "s1",
		PlanID: "plan-1", ChamberID: "chamber-1", MeasurementPointIDs: []string{"mp-1", "mp-2"},
	}
}

// lockAndInstall drives a task through lock and installation, returning the new
// state revision (after installation).
func lockAndInstall(t *testing.T, svc *Service, taskID string) int64 {
	t.Helper()
	mustCreate(t, svc, taskID)
	req := lockReq(taskID)
	if _, err := svc.Lock(context.Background(), req); err != nil {
		t.Fatalf("lock: %v", err)
	}
	_, err := svc.ConfirmInstallation(context.Background(), InstallationRequest{
		TaskID: taskID, OperationID: taskID + "-install", ExpectedRevision: 1,
		Orientation: "east", Sealed: true, PointsZeroed: true,
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	return 2
}

func submitAir(t *testing.T, svc *Service, taskID string, rev int64) int64 {
	t.Helper()
	for i, step := range []struct {
		polarity string
		pa       int64
	}{
		{"negative", -100},
		{"positive", 100},
	} {
		_, err := svc.SubmitPressureStep(context.Background(), PressureStepRequest{
			TaskID: taskID, OperationID: fmt.Sprintf("%s-air-%d", taskID, i), ExpectedRevision: rev,
			Phase: "air", Polarity: step.polarity, Ordinal: 1, ActualPa: step.pa, AirflowCCPerSec: 10,
		})
		if err != nil {
			t.Fatalf("air step %s: %v", step.polarity, err)
		}
		rev++
	}
	return rev
}

func submitSpray(t *testing.T, svc *Service, taskID string, rev int64) int64 {
	t.Helper()
	_, err := svc.SubmitSprayCheckpoint(context.Background(), SprayCheckpointRequest{
		TaskID: taskID, OperationID: taskID + "-spray", ExpectedRevision: rev,
		CheckpointID: "c1", ActualPa: 100,
	})
	if err != nil {
		t.Fatalf("spray: %v", err)
	}
	return rev + 1
}

func submitWind(t *testing.T, svc *Service, taskID string, rev int64) int64 {
	t.Helper()
	_, err := svc.SubmitPressureStep(context.Background(), PressureStepRequest{
		TaskID: taskID, OperationID: taskID + "-wind", ExpectedRevision: rev,
		Phase: "wind", Polarity: "positive", Ordinal: 1, ActualPa: 1000, DisplacementMicron: 100,
	})
	if err != nil {
		t.Fatalf("wind: %v", err)
	}
	return rev + 1
}

func asServiceError(t *testing.T, err error) *ServiceError {
	t.Helper()
	se, ok := err.(*ServiceError)
	if !ok {
		t.Fatalf("expected *ServiceError, got %T: %v", err, err)
	}
	return se
}

func codeOf(t *testing.T, err error) codes.Code {
	t.Helper()
	return asServiceError(t, err).Code
}

func decodeResponse(t *testing.T, body []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(body, dst); err != nil {
		t.Fatalf("decode response %s: %v", body, err)
	}
}

func snapshot(t *testing.T, svc *Service, taskID string) *Snapshot {
	t.Helper()
	snap, err := svc.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	return snap
}

// storeOpen opens an in-memory store without seeding any catalog, for tests
// that seed a custom revision.
func storeOpen() (store.Store, error) {
	return store.Open(":memory:")
}
