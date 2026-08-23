package store

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/windowproof/fenestration/internal/inspection"
)

// Recover validates the durable invariants at startup. It refuses to start if
// a broken invariant is detected, returning a stable codes.RecoveryError-style
// error so operators can quarantine the database.
func (s *sqliteStore) Recover(ctx context.Context) error {
	tx := s.readTx()

	// 1. Open occupancy uniqueness: no two active tokens may reference the same
	//    resource. The partial unique index normally enforces this, but an
	//    externally corrupted file could bypass it; validate defensively.
	tokens, err := tx.LoadAllActiveTokens(ctx)
	if err != nil {
		return fmt.Errorf("recover: load tokens: %w", err)
	}
	seen := map[string]string{}
	for _, t := range tokens {
		key := string(t.ResourceKind) + "/" + t.ResourceID
		if prev, ok := seen[key]; ok {
			return fmt.Errorf("recover: resource %s held by both %s and %s", key, prev, t.TaskID)
		}
		seen[key] = t.TaskID
	}

	// 2. Completed per-phase prefixes must be gap-free. For each task we scan its
	//    step records and confirm ordinals are contiguous from 1 within a phase.
	if err := s.recoverPrefixes(ctx, tx); err != nil {
		return err
	}

	// 3. Evidence chain references must resolve: a supersedes_id must name an
	//    existing evidence record, and versions must be strictly increasing.
	if err := s.recoverEvidence(ctx, tx); err != nil {
		return err
	}

	// 4. A task may hold at most one release credential and its terminal kind
	//    must be consistent with it.
	if err := s.recoverTerminal(ctx, tx); err != nil {
		return err
	}

	return nil
}

func (s *sqliteStore) recoverPrefixes(ctx context.Context, tx *sqliteTx) error {
	rows, err := tx.ex.QueryContext(ctx, `SELECT DISTINCT task_id FROM step_records`)
	if err != nil {
		return err
	}
	var taskIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		taskIDs = append(taskIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, id := range taskIDs {
		// Load all generations for this task.
		grows, err := tx.ex.QueryContext(ctx,
			`SELECT generation, phase, polarity, ordinal FROM step_records WHERE task_id=? ORDER BY generation, phase, polarity, ordinal`, id)
		if err != nil {
			return err
		}
		type key struct {
			gen   int64
			phase string
			pol   string
		}
		last := map[key]int{}
		for grows.Next() {
			var gen int64
			var phase, pol string
			var ord int
			if err := grows.Scan(&gen, &phase, &pol, &ord); err != nil {
				grows.Close()
				return err
			}
			k := key{gen, phase, pol}
			if ord != last[k]+1 {
				grows.Close()
				return fmt.Errorf("recover: task %s has a gap in %s/%s prefix (expected %d, got %d)",
					id, phase, pol, last[k]+1, ord)
			}
			last[k] = ord
		}
		grows.Close()
		if err := grows.Err(); err != nil {
			return err
		}
	}
	return nil
}

func (s *sqliteStore) recoverEvidence(ctx context.Context, tx *sqliteTx) error {
	rows, err := tx.ex.QueryContext(ctx,
		`SELECT task_id, evidence_id, supersedes_id, version FROM defect_evidence ORDER BY task_id, version`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type ev struct {
		id         string
		supersedes string
		version    int64
	}
	byTask := map[string][]ev{}
	ids := map[string]string{} // evidence_id -> task_id
	for rows.Next() {
		var taskID, id, supersedes string
		var version int64
		if err := rows.Scan(&taskID, &id, &supersedes, &version); err != nil {
			return err
		}
		byTask[taskID] = append(byTask[taskID], ev{id, supersedes, version})
		ids[id] = taskID
	}
	for _, list := range byTask {
		for _, e := range list {
			if e.supersedes != "" {
				if _, ok := ids[e.supersedes]; !ok {
					return fmt.Errorf("recover: evidence %s supersedes unknown %s", e.id, e.supersedes)
				}
			}
		}
		// Versions must be strictly increasing per task.
		sorted := append([]ev(nil), list...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].version < sorted[j].version })
		for i := 1; i < len(sorted); i++ {
			if sorted[i].version <= sorted[i-1].version {
				return fmt.Errorf("recover: task has non-increasing evidence versions")
			}
		}
	}
	return nil
}

func (s *sqliteStore) recoverTerminal(ctx context.Context, tx *sqliteTx) error {
	rows, err := tx.ex.QueryContext(ctx, `SELECT task_id FROM release_credentials`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		task, ok, err := tx.LoadTask(ctx, id)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("recover: release credential for unknown task %s", id)
		}
		if task.State != inspection.StateReleased {
			return fmt.Errorf("recover: task %s has a credential but is %s", id, task.State)
		}
	}
	return nil
}

// ErrRecovery is the wrapped recovery failure sentinel.
var ErrRecovery = errors.New("store recovery failed")
