package integrations

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func commentIdentityCalls(t *testing.T, f *commentFixture) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.root, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range bytes.Split(raw, []byte("\n")) {
		var args []string
		if json.Unmarshal(line, &args) == nil && len(args) > 1 && (args[0] == "auth" && args[1] == "status" || args[0] == "config" && args[1] == "profile" || args[0] == "user" && args[1] == "me") {
			count++
		}
	}
	return count
}

func TestCommentPublishReusesConfirmedSession(t *testing.T) {
	cli := buildCommentCLI(t)
	for _, cold := range []bool{false, true} {
		t.Run(map[bool]string{false: "open draft session", true: "new service"}[cold], func(t *testing.T) {
			f := newCommentFixture(t, cli)
			f.edit(t, "save", map[string]any{"content": "普通文字评论"})
			if cold {
				f.s = NewService(f.s.paths, f.s.config, nil)
			}
			before := commentIdentityCalls(t, f)
			result, _ := f.publish(t, nil)
			if result.Status != "published" {
				t.Fatal(result.Status, result.Message)
			}
			want := 0
			if cold {
				want = 3 // One profile/auth/user probe, shared by the whole publish.
			}
			if calls := commentIdentityCalls(t, f) - before; calls != want {
				t.Fatalf("publish repeated login identity queries: got %d CLI calls, want %d", calls, want)
			}
			if creates, uploads := f.counts(t); creates != 1 || uploads != 0 {
				t.Fatalf("session reuse changed publication count: %d creates %d uploads", creates, uploads)
			}
		})
	}
}
