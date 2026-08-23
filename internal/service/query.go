package service

import (
	"context"

	"github.com/windowproof/fenestration/internal/codes"
)

// Snapshot is the full read view of a task returned by GET.
type Snapshot struct {
	TaskID                string              `json:"task_id"`
	Generation            int64               `json:"generation"`
	State                 string              `json:"state"`
	StateRevision         int64               `json:"state_revision"`
	LockedCatalogRevision string              `json:"locked_catalog_revision"`
	LockedSnapshotHash    string              `json:"locked_snapshot_hash"`
	WindowUnitID          string              `json:"window_unit_id"`
	ChamberID             string              `json:"chamber_id"`
	MeasurementPointIDs   []string            `json:"measurement_point_ids"`
	CurrentPhase          string              `json:"current_phase"`
	TerminalKind          string              `json:"terminal_kind"`
	OrientationConfirmed  bool                `json:"orientation_confirmed"`
	SealedConfirmed       bool                `json:"sealed_confirmed"`
	PointsZeroed          bool                `json:"points_zeroed"`
	StepRecords           []StepRecordView    `json:"step_records"`
	SprayCoverage         []SprayCoverageView `json:"spray_coverage"`
	Evidence              []EvidenceView      `json:"evidence"`
	Reviews               []ReviewView        `json:"reviews"`
	Credential            *CredentialView     `json:"credential,omitempty"`
	Attempts              []AttemptView       `json:"attempts"`
}

// StepRecordView is the JSON view of a pressure step record.
type StepRecordView struct {
	Phase              string `json:"phase"`
	Polarity           string `json:"polarity"`
	Ordinal            int    `json:"ordinal"`
	ActualPa           int64  `json:"actual_pa"`
	AirflowCCPerSec    int64  `json:"airflow_cc_per_sec"`
	DisplacementMicron int64  `json:"displacement_micron"`
	Passed             bool   `json:"passed"`
}

// SprayCoverageView is the JSON view of a covered spray checkpoint.
type SprayCoverageView struct {
	CheckpointID string `json:"checkpoint_id"`
	ActualPa     int64  `json:"actual_pa"`
	Observation  string `json:"observation,omitempty"`
}

// EvidenceView is the JSON view of a defect-evidence version.
type EvidenceView struct {
	EvidenceID         string `json:"evidence_id"`
	Generation         int64  `json:"generation"`
	DefectKind         string `json:"defect_kind"`
	WindowUnitID       string `json:"window_unit_id"`
	PressureOrdinal    int    `json:"pressure_ordinal"`
	MeasurementPointID string `json:"measurement_point_id"`
	Version            int64  `json:"version"`
	ClosureID          string `json:"closure_id,omitempty"`
}

// ReviewView is the JSON view of a review seat.
type ReviewView struct {
	ReviewerID            string `json:"reviewer_id"`
	QualificationRevision string `json:"qualification_revision"`
	Decision              string `json:"decision"`
}

// AttemptView is the JSON view of an instrument attempt.
type AttemptView struct {
	AttemptID   string `json:"attempt_id"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	FailureCode string `json:"failure_code,omitempty"`
}

// GetTask returns the full snapshot of a task.
func (s *Service) GetTask(ctx context.Context, taskID string) (*Snapshot, error) {
	task, ok, err := s.store.LoadTask(ctx, taskID)
	if err != nil {
		return nil, Err(codes.StoreUnavailable)
	}
	if !ok {
		return nil, Err(codes.InvalidRequest)
	}
	snap := &Snapshot{
		TaskID:                task.TaskID,
		Generation:            int64(task.Generation),
		State:                 string(task.State),
		StateRevision:         task.StateRevision,
		LockedCatalogRevision: task.LockedCatalogRevision,
		LockedSnapshotHash:    task.LockedSnapshotHash,
		WindowUnitID:          task.WindowUnitID,
		ChamberID:             task.ChamberID,
		MeasurementPointIDs:   append([]string(nil), task.MeasurementPointIDs...),
		CurrentPhase:          string(task.CurrentPhase),
		TerminalKind:          string(task.TerminalKind),
		OrientationConfirmed:  task.OrientationConfirmed,
		SealedConfirmed:       task.SealedConfirmed,
		PointsZeroed:          task.PointsZeroed,
	}

	if steps, err := s.store.LoadStepRecords(ctx, taskID, int64(task.Generation)); err == nil {
		for _, r := range steps {
			snap.StepRecords = append(snap.StepRecords, StepRecordView{
				Phase: r.Phase, Polarity: string(r.Polarity), Ordinal: r.Ordinal,
				ActualPa: r.ActualPa, AirflowCCPerSec: r.AirflowCCPerSec,
				DisplacementMicron: r.DisplacementMicron, Passed: r.Passed,
			})
		}
	}
	if spray, err := s.store.LoadSprayRecords(ctx, taskID, int64(task.Generation)); err == nil {
		for _, r := range spray {
			if r.Covered {
				snap.SprayCoverage = append(snap.SprayCoverage, SprayCoverageView{
					CheckpointID: r.CheckpointID, ActualPa: r.ActualPa, Observation: r.Observation,
				})
			}
		}
	}
	if evidence, err := s.store.LoadEvidence(ctx, taskID); err == nil {
		for _, e := range evidence {
			snap.Evidence = append(snap.Evidence, EvidenceView{
				EvidenceID: e.EvidenceID, Generation: e.Generation, DefectKind: string(e.DefectKind),
				WindowUnitID: e.WindowUnitID, PressureOrdinal: e.PressureOrdinal,
				MeasurementPointID: e.MeasurementPointID, Version: e.Version, ClosureID: e.ClosureID,
			})
		}
	}
	if reviews, err := s.store.LoadReviews(ctx, taskID); err == nil {
		for _, r := range reviews {
			snap.Reviews = append(snap.Reviews, ReviewView{
				ReviewerID: r.ReviewerID, QualificationRevision: r.QualificationRevision, Decision: string(r.Decision),
			})
		}
	}
	if cred, ok, err := s.store.LoadCredential(ctx, taskID); err == nil && ok {
		snap.Credential = &CredentialView{
			TaskID: cred.TaskID, Generation: cred.Generation,
			VerdictHash: cred.VerdictHash, SerialNumber: cred.SerialNumber,
		}
	}
	if attempts, err := s.store.LoadAttempts(ctx, taskID); err == nil {
		for _, a := range attempts {
			snap.Attempts = append(snap.Attempts, AttemptView{
				AttemptID: a.AttemptID, Kind: a.Kind, Status: string(a.Status), FailureCode: a.FailureCode,
			})
		}
	}
	return snap, nil
}

// VerdictHash returns the stable conclusion summary that independent reviewers
// bind. It is the digest of the locked snapshot, generation, completed steps,
// covered checkpoints and closed evidence in the current generation.
func (s *Service) VerdictHash(ctx context.Context, taskID string) (string, error) {
	task, ok, err := s.store.LoadTask(ctx, taskID)
	if err != nil {
		return "", Err(codes.StoreUnavailable)
	}
	if !ok {
		return "", Err(codes.InvalidRequest)
	}
	return s.verdictHash(ctx, s.store, task)
}
