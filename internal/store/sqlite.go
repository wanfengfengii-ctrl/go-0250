package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/windowproof/fenestration/internal/acquisition"
	"github.com/windowproof/fenestration/internal/catalog"
	"github.com/windowproof/fenestration/internal/inspection"
	"github.com/windowproof/fenestration/internal/occupancy"
	"github.com/windowproof/fenestration/internal/verdict"

	_ "modernc.org/sqlite"
)

// Open opens (or creates) a SQLite WAL database at path, applies the schema and
// returns a Store. A path of ":memory:" yields an in-memory database used by
// tests and the no-persistence fallback.
func Open(path string) (Store, error) {
	if path == "" || path == ":memory:" {
		path = ":memory:"
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	// A single connection serializes all access; BEGIN IMMEDIATE inside Write
	// then guarantees the write-lock semantics required by the domain rules.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: apply schema: %w", err)
	}
	return &sqliteStore{db: db}, nil
}

type sqliteStore struct {
	db *sql.DB

	mu          sync.Mutex
	failCommits int
}

// FailNextCommits arms the commit failpoint so the next n write transactions
// are interrupted at commit time and rolled back. It is used by recovery tests
// to verify that a commit interruption never leaves partial tokens, checkpoints
// or credentials.
func (s *sqliteStore) FailNextCommits(n int) {
	s.mu.Lock()
	s.failCommits = n
	s.mu.Unlock()
}

func (s *sqliteStore) shouldFailCommit() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failCommits > 0 {
		s.failCommits--
		return true
	}
	return false
}

func (s *sqliteStore) Close() error { return s.db.Close() }

// Write runs fn inside a single BEGIN IMMEDIATE transaction. It acquires the
// single connection so that reads and writes are fully serialized and a
// concurrent writer cannot interleave between our read and commit.
func (s *sqliteStore) Write(ctx context.Context, fn func(Tx) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: acquire conn: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("store: begin immediate: %w", err)
	}
	tx := &sqliteTx{ex: conn}
	committed := false
	if err := fn(tx); err != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return err
	}
	if s.shouldFailCommit() {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return errors.New("store: injected commit failure")
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return fmt.Errorf("store: commit: %w", err)
	}
	committed = true
	_ = committed
	return nil
}

// execer is the subset of *sql.DB and *sql.Conn used by the store.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// sqliteTx implements Tx against either the single reserved write connection or
// the underlying database handle (for read-only store-level queries).
type sqliteTx struct {
	ex execer
}

// --- Catalog ---

func (t *sqliteTx) SaveCatalog(ctx context.Context, rev catalog.CatalogRevision) error {
	payload, err := json.Marshal(rev)
	if err != nil {
		return err
	}
	_, err = t.ex.ExecContext(ctx,
		`INSERT INTO catalog_revisions(revision_id, snapshot_hash, payload) VALUES(?,?,?)
		 ON CONFLICT(revision_id) DO NOTHING`,
		rev.RevisionID, rev.SnapshotHash(), payload)
	return err
}

func (t *sqliteTx) LoadCatalog(ctx context.Context, revisionID string) (catalog.CatalogRevision, bool, error) {
	var payload []byte
	err := t.ex.QueryRowContext(ctx,
		`SELECT payload FROM catalog_revisions WHERE revision_id = ?`, revisionID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.CatalogRevision{}, false, nil
	}
	if err != nil {
		return catalog.CatalogRevision{}, false, err
	}
	var rev catalog.CatalogRevision
	if err := json.Unmarshal(payload, &rev); err != nil {
		return catalog.CatalogRevision{}, false, err
	}
	return rev, true, nil
}

// --- Task ---

func (t *sqliteTx) SaveTask(ctx context.Context, task inspection.InspectionTask) error {
	mps, err := json.Marshal(task.MeasurementPointIDs)
	if err != nil {
		return err
	}
	_, err = t.ex.ExecContext(ctx, `
		INSERT INTO tasks(task_id, generation, state, state_revision, locked_catalog_revision,
			locked_snapshot_hash, locked_plan_id, window_unit_id, chamber_id, measurement_point_ids,
			spray_started_at, orientation_confirmed, sealed_confirmed, points_zeroed,
			current_phase, terminal_kind, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(task_id) DO UPDATE SET
			generation=excluded.generation, state=excluded.state, state_revision=excluded.state_revision,
			locked_catalog_revision=excluded.locked_catalog_revision,
			locked_snapshot_hash=excluded.locked_snapshot_hash,
			locked_plan_id=excluded.locked_plan_id, window_unit_id=excluded.window_unit_id,
			chamber_id=excluded.chamber_id, measurement_point_ids=excluded.measurement_point_ids,
			spray_started_at=excluded.spray_started_at,
			orientation_confirmed=excluded.orientation_confirmed, sealed_confirmed=excluded.sealed_confirmed,
			points_zeroed=excluded.points_zeroed, current_phase=excluded.current_phase,
			terminal_kind=excluded.terminal_kind, updated_at=excluded.updated_at`,
		task.TaskID, int64(task.Generation), string(task.State), task.StateRevision,
		task.LockedCatalogRevision, task.LockedSnapshotHash, task.LockedPlanID, task.WindowUnitID,
		task.ChamberID, mps, task.SprayStartedAt,
		boolInt(task.OrientationConfirmed), boolInt(task.SealedConfirmed), boolInt(task.PointsZeroed),
		string(task.CurrentPhase), string(task.TerminalKind), task.CreatedAt, task.UpdatedAt)
	return err
}

func scanTask(row *sql.Row) (inspection.InspectionTask, bool, error) {
	var t inspection.InspectionTask
	var gen int64
	var mps []byte
	var oC, sC, pZ int
	err := row.Scan(&t.TaskID, &gen, &t.State, &t.StateRevision, &t.LockedCatalogRevision,
		&t.LockedSnapshotHash, &t.LockedPlanID, &t.WindowUnitID, &t.ChamberID, &mps,
		&t.SprayStartedAt, &oC, &sC, &pZ, &t.CurrentPhase, &t.TerminalKind, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return inspection.InspectionTask{}, false, nil
	}
	if err != nil {
		return inspection.InspectionTask{}, false, err
	}
	t.Generation = inspection.Generation(gen)
	t.OrientationConfirmed = oC != 0
	t.SealedConfirmed = sC != 0
	t.PointsZeroed = pZ != 0
	_ = json.Unmarshal(mps, &t.MeasurementPointIDs)
	return t, true, nil
}

func (t *sqliteTx) LoadTask(ctx context.Context, taskID string) (inspection.InspectionTask, bool, error) {
	row := t.ex.QueryRowContext(ctx, `SELECT task_id, generation, state, state_revision,
		locked_catalog_revision, locked_snapshot_hash, locked_plan_id, window_unit_id, chamber_id,
		measurement_point_ids, spray_started_at, orientation_confirmed, sealed_confirmed, points_zeroed,
		current_phase, terminal_kind, created_at, updated_at FROM tasks WHERE task_id = ?`, taskID)
	return scanTask(row)
}

// --- Occupancy ---

func (t *sqliteTx) AcquireTokens(ctx context.Context, taskID string, generation int64, reqs []occupancy.Request) ([]occupancy.OccupancyToken, error) {
	sorted := occupancy.SortRequests(reqs)
	tokens := make([]occupancy.OccupancyToken, 0, len(sorted))
	for _, r := range sorted {
		// A conditional insert enforces the active-resource uniqueness. If another
		// open task holds the resource, this returns zero rows and the caller maps
		// the failure to an occupancy conflict, rolling back the whole transaction.
		res, err := t.ex.ExecContext(ctx, `
			INSERT INTO occupancy_tokens(token_id, resource_kind, resource_id, task_id, generation, active, acquired_at)
			SELECT ?, ?, ?, ?, ?, 1, ?
			WHERE NOT EXISTS (SELECT 1 FROM occupancy_tokens WHERE resource_kind=? AND resource_id=? AND active=1)`,
			newTokenID(), string(r.ResourceKind), r.ResourceID, taskID, generation, nowMillis(),
			string(r.ResourceKind), r.ResourceID)
		if err != nil {
			return nil, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, ErrOccupancyConflict{Kind: r.ResourceKind, ResourceID: r.ResourceID}
		}
		tokens = append(tokens, occupancy.OccupancyToken{
			TokenID:      "",
			ResourceKind: r.ResourceKind,
			ResourceID:   r.ResourceID,
			TaskID:       taskID,
			Generation:   generation,
			Active:       true,
		})
	}
	return tokens, nil
}

// ErrOccupancyConflict reports the specific resource that could not be acquired.
type ErrOccupancyConflict struct {
	Kind       occupancy.ResourceKind
	ResourceID string
}

func (e ErrOccupancyConflict) Error() string {
	return fmt.Sprintf("occupancy conflict: %s %s", e.Kind, e.ResourceID)
}

func (t *sqliteTx) ReleaseToken(ctx context.Context, taskID string, kind occupancy.ResourceKind, resourceID string, generation int64) error {
	_, err := t.ex.ExecContext(ctx, `
		UPDATE occupancy_tokens SET active=0, released_at=? WHERE task_id=? AND resource_kind=? AND resource_id=? AND generation=? AND active=1`,
		nowMillis(), taskID, string(kind), resourceID, generation)
	return err
}

func (t *sqliteTx) ReleaseAllTokens(ctx context.Context, taskID string, generation int64) error {
	_, err := t.ex.ExecContext(ctx,
		`UPDATE occupancy_tokens SET active=0, released_at=? WHERE task_id=? AND generation=? AND active=1`,
		nowMillis(), taskID, generation)
	return err
}

func (t *sqliteTx) LoadTokens(ctx context.Context, taskID string) ([]occupancy.OccupancyToken, error) {
	rows, err := t.ex.QueryContext(ctx,
		`SELECT token_id, resource_kind, resource_id, task_id, generation, active FROM occupancy_tokens WHERE task_id=? ORDER BY resource_kind, resource_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []occupancy.OccupancyToken
	for rows.Next() {
		var o occupancy.OccupancyToken
		var active int
		if err := rows.Scan(&o.TokenID, &o.ResourceKind, &o.ResourceID, &o.TaskID, &o.Generation, &active); err != nil {
			return nil, err
		}
		o.Active = active != 0
		out = append(out, o)
	}
	return out, rows.Err()
}

func (t *sqliteTx) LoadAllActiveTokens(ctx context.Context) ([]occupancy.OccupancyToken, error) {
	rows, err := t.ex.QueryContext(ctx,
		`SELECT token_id, resource_kind, resource_id, task_id, generation, active FROM occupancy_tokens WHERE active=1 ORDER BY resource_kind, resource_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []occupancy.OccupancyToken
	for rows.Next() {
		var o occupancy.OccupancyToken
		var active int
		if err := rows.Scan(&o.TokenID, &o.ResourceKind, &o.ResourceID, &o.TaskID, &o.Generation, &active); err != nil {
			return nil, err
		}
		o.Active = active != 0
		out = append(out, o)
	}
	return out, rows.Err()
}

// --- Acquisition ---

// SaveStepRecord upserts a pressure step record. A completed (passed) level is
// immutable: a later submission for the same (task, generation, phase, polarity,
// ordinal) must not overwrite a frozen conclusion. A failed (not-yet-passed)
// level is not completed, so a corrected reading that retries the same level is
// allowed to replace the prior failed record; without this the retry's pass is
// silently dropped and the task stays stuck on the stale failed reading.
func (t *sqliteTx) SaveStepRecord(ctx context.Context, rec acquisition.StepRecord) error {
	_, err := t.ex.ExecContext(ctx, `
		INSERT INTO step_records(task_id, generation, phase, polarity, ordinal, actual_pa,
			airflow_cc_per_sec, displacement_micron, operation_id, content_hash, passed)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(task_id, generation, phase, polarity, ordinal) DO UPDATE SET
			actual_pa=excluded.actual_pa,
			airflow_cc_per_sec=excluded.airflow_cc_per_sec,
			displacement_micron=excluded.displacement_micron,
			operation_id=excluded.operation_id,
			content_hash=excluded.content_hash,
			passed=excluded.passed
		WHERE step_records.passed = 0`,
		rec.TaskID, rec.Generation, rec.Phase, string(rec.Polarity), rec.Ordinal, rec.ActualPa,
		rec.AirflowCCPerSec, rec.DisplacementMicron, rec.OperationID, rec.ContentHash, boolInt(rec.Passed))
	return err
}

func (t *sqliteTx) LoadStepRecords(ctx context.Context, taskID string, generation int64) ([]acquisition.StepRecord, error) {
	rows, err := t.ex.QueryContext(ctx, `
		SELECT generation, phase, polarity, ordinal, actual_pa, airflow_cc_per_sec, displacement_micron, operation_id, content_hash, passed
		FROM step_records WHERE task_id=? AND generation=? ORDER BY phase, polarity, ordinal`, taskID, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []acquisition.StepRecord
	for rows.Next() {
		var r acquisition.StepRecord
		var passed int
		if err := rows.Scan(&r.Generation, &r.Phase, &r.Polarity, &r.Ordinal, &r.ActualPa,
			&r.AirflowCCPerSec, &r.DisplacementMicron, &r.OperationID, &r.ContentHash, &passed); err != nil {
			return nil, err
		}
		r.TaskID = taskID
		r.Passed = passed != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t *sqliteTx) SaveSprayRecord(ctx context.Context, rec acquisition.SprayRecord) error {
	_, err := t.ex.ExecContext(ctx, `
		INSERT INTO spray_records(task_id, generation, checkpoint_id, actual_pa, sensor_status, observation, covered)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(task_id, generation, checkpoint_id) DO NOTHING`,
		rec.TaskID, rec.Generation, rec.CheckpointID, rec.ActualPa, string(rec.SensorStatus),
		rec.Observation, boolInt(rec.Covered))
	return err
}

func (t *sqliteTx) LoadSprayRecords(ctx context.Context, taskID string, generation int64) ([]acquisition.SprayRecord, error) {
	rows, err := t.ex.QueryContext(ctx, `
		SELECT generation, checkpoint_id, actual_pa, sensor_status, observation, covered
		FROM spray_records WHERE task_id=? AND generation=? ORDER BY checkpoint_id`, taskID, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []acquisition.SprayRecord
	for rows.Next() {
		var r acquisition.SprayRecord
		var covered int
		if err := rows.Scan(&r.Generation, &r.CheckpointID, &r.ActualPa, &r.SensorStatus,
			&r.Observation, &covered); err != nil {
			return nil, err
		}
		r.TaskID = taskID
		r.Covered = covered != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t *sqliteTx) SaveAttempt(ctx context.Context, a acquisition.InstrumentAttempt) error {
	_, err := t.ex.ExecContext(ctx, `
		INSERT INTO instrument_attempts(attempt_id, operation_id, task_id, generation, kind, request_hash, request_payload, status, failure_code, response_payload)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(attempt_id) DO UPDATE SET status=excluded.status, failure_code=excluded.failure_code, response_payload=excluded.response_payload`,
		a.AttemptID, a.OperationID, a.TaskID, a.Generation, a.Kind, a.RequestHash,
		a.RequestPayload, string(a.Status), a.FailureCode, a.ResponsePayload)
	return err
}

func (t *sqliteTx) LoadAttempts(ctx context.Context, taskID string) ([]acquisition.InstrumentAttempt, error) {
	rows, err := t.ex.QueryContext(ctx, `
		SELECT attempt_id, operation_id, task_id, generation, kind, request_hash, request_payload, status, failure_code, response_payload
		FROM instrument_attempts WHERE task_id=? ORDER BY attempt_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []acquisition.InstrumentAttempt
	for rows.Next() {
		var a acquisition.InstrumentAttempt
		if err := rows.Scan(&a.AttemptID, &a.OperationID, &a.TaskID, &a.Generation, &a.Kind,
			&a.RequestHash, &a.RequestPayload, &a.Status, &a.FailureCode, &a.ResponsePayload); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// --- Verdict ---

func (t *sqliteTx) SaveEvidence(ctx context.Context, e verdict.DefectEvidence) error {
	_, err := t.ex.ExecContext(ctx, `
		INSERT INTO defect_evidence(evidence_id, task_id, generation, defect_kind, window_unit_id,
			pressure_ordinal, measurement_point_id, version, immutable_payload_hash, supersedes_id, closure_id)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(evidence_id) DO UPDATE SET closure_id=excluded.closure_id`,
		e.EvidenceID, e.TaskID, e.Generation, string(e.DefectKind), e.WindowUnitID,
		e.PressureOrdinal, e.MeasurementPointID, e.Version, e.ImmutablePayloadHash, e.SupersedesID, e.ClosureID)
	return err
}

func (t *sqliteTx) LoadEvidence(ctx context.Context, taskID string) ([]verdict.DefectEvidence, error) {
	rows, err := t.ex.QueryContext(ctx, `
		SELECT evidence_id, task_id, generation, defect_kind, window_unit_id, pressure_ordinal,
			measurement_point_id, version, immutable_payload_hash, supersedes_id, closure_id
		FROM defect_evidence WHERE task_id=? ORDER BY generation, version, evidence_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []verdict.DefectEvidence
	for rows.Next() {
		var e verdict.DefectEvidence
		if err := rows.Scan(&e.EvidenceID, &e.TaskID, &e.Generation, &e.DefectKind, &e.WindowUnitID,
			&e.PressureOrdinal, &e.MeasurementPointID, &e.Version, &e.ImmutablePayloadHash,
			&e.SupersedesID, &e.ClosureID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (t *sqliteTx) SaveRepairGeneration(ctx context.Context, r verdict.RepairGeneration) error {
	steps, err := json.Marshal(r.AffectedSteps)
	if err != nil {
		return err
	}
	checks, err := json.Marshal(r.AffectedCheckpoints)
	if err != nil {
		return err
	}
	reasons, err := json.Marshal(r.ReasonEvidenceIDs)
	if err != nil {
		return err
	}
	_, err = t.ex.ExecContext(ctx, `
		INSERT INTO repair_generations(task_id, generation, parent_generation, affected_steps, affected_checkpoints, reason_evidence_ids)
		VALUES(?,?,?,?,?,?)`,
		r.TaskID, r.Generation, r.ParentGeneration, steps, checks, reasons)
	return err
}

func (t *sqliteTx) LoadRepairs(ctx context.Context, taskID string) ([]verdict.RepairGeneration, error) {
	rows, err := t.ex.QueryContext(ctx, `
		SELECT generation, parent_generation, affected_steps, affected_checkpoints, reason_evidence_ids
		FROM repair_generations WHERE task_id=? ORDER BY generation`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []verdict.RepairGeneration
	for rows.Next() {
		var r verdict.RepairGeneration
		var steps, checks, reasons []byte
		if err := rows.Scan(&r.Generation, &r.ParentGeneration, &steps, &checks, &reasons); err != nil {
			return nil, err
		}
		r.TaskID = taskID
		_ = json.Unmarshal(steps, &r.AffectedSteps)
		_ = json.Unmarshal(checks, &r.AffectedCheckpoints)
		_ = json.Unmarshal(reasons, &r.ReasonEvidenceIDs)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t *sqliteTx) SaveReview(ctx context.Context, r verdict.Review) error {
	_, err := t.ex.ExecContext(ctx, `
		INSERT INTO reviews(task_id, reviewer_id, qualification_revision, verdict_hash, decision, operation_id)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(task_id, reviewer_id) DO NOTHING`,
		r.TaskID, r.ReviewerID, r.QualificationRevision, r.VerdictHash, string(r.Decision), r.OperationID)
	return err
}

func (t *sqliteTx) LoadReviews(ctx context.Context, taskID string) ([]verdict.Review, error) {
	rows, err := t.ex.QueryContext(ctx, `
		SELECT reviewer_id, qualification_revision, verdict_hash, decision, operation_id
		FROM reviews WHERE task_id=? ORDER BY reviewer_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []verdict.Review
	for rows.Next() {
		var r verdict.Review
		if err := rows.Scan(&r.ReviewerID, &r.QualificationRevision, &r.VerdictHash,
			&r.Decision, &r.OperationID); err != nil {
			return nil, err
		}
		r.TaskID = taskID
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t *sqliteTx) SaveCredential(ctx context.Context, c verdict.ReleaseCredential) error {
	_, err := t.ex.ExecContext(ctx, `
		INSERT INTO release_credentials(task_id, generation, verdict_hash, serial_number, issued_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(task_id) DO NOTHING`,
		c.TaskID, c.Generation, c.VerdictHash, c.SerialNumber, c.IssuedAt)
	return err
}

func (t *sqliteTx) LoadCredential(ctx context.Context, taskID string) (verdict.ReleaseCredential, bool, error) {
	var c verdict.ReleaseCredential
	err := t.ex.QueryRowContext(ctx,
		`SELECT task_id, generation, verdict_hash, serial_number, issued_at FROM release_credentials WHERE task_id=?`, taskID).
		Scan(&c.TaskID, &c.Generation, &c.VerdictHash, &c.SerialNumber, &c.IssuedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return verdict.ReleaseCredential{}, false, nil
	}
	if err != nil {
		return verdict.ReleaseCredential{}, false, err
	}
	return c, true, nil
}

// --- Idempotency and audit ---

func (t *sqliteTx) SaveOperationResult(ctx context.Context, r OperationResult) error {
	_, err := t.ex.ExecContext(ctx, `
		INSERT INTO operation_results(operation_id, request_hash, response_code, payload)
		VALUES(?,?,?,?)
		ON CONFLICT(operation_id) DO NOTHING`,
		r.OperationID, r.RequestHash, r.ResponseCode, r.Payload)
	return err
}

func (t *sqliteTx) LoadOperationResult(ctx context.Context, operationID string) (OperationResult, bool, error) {
	var r OperationResult
	err := t.ex.QueryRowContext(ctx,
		`SELECT operation_id, request_hash, response_code, payload FROM operation_results WHERE operation_id=?`, operationID).
		Scan(&r.OperationID, &r.RequestHash, &r.ResponseCode, &r.Payload)
	if errors.Is(err, sql.ErrNoRows) {
		return OperationResult{}, false, nil
	}
	if err != nil {
		return OperationResult{}, false, err
	}
	return r, true, nil
}

func (t *sqliteTx) AppendAudit(ctx context.Context, e AuditEvent) error {
	res, err := t.ex.ExecContext(ctx,
		`INSERT INTO audit_events(task_id, operation, event_type, payload) VALUES(?,?,?,?)`,
		e.TaskID, e.Operation, e.EventType, e.Payload)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err == nil {
		e.Sequence = id
	}
	return nil
}

// --- store-level (non-tx) readers delegate to a single-query execution ---

func (s *sqliteStore) LoadCatalog(ctx context.Context, revisionID string) (catalog.CatalogRevision, bool, error) {
	tx := s.readTx()
	return tx.LoadCatalog(ctx, revisionID)
}

func (s *sqliteStore) LoadTask(ctx context.Context, taskID string) (inspection.InspectionTask, bool, error) {
	tx := s.readTx()
	return tx.LoadTask(ctx, taskID)
}

func (s *sqliteStore) LoadTokens(ctx context.Context, taskID string) ([]occupancy.OccupancyToken, error) {
	tx := s.readTx()
	return tx.LoadTokens(ctx, taskID)
}

func (s *sqliteStore) LoadStepRecords(ctx context.Context, taskID string, generation int64) ([]acquisition.StepRecord, error) {
	tx := s.readTx()
	return tx.LoadStepRecords(ctx, taskID, generation)
}

func (s *sqliteStore) LoadSprayRecords(ctx context.Context, taskID string, generation int64) ([]acquisition.SprayRecord, error) {
	tx := s.readTx()
	return tx.LoadSprayRecords(ctx, taskID, generation)
}

func (s *sqliteStore) LoadAttempts(ctx context.Context, taskID string) ([]acquisition.InstrumentAttempt, error) {
	tx := s.readTx()
	return tx.LoadAttempts(ctx, taskID)
}

func (s *sqliteStore) LoadEvidence(ctx context.Context, taskID string) ([]verdict.DefectEvidence, error) {
	tx := s.readTx()
	return tx.LoadEvidence(ctx, taskID)
}

func (s *sqliteStore) LoadRepairs(ctx context.Context, taskID string) ([]verdict.RepairGeneration, error) {
	tx := s.readTx()
	return tx.LoadRepairs(ctx, taskID)
}

func (s *sqliteStore) LoadReviews(ctx context.Context, taskID string) ([]verdict.Review, error) {
	tx := s.readTx()
	return tx.LoadReviews(ctx, taskID)
}

func (s *sqliteStore) LoadCredential(ctx context.Context, taskID string) (verdict.ReleaseCredential, bool, error) {
	tx := s.readTx()
	return tx.LoadCredential(ctx, taskID)
}

func (s *sqliteStore) LoadOperationResult(ctx context.Context, operationID string) (OperationResult, bool, error) {
	tx := s.readTx()
	return tx.LoadOperationResult(ctx, operationID)
}

func (s *sqliteStore) LoadAllActiveTokens(ctx context.Context) ([]occupancy.OccupancyToken, error) {
	tx := s.readTx()
	return tx.LoadAllActiveTokens(ctx)
}

func (s *sqliteStore) readTx() *sqliteTx { return &sqliteTx{ex: s.db} }

// --- helpers ---

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

var idCounter uint64

func newTokenID() string {
	idCounter++
	return fmt.Sprintf("tok-%d", idCounter)
}

// nowMillis returns the current wall-clock time in milliseconds. It is a
// package variable so recovery tests can override it deterministically.
var storeNowMillis = time.Now().UnixMilli

func nowMillis() int64 { return storeNowMillis() }
