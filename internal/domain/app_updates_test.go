package domain

import "testing"

func TestCompareReleaseVersionsNumericAndStable(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        int
		valid       bool
	}{
		{"0.0.10", "0.0.9", 1, true}, {"1.0.0", "0.99.999", 1, true},
		{"0.0.9", "0.0.9", 0, true}, {"0.0.8", "0.0.9", -1, true},
		{"0.1.0-beta", "0.0.9", 0, false}, {"nightly", "0.0.9", 0, false},
		{"v0.0.10", "0.0.9", 0, false}, {"0.00.10", "0.0.9", 0, false},
		{"1.2", "0.0.9", 0, false}, {"1.2.18446744073709551616", "0.0.9", 0, false},
	} {
		got, valid := CompareReleaseVersions(test.left, test.right)
		if got != test.want || valid != test.valid {
			t.Fatalf("compare(%q,%q) = %d,%t", test.left, test.right, got, valid)
		}
	}
}
