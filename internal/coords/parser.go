package coords

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"wardogs-mortar/internal/model"
)

var labelNumber = regexp.MustCompile(`(?i)([xy])\s*[:=]?\s*([-+]?[0-9oOilIl]+(?:[.,]\s*[0-9oOilIl]+)?)`)
var numberSpacing = regexp.MustCompile(`\s*([.,])\s*`)

// ParseCoordinate extracts one complete x/y pair from OCR output. It accepts
// either x/y or y/x ordering because the map HUD in the supplied screenshots
// places y above x.
func ParseCoordinate(raw string) (model.Point, bool) {
	normalized := normalize(raw)
	matches := labelNumber.FindAllStringSubmatch(normalized, -1)
	if len(matches) < 2 {
		return model.Point{}, false
	}

	for i := 0; i < len(matches)-1; i++ {
		firstLabel := strings.ToLower(matches[i][1])
		secondLabel := strings.ToLower(matches[i+1][1])
		if firstLabel == secondLabel {
			continue
		}

		first, okFirst := parseNumber(matches[i][2])
		second, okSecond := parseNumber(matches[i+1][2])
		if !okFirst || !okSecond {
			continue
		}
		if firstLabel == "x" {
			return model.Point{X: first, Y: second}, true
		}
		return model.Point{X: second, Y: first}, true
	}

	return model.Point{}, false
}

func normalize(raw string) string {
	s := strings.ToLower(raw)
	s = strings.NewReplacer(
		"х", "x", // Cyrillic x occasionally returned by OCR
		"у", "y", // Cyrillic y occasionally returned by OCR
		"×", "x",
		"—", "-",
		"−", "-",
	).Replace(s)
	s = numberSpacing.ReplaceAllString(s, "$1")
	return s
}

func parseNumber(value string) (float64, bool) {
	v := strings.NewReplacer(
		"o", "0",
		"i", "1",
		"l", "1",
		",", ".",
	).Replace(strings.TrimSpace(value))
	parsed, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func FormatPoint(p model.Point) string {
	return fmt.Sprintf("x%.2f, y%.2f", p.X, p.Y)
}
