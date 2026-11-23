package domain

import "testing"

func TestApplyAINamingPreservesGeneratedNames(t *testing.T) {
	const link = "https://project.feishu.cn/obt/userstory/detail/123456"
	const prefix = "feature/{num}_{ai}"
	for _, suggestion := range []RequirementAiNamingSuggestion{
		{FolderName: "Improve cache consistency", BranchSuffix: "Improve_cache_for_parallel_naming_requests"},
		{FolderName: "需求命名结果缓存优化", BranchSuffix: "命名缓存-v2"},
	} {
		t.Run(suggestion.FolderName, func(t *testing.T) {
			state := RequirementDraftState{RequirementLink: link}
			updated, err := state.ApplyAINaming(link, suggestion, prefix)
			if err != nil {
				t.Fatal(err)
			}
			if updated.TaskName != suggestion.FolderName || updated.Branch != "feature/123456_"+suggestion.BranchSuffix {
				t.Fatalf("got task name %q and branch %q", updated.TaskName, updated.Branch)
			}
		})
	}
}

func TestValidateRequirementAiNamingSuggestionRequiresUsableFields(t *testing.T) {
	for _, suggestion := range []RequirementAiNamingSuggestion{
		{BranchSuffix: "cache"},
		{FolderName: "../cache", BranchSuffix: "cache"},
		{FolderName: "NUL", BranchSuffix: "cache"},
		{FolderName: "Cache", BranchSuffix: " \t"},
	} {
		if _, err := ValidateRequirementAiNamingSuggestion(suggestion); err == nil {
			t.Errorf("accepted invalid result %+v", suggestion)
		}
	}
}
