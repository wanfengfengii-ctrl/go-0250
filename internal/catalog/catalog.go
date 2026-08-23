// Package catalog models the construction-and-material rules catalogue: facade
// zones, window units, orientations, material batches, qualified people, test
// plans and their versioned revisions. A revision is the immutable unit that a
// task locks against; its normalized snapshot hash is the digest frozen into an
// inspection task at lock time.
package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Orientation is the facing direction of a window unit.
type Orientation string

// MaterialKind discriminates the three material batch families tracked by the
// catalogue.
type MaterialKind string

const (
	// Profile is the aluminium/steel profile batch family.
	Profile MaterialKind = "profile"
	// Glass is the glazing batch family.
	Glass MaterialKind = "glass"
	// Seal is the sealing/gasket batch family.
	Seal MaterialKind = "seal"
)

// FacadeZone identifies a building facade partition.
type FacadeZone struct {
	ZoneID string
	Name   string
}

// WindowUnit is an installed window specimen belonging to exactly one facade
// zone and facing one orientation.
type WindowUnit struct {
	UnitID       string
	ZoneID       string
	Orientation  Orientation
	ProfileBatch string
	GlassBatch   string
	SealBatch    string
}

// MaterialBatch is a versioned batch of a single material family.
type MaterialBatch struct {
	BatchID string
	Kind    MaterialKind
	Spec    string
}

// QualifiedPerson is a natural person eligible to perform independent review
// under a specific qualification revision.
type QualifiedPerson struct {
	PersonID              string
	QualificationRevision string
}

// TestPlan freezes the pressure sequence, spray checkpoints, thresholds and
// durations for a revision.
type TestPlan struct {
	PlanID                       string
	PressureSteps                []PressureStepSpec
	SprayCheckpoints             []SprayCheckpointSpec
	DisplacementThresholdMicrons int64
	SprayTargetPa                int64
	SprayTolerancePa             int64
}

// PressureStepSpec is a frozen pressure loading level.
type PressureStepSpec struct {
	Phase                   string
	Polarity                string
	Ordinal                 int
	TargetPa                int64
	TolerancePa             int64
	RequiredDurationSeconds int64
}

// SprayCheckpointSpec is a frozen spray checkpoint time window.
type SprayCheckpointSpec struct {
	CheckpointID       string
	PressureOrdinal    int
	Zone               string
	StartOffsetSeconds int64
	EndOffsetSeconds   int64
}

// CatalogRevision is an immutable, versioned snapshot of the catalogue. Its
// collections are kept sorted by business key so that the snapshot hash is
// deterministic.
type CatalogRevision struct {
	RevisionID      string
	FacadeZones     []FacadeZone
	WindowUnits     []WindowUnit
	Orientations    []Orientation
	ProfileBatches  []MaterialBatch
	GlassBatches    []MaterialBatch
	SealBatches     []MaterialBatch
	QualifiedPeople []QualifiedPerson
	TestPlans       []TestPlan
}

// SnapshotHash returns the normalized SHA-256 digest of the revision. Every
// collection is sorted by its business key before hashing so that two equal
// revisions always produce the same digest regardless of insertion order.
func (r CatalogRevision) SnapshotHash() string {
	var b strings.Builder

	writeZone := func(z FacadeZone) {
		fmt.Fprintf(&b, "zone:%s=%s\n", z.ZoneID, z.Name)
	}
	writeUnit := func(u WindowUnit) {
		fmt.Fprintf(&b, "unit:%s=%s|%s|%s|%s|%s\n",
			u.UnitID, u.ZoneID, u.Orientation, u.ProfileBatch, u.GlassBatch, u.SealBatch)
	}
	writeBatch := func(m MaterialBatch) {
		fmt.Fprintf(&b, "batch:%s=%s:%s\n", m.Kind, m.BatchID, m.Spec)
	}
	writePerson := func(p QualifiedPerson) {
		fmt.Fprintf(&b, "person:%s=%s\n", p.PersonID, p.QualificationRevision)
	}
	writeStep := func(s PressureStepSpec) {
		fmt.Fprintf(&b, "step:%s|%s|%d=%d/%d/%d\n",
			s.Phase, s.Polarity, s.Ordinal, s.TargetPa, s.TolerancePa, s.RequiredDurationSeconds)
	}
	writeCheckpoint := func(c SprayCheckpointSpec) {
		fmt.Fprintf(&b, "checkpoint:%s=%d|%s|%d|%d\n",
			c.CheckpointID, c.PressureOrdinal, c.Zone, c.StartOffsetSeconds, c.EndOffsetSeconds)
	}

	zones := append([]FacadeZone(nil), r.FacadeZones...)
	sort.Slice(zones, func(i, j int) bool { return zones[i].ZoneID < zones[j].ZoneID })
	for _, z := range zones {
		writeZone(z)
	}

	units := append([]WindowUnit(nil), r.WindowUnits...)
	sort.Slice(units, func(i, j int) bool { return units[i].UnitID < units[j].UnitID })
	for _, u := range units {
		writeUnit(u)
	}

	orientations := append([]Orientation(nil), r.Orientations...)
	sort.Slice(orientations, func(i, j int) bool { return orientations[i] < orientations[j] })
	for _, o := range orientations {
		fmt.Fprintf(&b, "orientation:%s\n", o)
	}

	profiles := append([]MaterialBatch(nil), r.ProfileBatches...)
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].BatchID < profiles[j].BatchID })
	for _, m := range profiles {
		writeBatch(m)
	}

	glasses := append([]MaterialBatch(nil), r.GlassBatches...)
	sort.Slice(glasses, func(i, j int) bool { return glasses[i].BatchID < glasses[j].BatchID })
	for _, m := range glasses {
		writeBatch(m)
	}

	seals := append([]MaterialBatch(nil), r.SealBatches...)
	sort.Slice(seals, func(i, j int) bool { return seals[i].BatchID < seals[j].BatchID })
	for _, m := range seals {
		writeBatch(m)
	}

	people := append([]QualifiedPerson(nil), r.QualifiedPeople...)
	sort.Slice(people, func(i, j int) bool { return people[i].PersonID < people[j].PersonID })
	for _, p := range people {
		writePerson(p)
	}

	plans := append([]TestPlan(nil), r.TestPlans...)
	sort.Slice(plans, func(i, j int) bool { return plans[i].PlanID < plans[j].PlanID })
	for _, tp := range plans {
		fmt.Fprintf(&b, "plan:%s=%d/%d/%d\n", tp.PlanID, tp.DisplacementThresholdMicrons, tp.SprayTargetPa, tp.SprayTolerancePa)
		steps := append([]PressureStepSpec(nil), tp.PressureSteps...)
		sort.Slice(steps, func(i, j int) bool {
			if steps[i].Phase != steps[j].Phase {
				return steps[i].Phase < steps[j].Phase
			}
			if steps[i].Polarity != steps[j].Polarity {
				return steps[i].Polarity < steps[j].Polarity
			}
			return steps[i].Ordinal < steps[j].Ordinal
		})
		for _, s := range steps {
			writeStep(s)
		}
		checks := append([]SprayCheckpointSpec(nil), tp.SprayCheckpoints...)
		sort.Slice(checks, func(i, j int) bool {
			if checks[i].PressureOrdinal != checks[j].PressureOrdinal {
				return checks[i].PressureOrdinal < checks[j].PressureOrdinal
			}
			return checks[i].CheckpointID < checks[j].CheckpointID
		})
		for _, c := range checks {
			writeCheckpoint(c)
		}
	}

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
