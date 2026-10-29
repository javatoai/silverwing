package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
)

func TestStandaloneTaskLocalMaterialsAndRequirementLink(t *testing.T) {
	s, config, root := optionalServiceFixture(t)
	validator := &optionalServiceBranchValidator{}
	s.BranchValidator = validator
	reads := 0
	s.RequirementMaterials = func(context.Context, domain.AppConfig, string, string) (domain.RequirementMaterialsDirectory, *string, error) {
		reads++
		return domain.RequirementMaterialsDirectory{}, nil, errors.New("standalone task must not contact Meegle")
	}
	request := CreateTaskRequest{FolderName: "临时订单分析", TaskNotes: "保留手工笔记"}
	if _, err := s.InspectCreateBranchReuse(context.Background(), config, request); err != nil {
		t.Fatal(err)
	}
	manifest, err := s.CreateTask(context.Background(), config, request)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, request.FolderName)
	scope := documents.LocalTaskMaterials(path)
	if manifest.RequirementLink != "" || manifest.RequirementID != nil || manifest.FeatureBranch != "" || len(manifest.Services) != 0 || manifest.RequirementMaterials.Status != domain.MaterialsReady || stringValue(manifest.RequirementMaterials.WriteRoot) != scope.Owned {
		t.Fatalf("standalone manifest: %+v", manifest)
	}
	for _, dir := range []string{scope.Owned, scope.Temporary} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("missing materials directory %s: %v", dir, err)
		}
	}
	file := filepath.Join(scope.Owned, "分析.md")
	if err := os.WriteFile(file, []byte("用户的分析内容"), 0600); err != nil {
		t.Fatal(err)
	}
	agents := filepath.Join(path, AgentRouterFileName)
	before, _ := os.ReadFile(agents)
	if !strings.Contains(string(before), scope.Owned) || !strings.Contains(string(before), scope.Temporary) || !strings.Contains(string(before), "不参与需求资料 Git 同步") {
		t.Fatalf("missing local directory instructions: %s", before)
	}
	if err := os.WriteFile(agents, append(before, []byte("\n用户手写说明\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	link := "https://project.feishu.cn/obt/userstory/detail/7140000001"
	linked, err := s.LinkRequirement(context.Background(), config, path, link)
	if err != nil || linked.RequirementLink != link || stringValue(linked.RequirementID) != "7140000001" || stringValue(linked.RequirementMaterials.WriteRoot) != scope.Owned {
		t.Fatalf("association changed local materials: %+v %v", linked, err)
	}
	if reads != 0 || validator.calls != 0 {
		t.Fatalf("unexpected external work: material reads=%d git checks=%d", reads, validator.calls)
	}
	after, _ := os.ReadFile(agents)
	if !strings.HasSuffix(string(after), "\n用户手写说明\n") {
		t.Fatal("association overwrote user AGENTS notes")
	}
	contextFile, _ := os.ReadFile(filepath.Join(path, AgentWorkspaceDir, AgentDirectoryName, AgentContextFileName))
	if !strings.Contains(string(contextFile), link) || !strings.Contains(string(contextFile), scope.Owned) {
		t.Fatal("association did not update task instructions")
	}
	if _, err := s.LinkRequirement(context.Background(), config, path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkRequirement(context.Background(), config, path, "https://project.feishu.cn/obt/userstory/detail/9999"); err == nil {
		t.Fatal("association silently replaced an existing requirement")
	}
	for _, step := range []func() error{
		func() error { _, err := s.RetryRequirementMaterials(context.Background(), config, path); return err },
		func() error { _, err := s.Archive(context.Background(), config, path, true); return err },
		func() error { _, err := s.Restore(context.Background(), config, path); return err },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(file); err != nil || string(data) != "用户的分析内容" {
			t.Fatalf("task materials lost: %q %v", data, err)
		}
	}
	if notes, err := s.Documents.ReadTaskNotes(path); err != nil || notes != request.TaskNotes {
		t.Fatalf("notes lost: %q %v", notes, err)
	}
	beforeCancel, _ := s.Manifests.Load(path)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.RetryRequirementMaterials(cancelled, config, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled material preparation continued: %v", err)
	}
	afterCancel, _ := s.Manifests.Load(path)
	if afterCancel.UpdatedAt != beforeCancel.UpdatedAt {
		t.Fatal("cancelled preparation saved the manifest")
	}
}

func TestStandaloneTaskLinkFailurePreservesManifestAndFiles(t *testing.T) {
	s, config, root := optionalServiceFixture(t)
	manifest, err := s.CreateTask(context.Background(), config, CreateTaskRequest{FolderName: "analysis"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "analysis")
	contextPath := filepath.Join(path, AgentWorkspaceDir, AgentDirectoryName, AgentContextFileName)
	if err := os.Remove(contextPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(contextPath, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkRequirement(context.Background(), config, path, "https://project.feishu.cn/obt/userstory/detail/123"); err == nil {
		t.Fatal("document failure was hidden")
	}
	saved, err := s.Manifests.Load(path)
	if err != nil || saved.RequirementLink != "" || saved.RequirementID != nil || saved.UpdatedAt != manifest.UpdatedAt {
		t.Fatalf("failed association persisted: %+v %v", saved, err)
	}
	if _, err := s.LinkRequirement(context.Background(), config, path, "not-a-link"); err == nil {
		t.Fatal("invalid requirement accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.LinkRequirement(ctx, config, path, "https://project.feishu.cn/obt/userstory/detail/123"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled association wrote: %v", err)
	}
}
