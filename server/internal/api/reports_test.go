package api

import "testing"

// The regression this guards against: genchem had 1 login and 191.87 hours
// of accrued session time over 30 days (a shared/kiosk-style account that
// rarely signs all the way out), producing an "average session" of 11,506
// minutes — a number no one experienced as one sitting. Below
// minLoginsForAvgSession the sample is too small to average meaningfully, so
// the user should be omitted from this report entirely rather than shown
// with a distorted number.
func TestComputeAvgSessionMinutesOmitsLowSampleUsers(t *testing.T) {
	seconds := map[string]float64{
		"genchem": 690732,  // 191.87 hours
		"mabrown": 1205388, // 334.83 hours
		"darias":  21600,   // 6 hours
	}
	logins := map[string]float64{
		"genchem": 1,
		"mabrown": 3,
		"darias":  6,
	}

	got := computeAvgSessionMinutes(seconds, logins, 3)

	if _, ok := got["genchem"]; ok {
		t.Errorf("genchem has only 1 login and should be omitted, got %v", got["genchem"])
	}
	if v, ok := got["mabrown"]; !ok {
		t.Error("mabrown has exactly the minimum login count and should be included")
	} else if want := 1205388.0 / 3 / 60; v != want {
		t.Errorf("mabrown avg = %v, want %v", v, want)
	}
	if v, ok := got["darias"]; !ok {
		t.Error("darias has well above the minimum and should be included")
	} else if want := 21600.0 / 6 / 60; v != want {
		t.Errorf("darias avg = %v, want %v", v, want)
	}
}

// A user with an ongoing session but no fresh login inside the window has a
// zero denominator (absent from the logins map entirely) and must be
// omitted, not reported as infinite.
func TestComputeAvgSessionMinutesOmitsZeroLogins(t *testing.T) {
	seconds := map[string]float64{"nologinuser": 3600}
	logins := map[string]float64{} // no entry at all

	got := computeAvgSessionMinutes(seconds, logins, 3)

	if _, ok := got["nologinuser"]; ok {
		t.Error("user with no logins in the window should be omitted, not reported as infinite")
	}
}
