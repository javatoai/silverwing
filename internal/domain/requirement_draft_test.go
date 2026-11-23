package domain

import "testing"

func TestFeishuWorkItemLinkAcceptsTypeKey(t *testing.T) {
	const directLink = "https://project.feishu.cn/obt/6785BC520A0621258BCF0583/detail/7143084336?from=share"
	parsed := ParseFeishuWorkItemLink(directLink)
	if parsed == nil || parsed.ProjectKey == nil || parsed.Space != "obt" || parsed.Kind != "6785bc520a0621258bcf0583" || parsed.WorkItemID != "7143084336" {
		t.Fatalf("incorrect direct-link identity: %+v", parsed)
	}
	if draft := (RequirementDraftState{}).ChangeRequirement(directLink, "feature/{num}", nil); !draft.MetadataLoading || draft.Branch != "feature/7143084336" {
		t.Fatalf("direct link did not start loading the requirement: %+v", draft)
	}
	for _, link := range []string{
		"https://project.feishu.cn/obt/6785bc520a0621258bcf058/detail/7143084336",
		"https://project.feishu.cn/obt/g785bc520a0621258bcf0583/detail/7143084336",
		"https://invalid.example/obt/6785bc520a0621258bcf0583/detail/7143084336",
	} {
		if parsed := ParseFeishuWorkItemLink(link); parsed != nil {
			t.Errorf("accepted invalid link %q: %+v", link, parsed)
		}
	}
}

func TestRequirementDraftChangesClearOnlyAutomaticNaming(t *testing.T) {
	const firstLink = "https://project.feishu.cn/obt/userstory/detail/1000000004"
	const secondLink = "https://project.feishu.cn/obt/userstory/detail/1000000005"
	const prefix = "feature/{num}_{ai}"
	first, err := (RequirementDraftState{RequirementLink: firstLink}).ApplyAINaming(firstLink, RequirementAiNamingSuggestion{
		FolderName: "旧需求", BranchSuffix: "previous",
	}, prefix)
	if err != nil {
		t.Fatal(err)
	}
	title := "新需求的完整标题"
	changed := first.ChangeRequirement(secondLink, prefix, &title)
	if changed.TaskName != "" || changed.Branch != "" || changed.AIBranchSuffix != nil || changed.RequirementTitle == nil || *changed.RequirementTitle != title || changed.MetadataLoading {
		t.Fatalf("previous automatic naming survived the requirement change: %+v", changed)
	}
	unchanged, err := changed.ApplyAINaming(firstLink, RequirementAiNamingSuggestion{FolderName: "旧需求", BranchSuffix: "previous"}, prefix)
	if err != nil || unchanged != changed {
		t.Fatalf("a suggestion for the retired requirement changed the draft: %+v, %v", unchanged, err)
	}
	updated, err := changed.ApplyAINaming(secondLink, RequirementAiNamingSuggestion{FolderName: "新需求", BranchSuffix: "new_item"}, prefix)
	if err != nil || updated.TaskName != "新需求" || updated.Branch != "feature/1000000005_new_item" {
		t.Fatalf("the current naming suggestion did not resolve the branch: %+v, %v", updated, err)
	}
	manual := first.EditName("手工名称").EditBranch("feature/manual").ChangeRequirement(secondLink, prefix, &title)
	manual, err = manual.ApplyAINaming(secondLink, RequirementAiNamingSuggestion{FolderName: "新需求", BranchSuffix: "new_item"}, prefix)
	if err != nil || manual.TaskName != "手工名称" || manual.Branch != "feature/manual" || manual.AIBranchSuffix != nil {
		t.Fatalf("manual naming was replaced: %+v, %v", manual, err)
	}
}
