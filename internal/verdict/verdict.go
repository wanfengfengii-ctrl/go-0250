// Package verdict models the defect-evidence and review/terminal arbiter: the
// immutable evidence version chain, repair generations, dual independent review
// with identity separation, the release summary and the single terminal
// credential.
package verdict

// DefectKind classifies a defect observation.
type DefectKind string

const (
	// DefectWaterLeak is an observed water leakage.
	DefectWaterLeak DefectKind = "water_leak"
	// DefectSealFailure is a seal/gasket failure.
	DefectSealFailure DefectKind = "seal_failure"
	// DefectResidualDeformation is a residual deformation after unloading.
	DefectResidualDeformation DefectKind = "residual_deformation"
)

// DefectEvidence is an append-only, immutable evidence record. Once written a
// version is never updated; supersession links to a newer version.
type DefectEvidence struct {
	EvidenceID           string
	TaskID               string
	Generation           int64
	DefectKind           DefectKind
	WindowUnitID         string
	PressureOrdinal      int
	MeasurementPointID   string
	Version              int64
	ImmutablePayloadHash string
	SupersedesID         string
	ClosureID            string
}

// RepairGeneration records a repair that increments the task generation and
// reopens only the explicitly affected steps and checkpoints.
type RepairGeneration struct {
	TaskID              string
	Generation          int64
	ParentGeneration    int64
	AffectedSteps       []string
	AffectedCheckpoints []string
	ReasonEvidenceIDs   []string
}

// ReviewDecision is the decision recorded by an independent reviewer.
type ReviewDecision string

const (
	// ReviewPass approves release.
	ReviewPass ReviewDecision = "pass"
	// ReviewFail rejects release.
	ReviewFail ReviewDecision = "fail"
)

// Review is a single independent review seat. A natural person may occupy at
// most one seat per task generation: a repair that increments the generation
// frees the seats of prior generations. Their reviews are retained as
// immutable audit history and never count toward the current generation's
// conclusion, mirroring the generation scoping of evidence, step and spray
// records.
type Review struct {
	TaskID                string
	Generation            int64
	ReviewerID            string
	QualificationRevision string
	VerdictHash           string
	Decision              ReviewDecision
	OperationID           string
}

// ReleaseCredential is the unique terminal release credential produced when a
// task is released.
type ReleaseCredential struct {
	TaskID       string
	Generation   int64
	VerdictHash  string
	SerialNumber string
	IssuedAt     int64
}
