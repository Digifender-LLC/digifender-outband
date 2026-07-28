package telemetry

import "testing"

func TestParseSensorValue(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"45.000", 45, true},
		{"12.5 degrees", 12.5, true},
		{"na", 0, false},
		{"", 0, false},
		{"-3.2", -3.2, true},
	}
	for _, tc := range cases {
		got, ok := parseSensorValue(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Fatalf("%q: got (%v,%v) want (%v,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestSensorKind(t *testing.T) {
	if sensorKind("Temperature") != "temp" {
		t.Fatal("temp")
	}
	if sensorKind("Fan") != "fan" {
		t.Fatal("fan")
	}
}
