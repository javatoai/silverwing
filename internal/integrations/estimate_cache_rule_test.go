package integrations

import (
	"encoding/json"
	"os"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestPersonalEstimateOldListRuleMissesWithoutDeletingCache(t *testing.T) {
	s := &Service{paths: storage.NewPaths(t.TempDir())}
	value := participatedResult{Items: []domain.ParticipatedWorkItem{}, FetchedAt: requirementUnixMillis()}
	if err := s.writeRequirementListCache("rule-key", value); err != nil {
		t.Fatal(err)
	}
	cached, path, err := s.readRequirementListCache("rule-key")
	if err != nil || cached == nil {
		t.Fatalf("current rule did not cache: %v", err)
	}
	data, _ := os.ReadFile(path)
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	delete(raw, "estimateRuleVersion")
	data, _ = json.Marshal(raw)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if cached, _, err := s.readRequirementListCache("rule-key"); cached != nil || err != nil {
		t.Fatalf("old rule was reused: %+v %v", cached, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("derived-rule miss deleted old data instead of leaving it for a successful replacement")
	}
}

func TestPersonalEstimateMetadataDropsOnlyOldDerivedValues(t *testing.T) {
	s := &Service{paths: storage.NewPaths(t.TempDir())}
	item := domain.ParticipatedWorkItem{ID: "123", ProjectKey: "p", Type: "userstory", URL: "https://project.feishu.cn/space/userstory/detail/123"}
	sprint, title := "p:900", "retained title"
	value := RequirementMetadataReadResult{ID: item.ID, ProjectKey: item.ProjectKey, Type: item.Type, Title: &title,
		WorkItem: &item, SprintEstimates: map[string]RequirementSprintEstimate{
			"p:900": {Days: refreshString("1"), FetchedAt: 300, RuleVersion: 1},
			"p:901": {Days: refreshString("2"), FetchedAt: 200, RuleVersion: personalEstimateRuleVersion},
		}}
	key := requirementCacheKey("metadata", "account", item)
	if _, err := s.writeRequirementMetadataCache(key, value, 300); err != nil {
		t.Fatal(err)
	}
	saved, _, err := s.readRequirementMetadataCache(key)
	if err != nil || saved == nil || saved.Data.Title == nil || *saved.Data.Title != title || saved.Data.WorkItem == nil {
		t.Fatalf("rule upgrade lost unrelated metadata: %+v %v", saved, err)
	}
	if _, old := saved.Data.SprintEstimates[sprint]; old || valuePointer(saved.Data.SprintEstimates["p:901"].Days) != "2" {
		t.Fatalf("derived values not scoped by rule: %+v", saved.Data.SprintEstimates)
	}
	item.MySprintEstimateDays = refreshString("2")
	result := s.overlayRequirementList("account", participatedResult{Items: []domain.ParticipatedWorkItem{item}, SelectedSprintKey: &sprint, FetchedAt: 100})
	if valuePointer(result.Items[0].MySprintEstimateDays) != "2" {
		t.Fatal("new rule was overwritten by a newer old-rule estimate")
	}
}
