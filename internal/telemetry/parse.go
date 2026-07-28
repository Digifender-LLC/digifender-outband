package telemetry

import (
	"strconv"
	"strings"
	"unicode"
)

func sensorKind(typ string) string {
	t := strings.ToLower(typ)
	switch {
	case strings.Contains(t, "temperature"):
		return "temp"
	case strings.Contains(t, "fan"):
		return "fan"
	case strings.Contains(t, "voltage"):
		return "voltage"
	case strings.Contains(t, "current"):
		return "current"
	case strings.Contains(t, "power"):
		return "power"
	default:
		return "other"
	}
}

// parseSensorValue extracts a leading float from IPMI reading strings.
func parseSensorValue(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "na") || strings.EqualFold(raw, "n/a") {
		return 0, false
	}
	end := 0
	for i, r := range raw {
		if unicode.IsDigit(r) || r == '.' || r == '-' || r == '+' || r == 'e' || r == 'E' {
			end = i + len(string(r))
			continue
		}
		break
	}
	if end == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(raw[:end], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
