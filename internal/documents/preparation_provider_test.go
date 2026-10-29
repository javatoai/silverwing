package documents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

type preparationProvider struct {
	resolveCalls, titleCalls int
	identity                 RequirementIdentity
	title                    string
	err                      error
	cancel                   context.CancelFunc
}

func (p *preparationProvider) Resolve(_ context.Context, _ domain.AppConfig, ref RequirementReference) (RequirementIdentity, error) {
	p.resolveCalls++
	if p.cancel != nil {
		p.cancel()
	}
	return p.identity, p.err
}

func (p *preparationProvider) Title(context.Context, domain.AppConfig, RequirementReference) (string, error) {
	p.titleCalls++
	return p.title, p.err
}

func TestMaterialsPreparationUsesInjectedFactsAndOfflineReuse(t *testing.T) {
	root, subdirectory := t.TempDir(), "资料"
	config := domain.AppConfig{RequirementMaterialsRoot: &root, RequirementMaterialsSubdirectory: &subdirectory}
	provider := &preparationProvider{identity: RequirementIdentity{Space: "obt", Kind: "bug", WorkItemID: "123"}, title: "来源标题"}
	service := NewPreparationService(storage.Paths{}, provider)
	ctx := WithMaterialsCreationTime(context.Background(), time.Date(2024, 12, 31, 12, 0, 0, 0, time.Local))
	link := "https://project.feishu.cn/obt/bug/detail/123"
	preview, err := service.Preview(ctx, config, link, "目录")
	if err != nil || preview.Status != domain.MaterialsReady || provider.resolveCalls != 1 {
		t.Fatalf("injected preview: %+v %v calls=%d", preview, err, provider.resolveCalls)
	}
	if _, err := os.Stat(filepath.Join(root, "2024")); !os.IsNotExist(err) {
		t.Fatal("preview created a directory", err)
	}
	plan, err := service.Plan(ctx, config, link, "", "目录")
	if err != nil || plan.RequirementTitle != provider.title || provider.titleCalls != 1 {
		t.Fatalf("injected title: %+v %v", plan, err)
	}
	ready, err := service.Ensure(ctx, config, link, "目录")
	if err != nil || ready.Status != domain.MaterialsReady || ready.WriteRoot == nil || !strings.Contains(*ready.WriteRoot, filepath.Join("2024", "123-目录")) {
		t.Fatalf("injected ensure: %+v %v", ready, err)
	}
	provider.err = errors.New("offline")
	before := provider.resolveCalls
	reused, err := service.Ensure(ctx, config, link, "另一个名称")
	if err != nil || reused.Status != domain.MaterialsReady || *reused.WriteRoot != *ready.WriteRoot || provider.resolveCalls != before {
		t.Fatalf("offline reuse: %+v %v calls=%d", reused, err, provider.resolveCalls)
	}
}

func TestMaterialsPreparationRejectsProviderIdentityAndKeepsSoftFailure(t *testing.T) {
	root, subdirectory := t.TempDir(), "资料"
	config := domain.AppConfig{RequirementMaterialsRoot: &root, RequirementMaterialsSubdirectory: &subdirectory}
	provider := &preparationProvider{identity: RequirementIdentity{Space: "other", Kind: "bug", WorkItemID: "123"}}
	service := NewPreparationService(storage.Paths{}, provider)
	link := "https://project.feishu.cn/obt/bug/detail/123"
	result, err := service.Ensure(context.Background(), config, link, "目录")
	if err != nil || result.Status != domain.MaterialsFailed || result.FailureReason == nil || !strings.Contains(*result.FailureReason, "身份") {
		t.Fatalf("mismatched source identity: %+v %v", result, err)
	}
	provider.err = errors.New("source unavailable")
	result, err = service.Ensure(context.Background(), config, link, "目录")
	if err != nil || result.Status != domain.MaterialsFailed || result.FailureReason == nil || *result.FailureReason != provider.err.Error() {
		t.Fatalf("source failure contract: %+v %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	provider.cancel, provider.err = cancel, context.Canceled
	_, err = service.Ensure(ctx, config, link, "目录")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation became soft failure: %v", err)
	}
}
