package domain

import "testing"

func TestSameMaterialsGitRepositoryURL(t *testing.T) {
	for _, test := range []struct {
		before, after string
		same          bool
	}{
		{"https://git.example.com/team/docs", "https://git.example.com/team/docs.git", true},
		{"https://git.example.com/team/docs.git", "https://git.example.com/team/docs", true},
		{"https://git.example.com/team/docs/", "https://git.example.com/team/docs.git/", true},
		{"git@git.example.com:team/docs", "git@git.example.com:team/docs.git", true},
		{"ssh://git@git.example.com/team/docs", "ssh://git@git.example.com/team/docs.git", true},
		{"http://git.example.com/team/docs", "http://git.example.com/team/docs.git", true},
		{"https://git.example.com/team/docs", "https://other.example.com/team/docs.git", false},
		{"https://git.example.com/team/docs", "https://git.example.com/team/other.git", false},
		{"https://git.example.com/team/docs/", "https://git.example.com/team/docs/.git", false},
		{"https://git.example.com/team/docs", "http://git.example.com/team/docs.git", false},
		{"https://git.example.com/team/docs", "https://git.example.com:8443/team/docs.git", false},
		{"https://git.example.com/team/docs.git", "https://git.example.com/team/docs.git.git", false},
		{"https://git.example.com/team/docs?token=x", "https://git.example.com/team/docs.git?token=x", false},
	} {
		if got := SameMaterialsGitRepositoryURL(test.before, test.after); got != test.same {
			t.Errorf("same repository for %q and %q = %t, want %t", test.before, test.after, got, test.same)
		}
	}
}

func TestMaterialsGitDefaultsAndValidation(t *testing.T) {
	config := DefaultAppConfig()
	defaults := config.MaterialsGitSettings()
	if defaults.Enabled || defaults.Branch != "master" || defaults.IntervalMinutes != 5 {
		t.Fatal(defaults)
	}
	for _, url := range []string{"https://github.com/team/docs.git", "git@gitlab.example.com:team/docs.git", "ssh://git@gitlab.example.com/team/docs"} {
		s := defaults
		s.RepositoryURL, s.Enabled, s.Branch = url, true, "docs/team"
		if err := s.Validate(); err != nil {
			t.Fatal(url, err)
		}
	}
	for _, mutation := range []func(*MaterialsGitSyncSettings){
		func(s *MaterialsGitSyncSettings) { s.IntervalMinutes = 0 }, func(s *MaterialsGitSyncSettings) { s.IntervalMinutes = -1 },
		func(s *MaterialsGitSyncSettings) { s.Branch = "--upload-pack=bad" }, func(s *MaterialsGitSyncSettings) { s.Branch = "a..b" },
		func(s *MaterialsGitSyncSettings) { s.Branch = "" }, func(s *MaterialsGitSyncSettings) { s.Enabled = true },
		func(s *MaterialsGitSyncSettings) { s.RepositoryURL = "https://token@example.com/docs.git" },
		func(s *MaterialsGitSyncSettings) { s.RepositoryURL = "ssh://git:secret@example.com/docs.git" },
	} {
		s := defaults
		mutation(&s)
		if s.Validate() == nil {
			t.Fatalf("accepted invalid settings: %+v", s)
		}
	}
}

func TestLegacyUnboundProjectInstructionsRetainSyncPreference(t *testing.T) {
	config := DefaultAppConfig()
	enabled := true
	config.ProjectInstructionsGitEnabled = &enabled
	if err := config.Validate(); err != nil {
		t.Fatalf("legacy configuration with an unbound project must still load: %v", err)
	}
	if config.ProjectInstructionsGitSettings().Enabled {
		t.Fatal("an unbound project cannot schedule synchronization")
	}
	config.ProjectInstructionsGitURL = "https://git.example.com/team/docs.git"
	if !config.ProjectInstructionsGitSettings().Enabled {
		t.Fatal("binding a remote ignored the explicit legacy preference")
	}
}
