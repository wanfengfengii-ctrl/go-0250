package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windowproof/fenestration/internal/api"
	"github.com/windowproof/fenestration/internal/catalog"
	"github.com/windowproof/fenestration/internal/codes"
	"github.com/windowproof/fenestration/internal/service"
	"github.com/windowproof/fenestration/internal/store"
)

func TestModel_RejectedPressureReplayPreservesStatusAndBody(t *testing.T) {
	cases := []struct {
		name             string
		taskID           string
		operationID      string
		expectedRevision int64
		wantStatus       int
		wantCode         codes.Code
	}{
		{
			name:             "stale revision pressure request",
			taskID:           "model-reject-replay-pressure",
			operationID:      "model-reject-replay-pressure-op",
			expectedRevision: 1,
			wantStatus:       http.StatusConflict,
			wantCode:         codes.StaleRevision,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(":memory:")
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			t.Cleanup(func() {
				if err := st.Close(); err != nil {
					t.Fatalf("close store: %v", err)
				}
			})
			svc := service.New(st, nil, service.StaticInstrument{})
			if err := svc.SeedCatalog(context.Background(), catalog.DemoRevision()); err != nil {
				t.Fatalf("seed catalog: %v", err)
			}
			handler := api.NewServer(svc).Handler()

			post := func(path string, body any) (int, []byte) {
				t.Helper()
				payload, err := json.Marshal(body)
				if err != nil {
					t.Fatalf("marshal request: %v", err)
				}
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
				req.Header.Set("Content-Type", "application/json")
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)
				return rr.Code, append([]byte(nil), rr.Body.Bytes()...)
			}

			setupRequests := []struct {
				path string
				body any
			}{
				{
					path: "/api/v1/tasks",
					body: map[string]any{
						"task_id":      tc.taskID,
						"operation_id": tc.taskID + "-create",
					},
				},
				{
					path: "/api/v1/tasks/" + tc.taskID + "/lock",
					body: map[string]any{
						"operation_id":          tc.taskID + "-lock",
						"expected_revision":     0,
						"catalog_revision_id":   "rev-demo",
						"window_unit_id":        "W-E-01",
						"profile_batch":         "p1",
						"glass_batch":           "g1",
						"seal_batch":            "s1",
						"plan_id":               "plan-1",
						"chamber_id":            "chamber-1",
						"measurement_point_ids": []string{"mp-1", "mp-2"},
					},
				},
				{
					path: "/api/v1/tasks/" + tc.taskID + "/installation",
					body: map[string]any{
						"operation_id":      tc.taskID + "-install",
						"expected_revision": 1,
						"orientation":       "east",
						"sealed":            true,
						"points_zeroed":     true,
					},
				},
			}
			for _, req := range setupRequests {
				status, body := post(req.path, req.body)
				if status != http.StatusOK {
					t.Fatalf("setup %s status = %d, body = %s", req.path, status, body)
				}
			}

			pressurePath := "/api/v1/tasks/" + tc.taskID + "/pressure-steps"
			pressureBody := map[string]any{
				"operation_id":       tc.operationID,
				"expected_revision":  tc.expectedRevision,
				"phase":              "air",
				"polarity":           "negative",
				"ordinal":            1,
				"actual_pa":          -100,
				"airflow_cc_per_sec": 10,
			}

			firstStatus, firstBody := post(pressurePath, pressureBody)
			replayStatus, replayBody := post(pressurePath, pressureBody)

			if firstStatus != tc.wantStatus {
				t.Fatalf("first status = %d, want %d; body = %s", firstStatus, tc.wantStatus, firstBody)
			}
			if replayStatus != firstStatus {
				t.Fatalf("replay status = %d, want first status %d; replay body = %s", replayStatus, firstStatus, replayBody)
			}
			if !bytes.Equal(replayBody, firstBody) {
				t.Fatalf("replay body differs:\nfirst=%s\nreplay=%s", firstBody, replayBody)
			}

			var got struct {
				Code          codes.Code `json:"code"`
				StateRevision int64      `json:"state_revision,omitempty"`
				Reasons       []struct {
					Code  codes.Code `json:"code"`
					Field string     `json:"field,omitempty"`
				} `json:"reasons"`
			}
			if err := json.Unmarshal(firstBody, &got); err != nil {
				t.Fatalf("decode first error body: %v", err)
			}
			if got.Code != tc.wantCode {
				t.Fatalf("first code = %s, want %s", got.Code, tc.wantCode)
			}
			if got.Reasons == nil {
				t.Fatalf("first reasons is nil, want deterministic empty array")
			}
		})
	}
}
