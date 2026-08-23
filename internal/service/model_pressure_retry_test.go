package service

import (
	"context"
	"testing"
)

func TestModel_FailedPressureRetryReplacesRecord(t *testing.T) {
	cases := []struct {
		name         string
		failedActual int64
		passedActual int64
	}{
		{
			name:         "corrected_air_negative_level",
			failedActual: -80,
			passedActual: -100,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newTestService(t, StaticInstrument{})
			id := nextTaskID()
			rev := lockAndInstall(t, svc, id)

			first := PressureStepRequest{
				TaskID: id, OperationID: id + "-first", ExpectedRevision: rev,
				Phase: "air", Polarity: "negative", Ordinal: 1,
				ActualPa: tc.failedActual, AirflowCCPerSec: 10,
			}
			firstBody, err := svc.SubmitPressureStep(context.Background(), first)
			if err != nil {
				t.Fatalf("initial below-threshold submission: %v", err)
			}
			var firstResponse PressureStepResponse
			decodeResponse(t, firstBody, &firstResponse)
			if firstResponse.Passed {
				t.Fatalf("initial response passed unexpectedly: %+v", firstResponse)
			}

			failedSnapshot := snapshot(t, svc, id)
			if len(failedSnapshot.StepRecords) != 1 {
				t.Fatalf("after failed reading records = %d, want 1", len(failedSnapshot.StepRecords))
			}
			if failedSnapshot.StepRecords[0].Passed || failedSnapshot.StepRecords[0].ActualPa != tc.failedActual {
				t.Fatalf("failed reading snapshot = %+v", failedSnapshot.StepRecords[0])
			}

			corrected := first
			corrected.OperationID = id + "-corrected"
			corrected.ExpectedRevision = failedSnapshot.StateRevision
			corrected.ActualPa = tc.passedActual
			correctedBody, err := svc.SubmitPressureStep(context.Background(), corrected)
			if err != nil {
				t.Fatalf("corrected retry: %v", err)
			}
			var correctedResponse PressureStepResponse
			decodeResponse(t, correctedBody, &correctedResponse)
			if !correctedResponse.Passed {
				t.Fatalf("corrected response did not pass: %+v", correctedResponse)
			}

			correctedSnapshot := snapshot(t, svc, id)
			if correctedResponse.State != correctedSnapshot.State || correctedResponse.StateRevision != correctedSnapshot.StateRevision {
				t.Fatalf("corrected response state %+v disagrees with GET state=%s revision=%d", correctedResponse, correctedSnapshot.State, correctedSnapshot.StateRevision)
			}
			if len(correctedSnapshot.StepRecords) != 1 {
				t.Fatalf("corrected snapshot records = %d, want one replaced record", len(correctedSnapshot.StepRecords))
			}
			record := correctedSnapshot.StepRecords[0]
			if !record.Passed || record.ActualPa != tc.passedActual || record.Polarity != "negative" || record.Ordinal != 1 {
				t.Fatalf("corrected snapshot record = %+v", record)
			}

			nextBody, err := svc.SubmitPressureStep(context.Background(), PressureStepRequest{
				TaskID: id, OperationID: id + "-next", ExpectedRevision: correctedSnapshot.StateRevision,
				Phase: "air", Polarity: "positive", Ordinal: 1, ActualPa: 100, AirflowCCPerSec: 10,
			})
			if err != nil {
				t.Fatalf("next pressure level after corrected retry: %v", err)
			}
			var nextResponse PressureStepResponse
			decodeResponse(t, nextBody, &nextResponse)
			finalSnapshot := snapshot(t, svc, id)
			if len(finalSnapshot.StepRecords) != 2 {
				t.Fatalf("final snapshot records = %d, want 2", len(finalSnapshot.StepRecords))
			}
			if finalSnapshot.State != "water_spray" {
				t.Fatalf("final state = %s, want water_spray", finalSnapshot.State)
			}
			if nextResponse.State != finalSnapshot.State || nextResponse.StateRevision != finalSnapshot.StateRevision {
				t.Fatalf("next response %+v disagrees with GET state=%s revision=%d", nextResponse, finalSnapshot.State, finalSnapshot.StateRevision)
			}
		})
	}
}
