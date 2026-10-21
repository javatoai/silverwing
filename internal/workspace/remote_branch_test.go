package workspace

import "testing"

func TestGitRemoteBrowserURLPreservesEscapedRepositoryPath(t *testing.T) {
	for _, test := range []struct{ remote, want string }{
		{"https://example.com/team/my%20repo.git", "https://example.com/team/my%20repo"},
		{"ssh://git@example.com/team/%E4%BB%93%E5%BA%93.git", "https://example.com/team/%E4%BB%93%E5%BA%93"},
		{"https://example.com/team/a%2Fb%2egit", "https://example.com/team/a%2Fb"},
		{"git@example.com:team/my%20repo.git", "https://example.com/team/my%2520repo"},
		{"https://user:secret@example.com/team/repo.git?token=secret#secret", "https://example.com/team/repo"},
	} {
		t.Run(test.remote, func(t *testing.T) {
			if got := GitRemoteBrowserURL(test.remote); got != test.want {
				t.Fatalf("browser URL = %q, want %q", got, test.want)
			}
		})
	}
}
