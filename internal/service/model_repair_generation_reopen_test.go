package service

import (
	"context"
	"reflect"
	"testing"
)

func TestModel_RepairGenerationReopensSelectedPressureStep(t *testing.T) {
	tests := []struct {
		name             string
		affectedStep     string
		reopenedPolarity string
		reopenedPa       int64
	}{
		{
			name:             "last air positive pressure step",
			affectedStep:     "air/positive/1",
			reopenedPolarity: "positive",
			reopenedPa:       105,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newTestService(t, StaticInstrument{})
			taskID := nextTaskID()
			rev := lockAndInstall(t, svc, taskID)
			rev = submitAir(t, svc, taskID, rev)

			oldGenerationRecords, err := svc.Store().LoadStepRecords(context.Background(), taskID, 1)
			if err != nil {
				t.Fatalf("load old generation records: %v", err)
			}
			if len(oldGenerationRecords) != 2 {
				t.Fatalf("old generation records = %d, want 2", len(oldGenerationRecords))
			}

			defectBody, err := svc.AddDefect(context.Background(), DefectRequest{
				TaskID:           taskID,
				OperationID:      taskID + "-seal-defect",
				ExpectedRevision: rev,
				DefectKind:       "seal_failure",
				WindowUnitID:     "W-E-01",
				PressureOrdinal:  1,
				Observation:      "positive pressure seal failed",
			})
			if err != nil {
				t.Fatalf("defect: %v", err)
			}
			var defect DefectResponse
			decodeResponse(t, defectBody, &defect)

			if _, err := svc.StartRepair(context.Background(), RepairRequest{
				TaskID:            taskID,
				OperationID:       taskID + "-repair-positive",
				ExpectedRevision:  defect.StateRevision,
				AffectedSteps:     []string{tc.affectedStep},
				ReasonEvidenceIDs: []string{defect.EvidenceID},
			}); err != nil {
				t.Fatalf("repair: %v", err)
			}

			reopened := snapshot(t, svc, taskID)
			if reopened.Generation != 2 {
				t.Errorf("generation = %d, want 2", reopened.Generation)
			}
			if reopened.State != "air_loading" {
				t.Errorf("state after repair = %s, want air_loading", reopened.State)
			}
			if len(reopened.StepRecords) != 1 {
				t.Errorf("current generation step records = %d, want only the unaffected negative step", len(reopened.StepRecords))
			} else {
				got := reopened.StepRecords[0]
				if got.Phase != "air" || got.Polarity != "negative" || got.Ordinal != 1 {
					t.Errorf("current generation inherited step = %s/%s/%d, want air/negative/1", got.Phase, got.Polarity, got.Ordinal)
				}
			}

			body, err := svc.SubmitPressureStep(context.Background(), PressureStepRequest{
				TaskID:           taskID,
				OperationID:      taskID + "-air-positive-repair",
				ExpectedRevision: reopened.StateRevision,
				Phase:            "air",
				Polarity:         tc.reopenedPolarity,
				Ordinal:          1,
				ActualPa:         tc.reopenedPa,
				AirflowCCPerSec:  10,
			})
			if err != nil {
				t.Fatalf("reopened positive pressure step: %v", err)
			}
			var pressure PressureStepResponse
			decodeResponse(t, body, &pressure)
			if pressure.State != "water_spray" {
				t.Fatalf("state after reopened pressure step = %s, want water_spray", pressure.State)
			}

			current := snapshot(t, svc, taskID)
			if current.State != "water_spray" {
				t.Fatalf("snapshot state after reopened pressure step = %s, want water_spray", current.State)
			}
			if len(current.StepRecords) != 2 {
				t.Fatalf("current generation records after resubmit = %d, want 2", len(current.StepRecords))
			}
			if got := current.StepRecords[1]; got.Phase != "air" || got.Polarity != "positive" || got.Ordinal != 1 || got.ActualPa != tc.reopenedPa {
				t.Fatalf("reopened step = %+v, want air/positive/1 at %d Pa", got, tc.reopenedPa)
			}

			oldGenerationAfter, err := svc.Store().LoadStepRecords(context.Background(), taskID, 1)
			if err != nil {
				t.Fatalf("reload old generation records: %v", err)
			}
			if !reflect.DeepEqual(oldGenerationAfter, oldGenerationRecords) {
				t.Fatalf("old generation records changed:\nbefore=%+v\nafter=%+v", oldGenerationRecords, oldGenerationAfter)
			}
		})
	}
}
