package coords

import "testing"

func TestParseCoordinate(t *testing.T) {
	tests := []struct {
		name string
		text string
		x    float64
		y    float64
		ok   bool
	}{
		{name: "standard", text: "x98.43 y110.44", x: 98.43, y: 110.44, ok: true},
		{name: "comma", text: "X 98.43, Y 110.44", x: 98.43, y: 110.44, ok: true},
		{name: "map y first", text: "y110.69\nx98.13", x: 98.13, y: 110.69, ok: true},
		{name: "integers", text: "x=98 y:110", x: 98, y: 110, ok: true},
		{name: "negative", text: "x -12.5, y +7.25", x: -12.5, y: 7.25, ok: true},
		{name: "decimal comma", text: "x98,43 y110,44", x: 98.43, y: 110.44, ok: true},
		{name: "ocr letters", text: "х98.O3 у11O.44", x: 98.03, y: 110.44, ok: true},
		{name: "embedded chat", text: "队伍 x98.43 y110.44", x: 98.43, y: 110.44, ok: true},
		{name: "missing y", text: "x98.43", ok: false},
		{name: "unrelated numbers", text: "100M 110", ok: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			point, ok := ParseCoordinate(tc.text)
			if ok != tc.ok {
				t.Fatalf("ok=%v, want %v; point=%+v", ok, tc.ok, point)
			}
			if ok && (point.X != tc.x || point.Y != tc.y) {
				t.Fatalf("point=%+v, want x=%v y=%v", point, tc.x, tc.y)
			}
		})
	}
}
