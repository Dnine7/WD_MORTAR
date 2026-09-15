package model

import (
	"math"
	"testing"
)

func TestDistance(t *testing.T) {
	if got := Distance(Point{X: 98.43, Y: 110.44}, Point{X: 98.13, Y: 110.69}); math.Abs(got-39.05124838) > 1e-8 {
		t.Fatalf("distance=%v", got)
	}
	// Regression: these OCR coordinates are about 1.51 coordinate units apart,
	// which is 151 metres in Wardogs rather than 1.51 metres.
	if got := Distance(Point{X: 98.42, Y: 110.40}, Point{X: 97.70, Y: 109.07}); math.Round(got) != 151 {
		t.Fatalf("screenshot distance=%v, rounded=%v", got, math.Round(got))
	}
	if got := Distance(Point{}, Point{}); got != 0 {
		t.Fatalf("same point distance=%v", got)
	}
}

func TestSnapshotHotkeyFlow(t *testing.T) {
	var snapshot Snapshot
	if snapshot.CalculateDistance() {
		t.Fatal("calculation unexpectedly succeeded without coordinates")
	}

	snapshot.RecordCurrent(CoordinateResult{Point: Point{X: 10, Y: -2}, Source: SourceChatInput})
	if snapshot.Distance != nil {
		t.Fatal("F8 should not calculate before a target exists")
	}

	snapshot.RecordTarget(CoordinateResult{Point: Point{X: 13, Y: 2}, Source: SourceMapCursor})
	if snapshot.Distance == nil || math.Abs(*snapshot.Distance-500) > 1e-9 {
		t.Fatalf("F9 automatic distance=%v", snapshot.Distance)
	}

	snapshot.RecordCurrent(CoordinateResult{Point: Point{X: 13, Y: 2}, Source: SourceChatInput})
	if snapshot.Distance == nil || *snapshot.Distance != 0 {
		t.Fatalf("updated current coordinate distance=%v", snapshot.Distance)
	}
}
