// Package occupancy models the specimen/chamber/measurement-point occupancy
// ledger. One-shot occupancy tokens carry a task and generation and are backed
// by a database unique constraint so that an active resource can be held by at
// most one open task. Acquisition, chamber replacement and terminal release are
// all-or-nothing operations.
package occupancy

// ResourceKind discriminates the three resource families tracked by the ledger.
type ResourceKind string

const (
	// ResourceWindowUnit is an installed window specimen.
	ResourceWindowUnit ResourceKind = "window_unit"
	// ResourceChamber is a test chamber.
	ResourceChamber ResourceKind = "chamber"
	// ResourceMeasurementPoint is a displacement measurement point.
	ResourceMeasurementPoint ResourceKind = "measurement_point"
)

// OccupancyToken is a one-shot hold of a single resource by a task generation.
type OccupancyToken struct {
	TokenID      string
	ResourceKind ResourceKind
	ResourceID   string
	TaskID       string
	Generation   int64
	Active       bool
}

// Request is a single resource requested for atomic acquisition.
type Request struct {
	ResourceKind ResourceKind
	ResourceID   string
}

// SortRequests orders requests by resource kind then resource id so that
// concurrent acquisitions contend in a deterministic order and deadlock-free
// fashion. It returns a fresh slice and does not mutate the input.
func SortRequests(reqs []Request) []Request {
	out := make([]Request, len(reqs))
	copy(out, reqs)
	less := func(a, b Request) bool {
		if a.ResourceKind != b.ResourceKind {
			return a.ResourceKind < b.ResourceKind
		}
		return a.ResourceID < b.ResourceID
	}
	// Insertion sort is sufficient for the small request sets in this domain and
	// keeps the sort stable without pulling in reflection.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && less(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
