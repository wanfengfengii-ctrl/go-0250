// Package store defines the persistence boundary for WindowProof. All domain
// records, idempotent operation results and audit events share a single SQLite
// transaction so that any validation or write failure rolls back atomically.
//
// The boundary is split into two views: a Store exposes read-only queries plus
// a serialized Write transaction (SQLite BEGIN IMMEDIATE), while a Tx exposes
// the same queries and all mutations against a single open transaction. The
// service layer composes compound operations entirely inside a Tx so that a
// partial failure can never leave half-applied state.
package store

import (
	"context"

	"github.com/windowproof/fenestration/internal/acquisition"
	"github.com/windowproof/fenestration/internal/catalog"
	"github.com/windowproof/fenestration/internal/inspection"
	"github.com/windowproof/fenestration/internal/occupancy"
	"github.com/windowproof/fenestration/internal/verdict"
)

// OperationResult is the deterministic, idempotent response recorded for an
// operation_id. Replaying the same operation_id with the same normalized
// request returns this result; a different request returns
// codes.OperationContentConflict.
type OperationResult struct {
	OperationID  string
	RequestHash  string
	ResponseCode string
	Payload      []byte
}

// AuditEvent is an append-only event with a monotonically increasing sequence
// number inside its transaction.
type AuditEvent struct {
	Sequence  int64
	TaskID    string
	Operation string
	EventType string
	Payload   []byte
}

// Querier is the read-only half of the persistence boundary. Every query is
// usable both outside a transaction (against the connection) and inside a Tx.
type Querier interface {
	LoadCatalog(ctx context.Context, revisionID string) (catalog.CatalogRevision, bool, error)
	LoadTask(ctx context.Context, taskID string) (inspection.InspectionTask, bool, error)
	LoadTokens(ctx context.Context, taskID string) ([]occupancy.OccupancyToken, error)
	LoadStepRecords(ctx context.Context, taskID string, generation int64) ([]acquisition.StepRecord, error)
	LoadSprayRecords(ctx context.Context, taskID string, generation int64) ([]acquisition.SprayRecord, error)
	LoadAttempts(ctx context.Context, taskID string) ([]acquisition.InstrumentAttempt, error)
	LoadEvidence(ctx context.Context, taskID string) ([]verdict.DefectEvidence, error)
	LoadRepairs(ctx context.Context, taskID string) ([]verdict.RepairGeneration, error)
	LoadReviews(ctx context.Context, taskID string, generation int64) ([]verdict.Review, error)
	LoadCredential(ctx context.Context, taskID string) (verdict.ReleaseCredential, bool, error)
	LoadOperationResult(ctx context.Context, operationID string) (OperationResult, bool, error)
	LoadAllActiveTokens(ctx context.Context) ([]occupancy.OccupancyToken, error)
}

// Mutator is the write half of the persistence boundary. All mutations run
// inside a single transaction.
type Mutator interface {
	SaveCatalog(ctx context.Context, rev catalog.CatalogRevision) error
	SaveTask(ctx context.Context, task inspection.InspectionTask) error
	AcquireTokens(ctx context.Context, taskID string, generation int64, reqs []occupancy.Request) ([]occupancy.OccupancyToken, error)
	ReleaseToken(ctx context.Context, taskID string, kind occupancy.ResourceKind, resourceID string, generation int64) error
	ReleaseAllTokens(ctx context.Context, taskID string, generation int64) error
	SaveStepRecord(ctx context.Context, rec acquisition.StepRecord) error
	SaveSprayRecord(ctx context.Context, rec acquisition.SprayRecord) error
	SaveAttempt(ctx context.Context, a acquisition.InstrumentAttempt) error
	SaveEvidence(ctx context.Context, e verdict.DefectEvidence) error
	SaveRepairGeneration(ctx context.Context, r verdict.RepairGeneration) error
	SaveReview(ctx context.Context, r verdict.Review) error
	SaveCredential(ctx context.Context, c verdict.ReleaseCredential) error
	SaveOperationResult(ctx context.Context, r OperationResult) error
	AppendAudit(ctx context.Context, e AuditEvent) error
}

// Tx is a single open, serialized write transaction exposing both queries and
// mutations.
type Tx interface {
	Querier
	Mutator
}

// Store is the top-level persistence facade owning the SQLite WAL connection.
type Store interface {
	Querier
	// Write runs fn inside a single SQLite BEGIN IMMEDIATE transaction. On any
	// error the transaction rolls back and nothing is applied.
	Write(ctx context.Context, fn func(Tx) error) error
	// Recover validates open occupancy uniqueness, completed per-phase prefixes,
	// spray coverage subsets, evidence chain references and the single terminal,
	// refusing to start on a broken invariant.
	Recover(ctx context.Context) error
	// Close releases the underlying database.
	Close() error
}

// CommitFailpoint is an optional fault-injection seam implemented by the SQLite
// store. It lets recovery tests interrupt a commit to verify atomic rollback.
type CommitFailpoint interface {
	FailNextCommits(n int)
}
