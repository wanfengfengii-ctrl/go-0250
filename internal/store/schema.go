package store

// schema is the full DDL applied at startup. It is intentionally idempotent via
// IF NOT EXISTS and never mutates an existing column set; startup recovery
// validates invariants against this shape.
const schema = `
CREATE TABLE IF NOT EXISTS catalog_revisions (
	revision_id  TEXT PRIMARY KEY,
	snapshot_hash TEXT NOT NULL,
	payload      BLOB NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
	task_id                 TEXT PRIMARY KEY,
	generation              INTEGER NOT NULL,
	state                   TEXT NOT NULL,
	state_revision          INTEGER NOT NULL,
	locked_catalog_revision TEXT NOT NULL DEFAULT '',
	locked_snapshot_hash    TEXT NOT NULL DEFAULT '',
	locked_plan_id          TEXT NOT NULL DEFAULT '',
	window_unit_id          TEXT NOT NULL DEFAULT '',
	chamber_id              TEXT NOT NULL DEFAULT '',
	measurement_point_ids   BLOB NOT NULL DEFAULT '[]',
	spray_started_at        INTEGER NOT NULL DEFAULT 0,
	orientation_confirmed   INTEGER NOT NULL DEFAULT 0,
	sealed_confirmed        INTEGER NOT NULL DEFAULT 0,
	points_zeroed           INTEGER NOT NULL DEFAULT 0,
	current_phase           TEXT NOT NULL DEFAULT '',
	terminal_kind           TEXT NOT NULL DEFAULT '',
	created_at              INTEGER NOT NULL DEFAULT 0,
	updated_at              INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS occupancy_tokens (
	token_id      TEXT PRIMARY KEY,
	resource_kind TEXT NOT NULL,
	resource_id   TEXT NOT NULL,
	task_id       TEXT NOT NULL,
	generation    INTEGER NOT NULL,
	active        INTEGER NOT NULL DEFAULT 1,
	acquired_at   INTEGER NOT NULL DEFAULT 0,
	released_at   INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_active_token
	ON occupancy_tokens(resource_kind, resource_id) WHERE active = 1;

CREATE TABLE IF NOT EXISTS step_records (
	task_id        TEXT NOT NULL,
	generation     INTEGER NOT NULL,
	phase          TEXT NOT NULL,
	polarity       TEXT NOT NULL,
	ordinal        INTEGER NOT NULL,
	actual_pa      INTEGER NOT NULL,
	airflow_cc_per_sec INTEGER NOT NULL DEFAULT 0,
	displacement_micron INTEGER NOT NULL DEFAULT 0,
	operation_id   TEXT NOT NULL,
	content_hash   TEXT NOT NULL,
	passed         INTEGER NOT NULL,
	PRIMARY KEY (task_id, generation, phase, polarity, ordinal)
);

CREATE TABLE IF NOT EXISTS spray_records (
	task_id       TEXT NOT NULL,
	generation    INTEGER NOT NULL,
	checkpoint_id TEXT NOT NULL,
	actual_pa     INTEGER NOT NULL,
	sensor_status TEXT NOT NULL,
	observation   TEXT NOT NULL DEFAULT '',
	covered       INTEGER NOT NULL,
	PRIMARY KEY (task_id, generation, checkpoint_id)
);

CREATE TABLE IF NOT EXISTS instrument_attempts (
	attempt_id       TEXT PRIMARY KEY,
	operation_id     TEXT NOT NULL,
	task_id          TEXT NOT NULL,
	generation       INTEGER NOT NULL,
	kind             TEXT NOT NULL,
	request_hash     TEXT NOT NULL,
	request_payload  TEXT NOT NULL DEFAULT '',
	status           TEXT NOT NULL,
	failure_code     TEXT NOT NULL DEFAULT '',
	response_payload TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS defect_evidence (
	evidence_id            TEXT PRIMARY KEY,
	task_id                TEXT NOT NULL,
	generation             INTEGER NOT NULL,
	defect_kind            TEXT NOT NULL,
	window_unit_id         TEXT NOT NULL DEFAULT '',
	pressure_ordinal       INTEGER NOT NULL DEFAULT 0,
	measurement_point_id   TEXT NOT NULL DEFAULT '',
	version                INTEGER NOT NULL,
	immutable_payload_hash TEXT NOT NULL,
	supersedes_id          TEXT NOT NULL DEFAULT '',
	closure_id             TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS repair_generations (
	task_id              TEXT NOT NULL,
	generation           INTEGER NOT NULL,
	parent_generation    INTEGER NOT NULL,
	affected_steps       BLOB NOT NULL DEFAULT '[]',
	affected_checkpoints BLOB NOT NULL DEFAULT '[]',
	reason_evidence_ids  BLOB NOT NULL DEFAULT '[]',
	PRIMARY KEY (task_id, generation)
);

CREATE TABLE IF NOT EXISTS reviews (
	task_id                TEXT NOT NULL,
	reviewer_id            TEXT NOT NULL,
	qualification_revision TEXT NOT NULL,
	verdict_hash           TEXT NOT NULL,
	decision               TEXT NOT NULL,
	operation_id           TEXT NOT NULL,
	PRIMARY KEY (task_id, reviewer_id)
);

CREATE TABLE IF NOT EXISTS release_credentials (
	task_id       TEXT PRIMARY KEY,
	generation    INTEGER NOT NULL,
	verdict_hash  TEXT NOT NULL,
	serial_number TEXT NOT NULL,
	issued_at     INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS operation_results (
	operation_id   TEXT PRIMARY KEY,
	request_hash   TEXT NOT NULL,
	response_code  TEXT NOT NULL,
	payload        BLOB NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_events (
	sequence  INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id   TEXT NOT NULL,
	operation TEXT NOT NULL,
	event_type TEXT NOT NULL,
	payload   BLOB NOT NULL
);
`
