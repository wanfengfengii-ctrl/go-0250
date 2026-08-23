package catalog

// DemoRevision returns the fixed reference catalogue used by the running
// service and the smoke test. It describes building A's east facade with a
// single installed window unit W-E-01, its three material batches, two
// qualified reviewers and one test plan covering air, water and wind.
func DemoRevision() CatalogRevision {
	return CatalogRevision{
		RevisionID: "rev-demo",
		FacadeZones: []FacadeZone{
			{ZoneID: "zone-a", Name: "A座东立面"},
		},
		WindowUnits: []WindowUnit{
			{UnitID: "W-E-01", ZoneID: "zone-a", Orientation: "east", ProfileBatch: "p1", GlassBatch: "g1", SealBatch: "s1"},
		},
		Orientations: []Orientation{"east"},
		ProfileBatches: []MaterialBatch{
			{BatchID: "p1", Kind: Profile, Spec: "aluminium"},
		},
		GlassBatches: []MaterialBatch{
			{BatchID: "g1", Kind: Glass, Spec: "tempered"},
		},
		SealBatches: []MaterialBatch{
			{BatchID: "s1", Kind: Seal, Spec: "epdm"},
		},
		QualifiedPeople: []QualifiedPerson{
			{PersonID: "reviewer-a", QualificationRevision: "q1"},
			{PersonID: "reviewer-b", QualificationRevision: "q1"},
		},
		TestPlans: []TestPlan{
			{
				PlanID: "plan-1",
				PressureSteps: []PressureStepSpec{
					{Phase: "air", Polarity: "negative", Ordinal: 1, TargetPa: -100, TolerancePa: 10, RequiredDurationSeconds: 5},
					{Phase: "air", Polarity: "positive", Ordinal: 1, TargetPa: 100, TolerancePa: 10, RequiredDurationSeconds: 5},
					{Phase: "wind", Polarity: "positive", Ordinal: 1, TargetPa: 1000, TolerancePa: 50, RequiredDurationSeconds: 5},
				},
				SprayCheckpoints: []SprayCheckpointSpec{
					{CheckpointID: "c1", PressureOrdinal: 1, Zone: "a", StartOffsetSeconds: 0, EndOffsetSeconds: 10},
				},
				DisplacementThresholdMicrons: 500,
				SprayTargetPa:                100,
				SprayTolerancePa:             10,
			},
		},
	}
}
