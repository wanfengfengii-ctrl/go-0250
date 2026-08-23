package service

import (
	"context"
	"strings"

	"github.com/windowproof/fenestration/internal/codes"
	"github.com/windowproof/fenestration/internal/inspection"
	"github.com/windowproof/fenestration/internal/store"
	"github.com/windowproof/fenestration/internal/verdict"
)

// DefectRequest appends an immutable defect-evidence version.
type DefectRequest struct {
	TaskID             string `json:"task_id"`
	OperationID        string `json:"operation_id"`
	ExpectedRevision   int64  `json:"expected_revision"`
	DefectKind         string `json:"defect_kind"`
	WindowUnitID       string `json:"window_unit_id,omitempty"`
	PressureOrdinal    int    `json:"pressure_ordinal,omitempty"`
	MeasurementPointID string `json:"measurement_point_id,omitempty"`
	Observation        string `json:"observation,omitempty"`
}

// DefectResponse reports the created evidence version.
type DefectResponse struct {
	TaskID        string `json:"task_id"`
	Generation    int64  `json:"generation"`
	EvidenceID    string `json:"evidence_id"`
	Version       int64  `json:"version"`
	StateRevision int64  `json:"state_revision"`
}

// AddDefect appends an immutable, generation-bound evidence version. The payload
// hash freezes the observation so a later version cannot silently overwrite it.
func (s *Service) AddDefect(ctx context.Context, in DefectRequest) ([]byte, error) {
	return s.run(ctx, in.OperationID, in, func(tx store.Tx) (*outcome, error) {
		task, ok, err := tx.LoadTask(ctx, in.TaskID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "task_id", in.TaskID))
		}
		if task.State.IsTerminal() {
			return rejected(terminalError(task))
		}
		if in.ExpectedRevision != task.StateRevision {
			return rejected(Err(codes.StaleRevision))
		}
		if !validDefectKind(in.DefectKind) {
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "defect_kind", in.DefectKind))
		}

		existing, err := tx.LoadEvidence(ctx, in.TaskID)
		if err != nil {
			return nil, err
		}
		version := int64(len(existing)) + 1
		evidence := verdict.DefectEvidence{
			EvidenceID:         newEvidenceID(),
			TaskID:             in.TaskID,
			Generation:         int64(task.Generation),
			DefectKind:         verdict.DefectKind(in.DefectKind),
			WindowUnitID:       in.WindowUnitID,
			PressureOrdinal:    in.PressureOrdinal,
			MeasurementPointID: in.MeasurementPointID,
			Version:            version,
			ImmutablePayloadHash: HashRequest(struct {
				Kind        string `json:"kind"`
				Observation string `json:"observation"`
			}{in.DefectKind, in.Observation}),
		}
		if err := tx.SaveEvidence(ctx, evidence); err != nil {
			return nil, err
		}
		task.StateRevision++
		task.UpdatedAt = s.clock.NowMillis()
		if err := tx.SaveTask(ctx, task); err != nil {
			return nil, err
		}
		payload := mustJSON(DefectResponse{
			TaskID: task.TaskID, Generation: int64(task.Generation),
			EvidenceID: evidence.EvidenceID, Version: version, StateRevision: task.StateRevision,
		})
		return &outcome{payload: payload}, nil
	})
}

// RepairRequest opens a repair generation and reopens only the affected steps
// and checkpoints.
type RepairRequest struct {
	TaskID              string   `json:"task_id"`
	OperationID         string   `json:"operation_id"`
	ExpectedRevision    int64    `json:"expected_revision"`
	AffectedSteps       []string `json:"affected_steps"`
	AffectedCheckpoints []string `json:"affected_checkpoints"`
	ReasonEvidenceIDs   []string `json:"reason_evidence_ids"`
}

// RepairResponse reports the new generation and re-opened state.
type RepairResponse struct {
	TaskID        string `json:"task_id"`
	Generation    int64  `json:"generation"`
	State         string `json:"state"`
	StateRevision int64  `json:"state_revision"`
}

// StartRepair increments the task generation, copies unaffected obligations and
// reopens exactly the affected steps and checkpoints, moving the task back to
// the earliest affected phase.
func (s *Service) StartRepair(ctx context.Context, in RepairRequest) ([]byte, error) {
	return s.run(ctx, in.OperationID, in, func(tx store.Tx) (*outcome, error) {
		task, ok, err := tx.LoadTask(ctx, in.TaskID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "task_id", in.TaskID))
		}
		if task.State.IsTerminal() {
			return rejected(terminalError(task))
		}
		if in.ExpectedRevision != task.StateRevision {
			return rejected(Err(codes.StaleRevision))
		}
		if len(in.AffectedSteps) == 0 && len(in.AffectedCheckpoints) == 0 {
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "affected", ""))
		}

		// Validate the reason evidence exists for this task.
		evidence, err := tx.LoadEvidence(ctx, in.TaskID)
		if err != nil {
			return nil, err
		}
		evByID := map[string]bool{}
		for _, e := range evidence {
			evByID[e.EvidenceID] = true
		}
		for _, id := range in.ReasonEvidenceIDs {
			if !evByID[id] {
				return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "reason_evidence", id))
			}
		}

		oldGen := int64(task.Generation)
		newGen := oldGen + 1

		// Copy unaffected step records to the new generation.
		affectedSteps := map[string]bool{}
		for _, k := range in.AffectedSteps {
			affectedSteps[k] = true
		}
		oldSteps, err := tx.LoadStepRecords(ctx, in.TaskID, oldGen)
		if err != nil {
			return nil, err
		}
		for _, r := range oldSteps {
			key := stepKey(r.Phase, string(r.Polarity), r.Ordinal)
			if affectedSteps[key] {
				continue
			}
			r.Generation = newGen
			if err := tx.SaveStepRecord(ctx, r); err != nil {
				return nil, err
			}
		}

		// Copy unaffected spray records to the new generation.
		affectedChecks := map[string]bool{}
		for _, k := range in.AffectedCheckpoints {
			affectedChecks[k] = true
		}
		oldSpray, err := tx.LoadSprayRecords(ctx, in.TaskID, oldGen)
		if err != nil {
			return nil, err
		}
		for _, r := range oldSpray {
			if affectedChecks[r.CheckpointID] {
				continue
			}
			r.Generation = newGen
			if err := tx.SaveSprayRecord(ctx, r); err != nil {
				return nil, err
			}
		}

		task.Generation = inspection.Generation(newGen)
		task.State = earliestPhase(in.AffectedSteps, in.AffectedCheckpoints)
		task.CurrentPhase = phaseOfState(task.State)
		task.SprayStartedAt = 0
		task.StateRevision++
		task.UpdatedAt = s.clock.NowMillis()
		if err := tx.SaveTask(ctx, task); err != nil {
			return nil, err
		}
		if err := tx.SaveRepairGeneration(ctx, verdict.RepairGeneration{
			TaskID:              in.TaskID,
			Generation:          newGen,
			ParentGeneration:    oldGen,
			AffectedSteps:       append([]string(nil), in.AffectedSteps...),
			AffectedCheckpoints: append([]string(nil), in.AffectedCheckpoints...),
			ReasonEvidenceIDs:   append([]string(nil), in.ReasonEvidenceIDs...),
		}); err != nil {
			return nil, err
		}

		payload := mustJSON(RepairResponse{
			TaskID: task.TaskID, Generation: newGen, State: string(task.State), StateRevision: task.StateRevision,
		})
		return &outcome{payload: payload}, nil
	})
}

// CloseDefectRequest closes an evidence version against a repair verification.
type CloseDefectRequest struct {
	TaskID           string `json:"task_id"`
	OperationID      string `json:"operation_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	EvidenceID       string `json:"evidence_id"`
	Verification     string `json:"verification,omitempty"`
}

// CloseDefectResponse reports the closure.
type CloseDefectResponse struct {
	TaskID        string `json:"task_id"`
	EvidenceID    string `json:"evidence_id"`
	ClosureID     string `json:"closure_id"`
	StateRevision int64  `json:"state_revision"`
}

// CloseDefect marks an evidence version closed. The immutable payload is never
// rewritten; only the closure id is appended.
func (s *Service) CloseDefect(ctx context.Context, in CloseDefectRequest) ([]byte, error) {
	return s.run(ctx, in.OperationID, in, func(tx store.Tx) (*outcome, error) {
		task, ok, err := tx.LoadTask(ctx, in.TaskID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "task_id", in.TaskID))
		}
		if task.State.IsTerminal() {
			return rejected(terminalError(task))
		}
		if in.ExpectedRevision != task.StateRevision {
			return rejected(Err(codes.StaleRevision))
		}

		evidence, err := tx.LoadEvidence(ctx, in.TaskID)
		if err != nil {
			return nil, err
		}
		var target *verdict.DefectEvidence
		for i := range evidence {
			if evidence[i].EvidenceID == in.EvidenceID {
				target = &evidence[i]
				break
			}
		}
		if target == nil {
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "evidence_id", in.EvidenceID))
		}
		if target.ClosureID != "" {
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "evidence_id", in.EvidenceID))
		}
		closureID := newEvidenceID()
		target.ClosureID = closureID
		if err := tx.SaveEvidence(ctx, *target); err != nil {
			return nil, err
		}
		task.StateRevision++
		task.UpdatedAt = s.clock.NowMillis()
		if err := tx.SaveTask(ctx, task); err != nil {
			return nil, err
		}
		payload := mustJSON(CloseDefectResponse{
			TaskID: task.TaskID, EvidenceID: in.EvidenceID, ClosureID: closureID, StateRevision: task.StateRevision,
		})
		return &outcome{payload: payload}, nil
	})
}

// openDefects reports whether any evidence in the current generation is still
// open (unclosed).
func (s *Service) openDefects(ctx context.Context, tx store.Tx, taskID string, generation int64) (bool, error) {
	evidence, err := tx.LoadEvidence(ctx, taskID)
	if err != nil {
		return false, err
	}
	for _, e := range evidence {
		if e.Generation == generation && e.ClosureID == "" {
			return true, nil
		}
	}
	return false, nil
}

func validDefectKind(kind string) bool {
	switch kind {
	case string(verdict.DefectWaterLeak), string(verdict.DefectSealFailure), string(verdict.DefectResidualDeformation):
		return true
	default:
		return false
	}
}

func earliestPhase(steps, checkpoints []string) inspection.State {
	rank := map[string]int{"air": 0, "water": 1, "wind": 2}
	best := 2 // wind
	for _, s := range steps {
		phase := phaseFromStepKey(s)
		if r, ok := rank[phase]; ok && r < best {
			best = r
		}
	}
	if len(checkpoints) > 0 {
		if rank["water"] < best {
			best = rank["water"]
		}
	}
	switch best {
	case 0:
		return inspection.StateAirLoading
	case 1:
		return inspection.StateWaterSpray
	default:
		return inspection.StateWindReview
	}
}

func phaseFromStepKey(key string) string {
	i := strings.IndexByte(key, '/')
	if i < 0 {
		return ""
	}
	return key[:i]
}

func phaseOfState(s inspection.State) inspection.Phase {
	switch s {
	case inspection.StateAirLoading:
		return inspection.PhaseAir
	case inspection.StateWaterSpray:
		return inspection.PhaseWater
	case inspection.StateWindReview:
		return inspection.PhaseWind
	default:
		return ""
	}
}

var evidenceCounter uint64

func newEvidenceID() string {
	evidenceCounter++
	return "ev-" + itoa(int(evidenceCounter))
}
