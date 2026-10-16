package desktop

import (
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
)

func TestWorkspaceCommitHistoryMessageExpandsLongText(t *testing.T) {
	long := strings.Repeat("提交说明", 50)
	collapsed, expandable := workspaceCommitHistoryMessage(long, false)
	if !expandable || len([]rune(collapsed)) > 150 || !strings.HasSuffix(collapsed, "…") {
		t.Fatalf("collapsed message = (%d runes, expandable=%v, text=%q)", len([]rune(collapsed)), expandable, collapsed)
	}
	expanded, stillExpandable := workspaceCommitHistoryMessage(long, true)
	if expanded != long || !stillExpandable {
		t.Fatal("expanded message did not preserve the full commit text")
	}
	multiline, expandable := workspaceCommitHistoryMessage("第一行\n第二行", false)
	if multiline != "第一行" || !expandable {
		t.Fatalf("multiline message = (%q, expandable=%v)", multiline, expandable)
	}
}

func TestWorkspaceCommitHistoryUsesLocalCommitTime(t *testing.T) {
	previous := time.Local
	time.Local = time.FixedZone("preview-local", 8*60*60)
	defer func() { time.Local = previous }()

	committedAt := time.Date(2026, 10, 15, 2, 3, 4, 0, time.UTC)
	if got, want := workspaceCommitHistoryLocalTime(committedAt), "2026-10-15 10:03:04"; got != want {
		t.Fatalf("local commit time = %q, want %q", got, want)
	}
}

func TestWorkspaceHistoryTracksResolvedCurrentBranchConfig(t *testing.T) {
	baseline := application.Action{Kind: "workspace.history", Payload: map[string]any{"baselineRef": "origin/main", "allHistory": false}}
	if !workspaceHistoryTracksCurrentBranchConfig(baseline) {
		t.Fatal("baseline history read must track current module/service configuration")
	}
	allHistory := application.Action{Kind: "workspace.history", Payload: map[string]any{"allHistory": true}}
	if workspaceHistoryTracksCurrentBranchConfig(allHistory) {
		t.Fatal("all-history read without a baseline must not track branch configuration")
	}
	filePreview := application.Action{Kind: "workspace.file-preview"}
	if workspaceHistoryTracksCurrentBranchConfig(filePreview) {
		t.Fatal("file preview should not track branch configuration")
	}
}
