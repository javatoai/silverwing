package requirementprovider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

type preparationReviewRunner struct{ output string }

func (r preparationReviewRunner) Run(context.Context, process.Spec) (process.Result, error) {
	return process.Result{Stdout: r.output}, nil
}

func TestEnsureMaterialsMatchesNumericRequirementIDExactly(t *testing.T) {
	for _, fixture := range []struct{ id, value string }{
		{"1000000008", "1000000008"},
		{"9007199254740993", "9007199254740993"},
		{"1000000008", `{"long_value":1000000008}`},
		{"1000000008", `"1000000008"`},
	} {
		t.Run(fixture.value, func(t *testing.T) {
			root := t.TempDir()
			subdir := "资料"
			config := domain.AppConfig{RequirementMaterialsRoot: &root, RequirementMaterialsSubdirectory: &subdir,
				MeegleProjects: []domain.MeegleProjectConfig{{ProjectKey: "project-1", SimpleName: "obt"}}}
			service := documents.NewPreparationService(storage.Paths{}, NewMeegle(preparationReviewRunner{output: `{"work_item_attribute":{"work_item_id":` + fixture.value + `}}`}))
			result, err := service.Ensure(context.Background(), config, "https://project.feishu.cn/obt/technical/detail/"+fixture.id, "fixture")
			if err != nil || result.Status != domain.MaterialsReady || result.WriteRoot == nil {
				t.Fatalf("numeric requirement was not matched: result=%#v err=%v", result, err)
			}
			if *result.WriteRoot != filepath.Join(root, fmt.Sprint(time.Now().Year()), fixture.id+"-fixture", subdir) {
				t.Fatalf("wrong requirement directory: %q", *result.WriteRoot)
			}
			if info, err := os.Stat(*result.WriteRoot); err != nil || !info.IsDir() {
				t.Fatalf("materials directory missing: %v", err)
			}
		})
	}
}
