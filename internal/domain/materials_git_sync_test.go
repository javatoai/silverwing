package domain

import "testing"

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
