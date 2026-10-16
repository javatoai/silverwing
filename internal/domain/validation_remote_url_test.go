package domain

import "testing"

func TestRemoteGitURLSCPPaths(t *testing.T) {
	for _, value := range []string{"git@github.com:example/silverwing.git", "git@gitlab.example:skills/s.git", "git@gitlab.example:team/repo.git"} {
		if err := ValidateRemoteGitURL(value); err != nil {
			t.Errorf("valid SCP remote %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"git@github.com:team/a b.git", "git@github.com:team/a\tb.git", `git@github.com:team\repo.git`, "git@github.com:"} {
		if err := ValidateRemoteGitURL(value); err == nil {
			t.Errorf("invalid SCP remote %q accepted", value)
		}
	}
}
