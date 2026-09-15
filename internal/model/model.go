package model

import "math"

// CoordinateUnitMetres converts one Wardogs coordinate unit to metres.
const CoordinateUnitMetres = 100.0

// Point is a two-dimensional Wardogs game coordinate.
type Point struct {
	X float64
	Y float64
}

// CoordinateSource identifies the screen region from which a coordinate was
// read.
type CoordinateSource int

const (
	SourceChatInput CoordinateSource = iota
	SourceMapCursor
)

func (s CoordinateSource) String() string {
	switch s {
	case SourceChatInput:
		return "聊天输入框"
	case SourceMapCursor:
		return "小地图"
	default:
		return "未知来源"
	}
}

// CoordinateResult is the parsed coordinate and the OCR source used to find
// it. Raw is retained for troubleshooting and is never shown automatically.
type CoordinateResult struct {
	Point  Point
	Source CoordinateSource
	Raw    string
}

func Distance(current, target Point) float64 {
	return math.Hypot(target.X-current.X, target.Y-current.Y) * CoordinateUnitMetres
}

// Snapshot is the immutable view rendered by the overlay.
type Snapshot struct {
	Current        *CoordinateResult
	Target         *CoordinateResult
	Distance       *float64
	Status         string
	Error          string
	OverlayVisible bool
}

// RecordCurrent stores a successful OCR result and recomputes the distance
// when a target is already present.
func (s *Snapshot) RecordCurrent(result CoordinateResult) {
	copyResult := result
	s.Current = &copyResult
	s.recompute()
}

// RecordTarget stores a successful OCR result and implements F9's automatic
// calculation behavior when the current coordinate is already present.
func (s *Snapshot) RecordTarget(result CoordinateResult) {
	copyResult := result
	s.Target = &copyResult
	s.recompute()
}

// CalculateDistance recomputes the value and reports whether both required
// coordinates were available.
func (s *Snapshot) CalculateDistance() bool {
	if s.Current == nil || s.Target == nil {
		s.Distance = nil
		return false
	}
	s.recompute()
	return true
}

func (s *Snapshot) recompute() {
	if s.Current == nil || s.Target == nil {
		s.Distance = nil
		return
	}
	distance := Distance(s.Current.Point, s.Target.Point)
	s.Distance = &distance
}
