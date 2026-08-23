package catalog

import (
	"fmt"
	"sort"
)

// ValidationError describes a single catalog-rule mismatch with a stable field
// label so callers can sort and surface rejection reasons deterministically.
type ValidationError struct {
	Code  string
	Field string
	Value string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Field)
}

// WindowUnitByID returns the window unit with the given id and whether it
// exists within the revision.
func (r CatalogRevision) WindowUnitByID(unitID string) (WindowUnit, bool) {
	for _, u := range r.WindowUnits {
		if u.UnitID == unitID {
			return u, true
		}
	}
	return WindowUnit{}, false
}

// PlanByID returns the test plan with the given id and whether it exists.
func (r CatalogRevision) PlanByID(planID string) (TestPlan, bool) {
	for _, p := range r.TestPlans {
		if p.PlanID == planID {
			return p, true
		}
	}
	return TestPlan{}, false
}

// OrientationsSorted returns a copy of the sorted orientation list.
func (r CatalogRevision) OrientationsSorted() []Orientation {
	out := append([]Orientation(nil), r.Orientations...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ValidateLock validates that a lock request's window unit, orientation and
// three material batches all match the frozen revision rules. It returns every
// mismatch rather than stopping at the first, so the caller can produce a fully
// sorted rejection.
func (r CatalogRevision) ValidateLock(unitID, profile, glass, seal string) []*ValidationError {
	var errs []*ValidationError
	unit, ok := r.WindowUnitByID(unitID)
	if !ok {
		errs = append(errs, &ValidationError{Code: "UNIT_UNKNOWN", Field: "window_unit", Value: unitID})
		return errs
	}
	zoneKnown := false
	for _, z := range r.FacadeZones {
		if z.ZoneID == unit.ZoneID {
			zoneKnown = true
			break
		}
	}
	if !zoneKnown {
		errs = append(errs, &ValidationError{Code: "ZONE_UNKNOWN", Field: "facade_zone", Value: unit.ZoneID})
	}
	orientKnown := false
	for _, o := range r.Orientations {
		if o == unit.Orientation {
			orientKnown = true
			break
		}
	}
	if !orientKnown {
		errs = append(errs, &ValidationError{Code: "ORIENTATION_UNKNOWN", Field: "orientation", Value: string(unit.Orientation)})
	}
	if unit.ProfileBatch != profile {
		errs = append(errs, &ValidationError{Code: "PROFILE_BATCH_MISMATCH", Field: "profile_batch", Value: profile})
	}
	if unit.GlassBatch != glass {
		errs = append(errs, &ValidationError{Code: "GLASS_BATCH_MISMATCH", Field: "glass_batch", Value: glass})
	}
	if unit.SealBatch != seal {
		errs = append(errs, &ValidationError{Code: "SEAL_BATCH_MISMATCH", Field: "seal_batch", Value: seal})
	}
	return errs
}

// PressureStepsFor returns the pressure steps of a phase ordered by the fixed
// (phase, polarity, ordinal) sort. Only steps whose Phase equals phase are
// returned.
func (p TestPlan) PressureStepsFor(phase string) []PressureStepSpec {
	var out []PressureStepSpec
	for _, s := range p.PressureSteps {
		if s.Phase == phase {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Polarity != out[j].Polarity {
			return out[i].Polarity < out[j].Polarity
		}
		return out[i].Ordinal < out[j].Ordinal
	})
	return out
}

// CheckpointsSorted returns the plan's spray checkpoints ordered by pressure
// ordinal then checkpoint id.
func (p TestPlan) CheckpointsSorted() []SprayCheckpointSpec {
	out := append([]SprayCheckpointSpec(nil), p.SprayCheckpoints...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].PressureOrdinal != out[j].PressureOrdinal {
			return out[i].PressureOrdinal < out[j].PressureOrdinal
		}
		return out[i].CheckpointID < out[j].CheckpointID
	})
	return out
}
