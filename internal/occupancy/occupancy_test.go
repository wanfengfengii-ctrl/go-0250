package occupancy

import "testing"

func TestSortRequestsDeterministic(t *testing.T) {
	in := []Request{
		{ResourceChamber, "chamber-2"},
		{ResourceMeasurementPoint, "mp-1"},
		{ResourceChamber, "chamber-1"},
		{ResourceWindowUnit, "W-E-01"},
	}
	want := []Request{
		{ResourceChamber, "chamber-1"},
		{ResourceChamber, "chamber-2"},
		{ResourceMeasurementPoint, "mp-1"},
		{ResourceWindowUnit, "W-E-01"},
	}
	got := SortRequests(in)
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	// Input must not be mutated.
	if in[0].ResourceID != "chamber-2" {
		t.Fatalf("input slice was mutated")
	}
}

func TestSortRequestsEmpty(t *testing.T) {
	if got := SortRequests(nil); got == nil || len(got) != 0 {
		t.Fatalf("empty input should return empty slice")
	}
}
