package service

import (
	"context"
	"sort"

	"github.com/windowproof/fenestration/internal/codes"
	"github.com/windowproof/fenestration/internal/inspection"
	"github.com/windowproof/fenestration/internal/store"
	"github.com/windowproof/fenestration/internal/verdict"
)

// ReviewRequest submits one independent review seat.
type ReviewRequest struct {
	TaskID                string `json:"task_id"`
	OperationID           string `json:"operation_id"`
	ReviewerID            string `json:"reviewer_id"`
	QualificationRevision string `json:"qualification_revision"`
	Decision              string `json:"decision"`
	VerdictHash           string `json:"verdict_hash"`
}

// ReviewResponse reports the review and resulting state.
type ReviewResponse struct {
	TaskID        string `json:"task_id"`
	State         string `json:"state"`
	StateRevision int64  `json:"state_revision"`
	Reviews       int    `json:"reviews"`
}

// SubmitReview validates reviewer qualification and identity separation, and,
// once two distinct qualified reviewers bind the same verdict, advances the task
// to releasable.
func (s *Service) SubmitReview(ctx context.Context, in ReviewRequest) ([]byte, error) {
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
		if task.State != inspection.StatePendingReview && task.State != inspection.StateReleasable {
			return rejected(Err(codes.InvalidState))
		}
		if in.Decision != string(verdict.ReviewPass) {
			return rejected(Err(codes.InvalidRequest).WithReason(codes.InvalidRequest, "decision", in.Decision))
		}

		rev, ok, err := tx.LoadCatalog(ctx, task.LockedCatalogRevision)
		if err != nil {
			return nil, err
		}
		if !ok {
			return rejected(Err(codes.CatalogMismatch))
		}
		qualified := false
		for _, p := range rev.QualifiedPeople {
			if p.PersonID == in.ReviewerID && p.QualificationRevision == in.QualificationRevision {
				qualified = true
				break
			}
		}
		if !qualified {
			e := Err(codes.QualificationInvalid).WithReason(codes.QualificationInvalid, "reviewer_id", in.ReviewerID)
			e.TaskID = task.TaskID
			e.StateRevision = task.StateRevision
			return rejected(e)
		}

		wantHash, err := s.verdictHash(ctx, tx, task)
		if err != nil {
			return nil, err
		}
		if in.VerdictHash != wantHash {
			e := Err(codes.VerdictMismatch).WithReason(codes.VerdictMismatch, "verdict_hash", in.VerdictHash)
			e.TaskID = task.TaskID
			e.StateRevision = task.StateRevision
			return rejected(e)
		}

		existing, err := tx.LoadReviews(ctx, in.TaskID, int64(task.Generation))
		if err != nil {
			return nil, err
		}
		for _, r := range existing {
			if r.ReviewerID == in.ReviewerID {
				e := Err(codes.ReviewConflict).WithReason(codes.ReviewConflict, "reviewer_id", in.ReviewerID)
				e.TaskID = task.TaskID
				e.StateRevision = task.StateRevision
				return rejected(e)
			}
		}

		review := verdict.Review{
			TaskID:                in.TaskID,
			Generation:            int64(task.Generation),
			ReviewerID:            in.ReviewerID,
			QualificationRevision: in.QualificationRevision,
			VerdictHash:           in.VerdictHash,
			Decision:              verdict.ReviewPass,
			OperationID:           in.OperationID,
		}
		if err := tx.SaveReview(ctx, review); err != nil {
			return nil, err
		}
		existing = append(existing, review)

		if task.State == inspection.StatePendingReview && len(existing) >= 2 {
			task.State = inspection.StateReleasable
		}
		task.StateRevision++
		task.UpdatedAt = s.clock.NowMillis()
		if err := tx.SaveTask(ctx, task); err != nil {
			return nil, err
		}
		payload := mustJSON(ReviewResponse{
			TaskID: task.TaskID, State: string(task.State),
			StateRevision: task.StateRevision, Reviews: len(existing),
		})
		return &outcome{payload: payload}, nil
	})
}

// verdictHash computes the stable conclusion summary bound by two reviews.
func (s *Service) verdictHash(ctx context.Context, q store.Querier, task inspection.InspectionTask) (string, error) {
	steps, err := q.LoadStepRecords(ctx, task.TaskID, int64(task.Generation))
	if err != nil {
		return "", err
	}
	spray, err := q.LoadSprayRecords(ctx, task.TaskID, int64(task.Generation))
	if err != nil {
		return "", err
	}
	evidence, err := q.LoadEvidence(ctx, task.TaskID)
	if err != nil {
		return "", err
	}
	var stepKeys []string
	for _, r := range steps {
		if r.Passed {
			stepKeys = append(stepKeys, stepKey(r.Phase, string(r.Polarity), r.Ordinal))
		}
	}
	sort.Strings(stepKeys)
	var checkIDs []string
	for _, r := range spray {
		if r.Covered {
			checkIDs = append(checkIDs, r.CheckpointID)
		}
	}
	sort.Strings(checkIDs)
	var closedIDs []string
	for _, e := range evidence {
		if e.Generation == int64(task.Generation) && e.ClosureID != "" {
			closedIDs = append(closedIDs, e.EvidenceID)
		}
	}
	sort.Strings(closedIDs)
	type summary struct {
		Snapshot    string   `json:"snapshot"`
		Generation  int64    `json:"generation"`
		Steps       []string `json:"steps"`
		Checkpoints []string `json:"checkpoints"`
		Closed      []string `json:"closed"`
	}
	return HashRequest(summary{task.LockedSnapshotHash, int64(task.Generation), stepKeys, checkIDs, closedIDs}), nil
}

// TerminalRequest is shared by release, quarantine and cancel.
type TerminalRequest struct {
	TaskID           string `json:"task_id"`
	OperationID      string `json:"operation_id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

// TerminalResponse reports the terminal outcome.
type TerminalResponse struct {
	TaskID        string          `json:"task_id"`
	State         string          `json:"state"`
	TerminalKind  string          `json:"terminal_kind"`
	StateRevision int64           `json:"state_revision"`
	Credential    *CredentialView `json:"credential,omitempty"`
}

// CredentialView is the JSON view of a release credential.
type CredentialView struct {
	TaskID       string `json:"task_id"`
	Generation   int64  `json:"generation"`
	VerdictHash  string `json:"verdict_hash"`
	SerialNumber string `json:"serial_number"`
}

// Release finalizes a releasable task and issues a unique credential.
func (s *Service) Release(ctx context.Context, in TerminalRequest) ([]byte, error) {
	return s.terminal(ctx, in, inspection.TerminalReleased)
}

// Quarantine isolates a task for repair.
func (s *Service) Quarantine(ctx context.Context, in TerminalRequest) ([]byte, error) {
	return s.terminal(ctx, in, inspection.TerminalQuarantined)
}

// Cancel cancels a task.
func (s *Service) Cancel(ctx context.Context, in TerminalRequest) ([]byte, error) {
	return s.terminal(ctx, in, inspection.TerminalCancelled)
}

func (s *Service) terminal(ctx context.Context, in TerminalRequest, kind inspection.TerminalKind) ([]byte, error) {
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
		switch kind {
		case inspection.TerminalReleased:
			if task.State != inspection.StateReleasable {
				return rejected(Err(codes.InvalidState))
			}
		case inspection.TerminalQuarantined:
			if task.State == inspection.StatePendingLock || task.State == inspection.StateInstalling {
				return rejected(Err(codes.InvalidState))
			}
		case inspection.TerminalCancelled:
			// Cancellation is allowed from any non-terminal state.
		}

		task.State = stateForTerminal(kind)
		task.TerminalKind = kind
		task.StateRevision++
		task.UpdatedAt = s.clock.NowMillis()
		if err := tx.SaveTask(ctx, task); err != nil {
			return nil, err
		}

		var cred *CredentialView
		if kind == inspection.TerminalReleased {
			hash, err := s.verdictHash(ctx, tx, task)
			if err != nil {
				return nil, err
			}
			c := verdict.ReleaseCredential{
				TaskID:       task.TaskID,
				Generation:   int64(task.Generation),
				VerdictHash:  hash,
				SerialNumber: serialFor(task.TaskID, hash, int64(task.Generation)),
				IssuedAt:     s.clock.NowMillis(),
			}
			if err := tx.SaveCredential(ctx, c); err != nil {
				return nil, err
			}
			cred = &CredentialView{
				TaskID: c.TaskID, Generation: c.Generation,
				VerdictHash: c.VerdictHash, SerialNumber: c.SerialNumber,
			}
		}

		// Release all occupancy tokens at the terminal fence.
		if err := tx.ReleaseAllTokens(ctx, task.TaskID, int64(task.Generation)); err != nil {
			return nil, err
		}

		payload := mustJSON(TerminalResponse{
			TaskID: task.TaskID, State: string(task.State),
			TerminalKind: string(kind), StateRevision: task.StateRevision, Credential: cred,
		})
		return &outcome{payload: payload}, nil
	})
}

func stateForTerminal(kind inspection.TerminalKind) inspection.State {
	switch kind {
	case inspection.TerminalReleased:
		return inspection.StateReleased
	case inspection.TerminalQuarantined:
		return inspection.StateQuarantined
	default:
		return inspection.StateCancelled
	}
}

func serialFor(taskID, hash string, generation int64) string {
	h := HashRequest(struct {
		Task string `json:"task"`
		Hash string `json:"hash"`
		Gen  int64  `json:"gen"`
	}{taskID, hash, generation})
	return "WP-" + h[:16]
}
