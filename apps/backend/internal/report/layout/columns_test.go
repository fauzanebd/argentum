package layout

import (
	"math"
	"testing"
)

func TestDistributeSumsToTheGrid(t *testing.T) {
	cases := [][]float64{
		{40, 30, 20, 10},
		{1, 1, 1},
		{93.4, 12.1, 12.1, 12.1, 12.1, 12.1, 12.1, 12.1},
		{0, 0, 0},
	}
	for _, weights := range cases {
		units := Distribute(weights, 120, 6)
		sum := 0
		for _, u := range units {
			sum += u
		}
		if sum != 120 {
			t.Errorf("Distribute(%v) sums to %d, want 120 (%v)", weights, sum, units)
		}
	}
}

// The bug this replaced: reserving the minimum per column up front and dividing
// the remainder in proportion to full widths, which hands every wide column
// less than it asked for. With one column wanting 60% of an eight-column table,
// the reserving version gives it 6 + 0.6*72 = 49 units; the right answer is 72.
func TestDistributeDoesNotStarveAWideColumn(t *testing.T) {
	weights := []float64{60, 5.7, 5.7, 5.7, 5.7, 5.7, 5.7, 5.8}
	units := Distribute(weights, 120, 6)
	if units[0] < 65 {
		t.Errorf("the column asking for 60%% of the table got %d of 120 units: %v", units[0], units)
	}
	for i, u := range units {
		if u < 6 {
			t.Errorf("column %d is %d units, below the readable minimum: %v", i, u, units)
		}
	}
}

func TestDistributeShrinksTheMinimumRatherThanTheTable(t *testing.T) {
	weights := make([]float64, 30)
	for i := range weights {
		weights[i] = 1
	}
	units := Distribute(weights, 120, 6) // 30 × 6 = 180 > 120
	sum := 0
	for _, u := range units {
		sum += u
		if u < 1 {
			t.Errorf("a column came out at %d units", u)
		}
	}
	if sum != 120 {
		t.Errorf("sums to %d, want 120", sum)
	}
}

// A rigid column is paid what it measured; the flexible ones reflow into what
// is left. This is the whole reason the two kinds are distinguished.
func TestAllocatePaysRigidColumnsFirst(t *testing.T) {
	natural := []float64{120, 30, 30} // 180mm of demand
	rigid := []bool{false, true, true}
	got := Allocate(natural, rigid, 150, 10)

	if got[1] != 30 || got[2] != 30 {
		t.Errorf("rigid columns were narrowed: %v", got)
	}
	if math.Abs(got[0]-90) > 0.001 {
		t.Errorf("the flexible column got %v, want the remaining 90", got[0])
	}
}

// Over-subscribed the other way: the rigid columns alone leave the flexible one
// unreadable, so nothing is rigid and everything shrinks together.
func TestAllocateGivesUpWhenRigidColumnsDoNotFit(t *testing.T) {
	natural := []float64{40, 80, 80}
	rigid := []bool{false, true, true}
	got := Allocate(natural, rigid, 150, 30)
	for i := range natural {
		if got[i] != natural[i] {
			t.Fatalf("expected the natural widths back unchanged, got %v", got)
		}
	}
}

func TestScaleFillsTheMeasureAndHoldsTheFloor(t *testing.T) {
	got := Scale([]float64{100, 4, 4, 4}, 200, 20)
	sum := 0.0
	for i, w := range got {
		if w < 20-0.001 {
			t.Errorf("column %d is %vmm, under the 20mm floor: %v", i, w, got)
		}
		sum += w
	}
	if math.Abs(sum-200) > 0.001 {
		t.Errorf("widths sum to %v, want 200: %v", sum, got)
	}
}

func TestScaleWithNoWeightsDividesEvenly(t *testing.T) {
	got := Scale([]float64{0, 0, 0, 0}, 200, 10)
	for _, w := range got {
		if math.Abs(w-50) > 0.001 {
			t.Fatalf("want four 50mm columns, got %v", got)
		}
	}
}
