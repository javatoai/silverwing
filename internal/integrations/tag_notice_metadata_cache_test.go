package integrations

import (
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestNoticeMetadataCacheRetainsCompleteRosterEvidence(t *testing.T) {
	s := &Service{paths: storage.NewPaths(t.TempDir())}
	for _, mode := range []string{"confirmed-empty", "confirmed-full", "legacy-display-only", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			title := "Requirement"
			data := RequirementMetadataReadResult{ID: "1", ProjectKey: "p", Type: "userstory", Title: &title, Participants: domain.RequirementParticipants{QCOwners: []domain.RequirementPerson{{Name: "Display only"}}}}
			switch mode {
			case "confirmed-empty":
				data.ReleaseTesters = []domain.RequirementPerson{}
				data.ReleaseTestersConfirmed = true
			case "confirmed-full":
				data.ReleaseTesters = []domain.RequirementPerson{{Name: "A"}, {Name: "B"}}
				data.ReleaseTestersConfirmed = true
			case "incomplete":
				data.ReleaseTestersConfirmed = true
			}
			if _, err := s.writeRequirementMetadataCache(mode, data, 1); err != nil {
				t.Fatal(err)
			}
			loaded, _, err := s.readRequirementMetadataCache(mode)
			if err != nil {
				t.Fatal(err)
			}
			confirmed := mode == "confirmed-empty" || mode == "confirmed-full"
			if loaded.Data.ReleaseTestersConfirmed != confirmed || (loaded.Data.ReleaseTesters != nil) != confirmed {
				t.Fatal("display projection inferred a complete roster", loaded.Data)
			}
			if mode == "confirmed-full" && len(loaded.Data.ReleaseTesters) != 2 {
				t.Fatal(loaded.Data.ReleaseTesters)
			}
		})
	}
}
