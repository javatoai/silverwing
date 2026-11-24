package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/application"
)

func TestCLIDragonRejectsUnsupportedOrIncompleteOptions(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"dragon", "submit"}, "请指定 --task"},
		{[]string{"dragon", "submit", "--task", "fixture", "--service", "api"}, "service"},
		{[]string{"dragon", "submit", "--task", "fixture", "--env", "prod"}, "env"},
		{[]string{"dragon", "submit", "--task", "fixture", "--preflight", ""}, "请指定有效的预检编号"},
		{[]string{"dragon", "detail"}, "请指定 --approval-id"},
		{[]string{"dragon", "recheck", "--task", "fixture", "--approval-id", "-1"}, "青龙审批 ID 必须是正整数"},
		{[]string{"dragon", "approvals", "--size", "101"}, "--size 必须在 1–100 之间"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var stderr bytes.Buffer
			args := append([]string{"--conf-dir", filepath.Join(t.TempDir(), "no-profile")}, tc.args...)
			if code := run(context.Background(), args, io.Discard, &stderr); code != exitUsage || !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("exit=%d stderr=%q expected %q", code, stderr.String(), tc.want)
			}
		})
	}
}

func TestCLIDragonJSONContainsCandidateApprovals(t *testing.T) {
	result := application.CLIDragonQAResult{
		TaskID: "fixture", Phase: application.DragonQAUnknown,
		Candidates: []application.CLIDragonQAApproval{{ID: "123", URL: "https://example.invalid/123"}},
	}
	var out bytes.Buffer
	(options{asJSON: true}).outputResult(&out, io.Discard, result, exitError, nil)
	var response struct {
		OK   bool
		Data application.CLIDragonQAResult
	}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil || response.OK ||
		len(response.Data.Candidates) != 1 || response.Data.Candidates[0].ID != "123" {
		t.Fatalf("invalid CLI JSON %q, %v", out.String(), err)
	}
}
