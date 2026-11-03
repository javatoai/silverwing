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

func TestAppUpdateReceiptRejectsForeignOrTamperedLinks(t *testing.T) {
	result := UpdateCheckResult{Repository: "owner/repo", TagPrefix: "v", Release: &UpdateRelease{
		Version: "0.0.10", Tag: "v0.0.10", URL: "https://github.com/owner/repo/releases/tag/v0.0.10",
		Package: &UpdateAsset{Name: "app.exe", URL: "https://github.com/owner/repo/releases/download/v0.0.10/app.exe", Size: 10},
	}}
	if !result.Available("0.0.9", "owner/repo", "v") || result.Available("0.0.10", "owner/repo", "v") {
		t.Fatal("a new build must clear the old version's notification")
	}
	if result.Available("0.0.9", "other/repo", "v") || result.Available("0.0.9", "owner/repo", "qa-v") {
		t.Fatal("release channel isolation failed")
	}
	result.Release.Package.URL = "https://example.com/app.exe"
	if result.Valid("owner/repo", "v") {
		t.Fatal("untrusted persisted download was accepted")
	}
	result.Release.Package = nil
	result.Release.URL = "file:///C:/example.exe"
	if result.Valid("owner/repo", "v") {
		t.Fatal("untrusted persisted release URL was accepted")
	}
}
