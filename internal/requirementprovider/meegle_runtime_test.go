package requirementprovider

import (
	"context"
	"strings"
	"testing"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

type materialsRuntimeRunner struct {
	specs   []process.Spec
	failure string
}

func (r *materialsRuntimeRunner) Run(_ context.Context, spec process.Spec) (process.Result, error) {
	if spec.Executable == "/bin/zsh" {
		return process.Result{Stdout: "/usr/local/bin:/usr/bin:/bin\n"}, nil
	}
	r.specs = append(r.specs, spec)
	if r.failure != "" {
		return process.Result{ExitCode: 127, Stderr: r.failure}, nil
	}
	return process.Result{Stdout: `{"work_item_id":"123456","name":"最新需求标题"}`}, nil
}

func TestMaterialsNewDirectoryAndTitleShareConfiguredRuntime(t *testing.T) {
	root, subdir, cli, proxy := t.TempDir(), "资料", "/fixture path/meegle", "http://proxy.invalid:8080"
	c := domain.DefaultAppConfig()
	c.MeegleExecutablePath = &cli
	c.RequirementMaterialsRoot = &root
	c.RequirementMaterialsSubdirectory = &subdir
	c.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "project", SimpleName: "obt"}}
	c.CommandProxyURL = &proxy
	c.CommandProxyTargets = []domain.CommandProxyTarget{domain.ProxyTargetMeegle}
	runner := &materialsRuntimeRunner{}
	provider := NewMeegle(runner)
	service := documents.NewPreparationService(storage.NewPaths(t.TempDir()), provider)
	result, err := service.Ensure(context.Background(), c, "https://project.feishu.cn/obt/bug/detail/123456", "新目录")
	if err != nil || result.Status != domain.MaterialsReady || result.WriteRoot == nil {
		t.Fatalf("fresh materials failed: %+v %v", result, err)
	}
	identity := documents.RequirementIdentity{Space: "obt", Kind: "bug", WorkItemID: "123456"}
	if title, err := provider.Title(context.Background(), c, documents.RequirementReference{WorkItemID: identity.WorkItemID, Identity: &identity}); err != nil || title != "最新需求标题" {
		t.Fatal(title, err)
	}
	if len(runner.specs) != 2 {
		t.Fatalf("expected both remote boundaries, got %d", len(runner.specs))
	}
	for _, spec := range runner.specs {
		if spec.Executable != cli || spec.Env["HTTP_PROXY"] != proxy || len(spec.RemoveEnv) == 0 {
			t.Fatalf("different runtime: %+v", spec)
		}
	}
	_, err = service.Ensure(context.Background(), c, "https://project.feishu.cn/obt/bug/detail/123456", "新目录")
	if err != nil || len(runner.specs) != 2 {
		t.Fatal("existing materials queried again")
	}
}

func TestMaterialsRuntimeFailurePreservesCause(t *testing.T) {
	for _, failure := range []string{"env: node: No such file or directory", "node: command not found", "authentication failed"} {
		t.Run(failure, func(t *testing.T) {
			root, subdir, cli := t.TempDir(), "资料", "/fixture/meegle"
			c := domain.DefaultAppConfig()
			c.MeegleExecutablePath = &cli
			c.RequirementMaterialsRoot = &root
			c.RequirementMaterialsSubdirectory = &subdir
			c.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "project", SimpleName: "obt"}}
			r := &materialsRuntimeRunner{failure: failure}
			service := documents.NewPreparationService(storage.NewPaths(t.TempDir()), NewMeegle(r))
			result, err := service.Ensure(context.Background(), c, "https://project.feishu.cn/obt/bug/detail/123456", "失败目录")
			if err != nil || result.Status != domain.MaterialsFailed || result.FailureReason == nil || !strings.Contains(*result.FailureReason, failure) {
				t.Fatalf("lost startup/auth failure: %+v %v", result, err)
			}
		})
	}
	if !missingRequirement("work item 123 does not exist") || !missingRequirement("HTTP 404: work item not found") {
		t.Fatal("explicit missing item not recognized")
	}
}
