package delivery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func TestTagAnnouncementUsesCompleteRequirementTitle(t *testing.T) {
	tag := "1.2.3.beta-4"
	operations := []domain.TagOperation{{ServiceName: "api", Tag: &tag, State: domain.TagOperationSuccess}}
	title := "支持多个服务批量测试Tag：这是一条完整需求标题"
	want := "需求：" + title + "\n测试Tag已构建完毕，辛苦发版：\n\napi · " + tag
	if got := FormatTagAnnouncement(title, operations, true); got != want || strings.Contains(got, `\n`) || strings.Contains(got, "需求链接") {
		t.Fatalf("title/line fidelity mismatch: %q", got)
	}
	message := "fixture failure"
	operations = append(operations, domain.TagOperation{ServiceName: "job", State: domain.TagOperationFailed, Message: &message})
	got := FormatTagAnnouncement(title, operations, true)
	if !strings.HasPrefix(got, "需求："+title+"\n\napi · "+tag) || !strings.Contains(got, "fixture failure") || !strings.HasSuffix(got, "测试Tag未全部构建成功，请处理失败项后重试") {
		t.Fatalf("failure notification lost its existing meaning: %q", got)
	}
}

func TestTagHistoryDeleteRequiresCompleteBatch(t *testing.T) {
	root := t.TempDir()
	taskRoot := filepath.Join(root, "tasks")
	directory := filepath.Join(taskRoot, "task-a")
	other := filepath.Join(taskRoot, "task-b")
	for _, path := range []string{directory, other} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	s := &Service{locks: workspace.NewOperationLocks(filepath.Join(root, "locks"))}
	batch, tag := "batch", "1.2.3"
	aID, bID, singleID, foreignID := "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000003", "00000000-0000-0000-0000-000000000004"
	newRecord := func(id string, batchID *string) domain.TagOperation {
		return domain.TagOperation{OperationID: id, FolderName: "fixture", ServiceName: id, RepositoryID: "repository", SourceBranch: "feature/fixture", Remote: "origin", TagMode: domain.TagBuildMergeToTargetBranch, State: domain.TagOperationSuccess, CreatedAt: "2026-10-15T10:00:00+08:00", UpdatedAt: "2026-10-15T10:00:00+08:00", Tag: &tag, BatchID: batchID, ConflictFiles: []string{}, GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown}}
	}
	for _, record := range []domain.TagOperation{newRecord(aID, &batch), newRecord(bID, &batch), newRecord(singleID, nil)} {
		if err := s.store.save(context.Background(), directory, record); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.store.save(context.Background(), other, newRecord(foreignID, &batch)); err != nil {
		t.Fatal(err)
	}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &taskRoot
	tasks := []domain.TaskManifest{{TaskDirectoryName: "task-a"}}
	for _, ids := range [][]string{{aID}, {singleID, aID}} {
		deleted, err := s.DeleteHistory(context.Background(), config, tasks, ids)
		current, readErr := s.store.list(directory)
		if err == nil || deleted != 0 || readErr != nil || len(current) != 3 {
			t.Fatalf("partial batch was not rejected before any deletion: %v %d %v", err, deleted, readErr)
		}
	}
	deleted, err := s.DeleteHistory(context.Background(), config, tasks, []string{aID, bID})
	current, readErr := s.store.list(directory)
	foreign, foreignErr := s.store.list(other)
	if err != nil || deleted != 2 || readErr != nil || len(current) != 1 || current[0].OperationID != singleID || foreignErr != nil || len(foreign) != 1 {
		t.Fatalf("complete batch/task isolation changed: deleted=%d current=%+v err=%v/%v/%v", deleted, current, err, readErr, foreignErr)
	}
}
