package catalog

import "testing"

func sampleRevision() CatalogRevision {
	return CatalogRevision{
		RevisionID: "rev-1",
		FacadeZones: []FacadeZone{
			{ZoneID: "zone-b", Name: "West"},
			{ZoneID: "zone-a", Name: "East"},
		},
		WindowUnits: []WindowUnit{
			{UnitID: "W-E-02", ZoneID: "zone-a", Orientation: "east", ProfileBatch: "p1", GlassBatch: "g1", SealBatch: "s1"},
			{UnitID: "W-E-01", ZoneID: "zone-a", Orientation: "east", ProfileBatch: "p1", GlassBatch: "g1", SealBatch: "s1"},
		},
		Orientations: []Orientation{"east", "north"},
		ProfileBatches: []MaterialBatch{
			{BatchID: "p2", Kind: Profile, Spec: "al"},
			{BatchID: "p1", Kind: Profile, Spec: "al"},
		},
		GlassBatches: []MaterialBatch{
			{BatchID: "g1", Kind: Glass, Spec: "tempered"},
		},
		SealBatches: []MaterialBatch{
			{BatchID: "s1", Kind: Seal, Spec: "epdm"},
		},
		QualifiedPeople: []QualifiedPerson{
			{PersonID: "person-b", QualificationRevision: "q1"},
			{PersonID: "person-a", QualificationRevision: "q1"},
		},
		TestPlans: []TestPlan{
			{
				PlanID: "plan-1",
				PressureSteps: []PressureStepSpec{
					{Phase: "air", Polarity: "negative", Ordinal: 1, TargetPa: -100, TolerancePa: 10, RequiredDurationSeconds: 5},
					{Phase: "air", Polarity: "positive", Ordinal: 1, TargetPa: 100, TolerancePa: 10, RequiredDurationSeconds: 5},
				},
				SprayCheckpoints: []SprayCheckpointSpec{
					{CheckpointID: "c2", PressureOrdinal: 1, Zone: "a", StartOffsetSeconds: 1, EndOffsetSeconds: 2},
					{CheckpointID: "c1", PressureOrdinal: 1, Zone: "a", StartOffsetSeconds: 0, EndOffsetSeconds: 1},
				},
				DisplacementThresholdMicrons: 500,
			},
		},
	}
}

func TestSnapshotHashDeterministic(t *testing.T) {
	a := sampleRevision()
	b := sampleRevision()
	if a.SnapshotHash() != b.SnapshotHash() {
		t.Fatalf("equal revisions produced different hashes")
	}
}

func TestSnapshotHashEmpty(t *testing.T) {
	if (CatalogRevision{}).SnapshotHash() == "" {
		t.Fatalf("empty revision produced empty hash")
	}
}

func TestSnapshotHashChangesWithContent(t *testing.T) {
	a := sampleRevision()
	altered := sampleRevision()
	altered.WindowUnits[0].Orientation = "west"
	if a.SnapshotHash() == altered.SnapshotHash() {
		t.Fatalf("content change did not alter snapshot hash")
	}
}
