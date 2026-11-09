package application

import (
	"slices"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/integrations"
)

func TestReleaseNoticeTestersPreserveRosterAndConfirmEmpty(t *testing.T) {
	metadata := integrations.RequirementMetadataReadResult{Participants: domain.RequirementParticipants{QCOwners: []domain.RequirementPerson{{Name: "靳保新"}, {Name: " 梁聪 "}, {Name: "靳保新"}, {Name: " "}}}}
	if got := releaseNoticeTesters(metadata); !slices.Equal(got, []string{"靳保新", "梁聪"}) {
		t.Fatal(got)
	}
	metadata.ReleaseTesters = []domain.RequirementPerson{}
	if got := releaseNoticeTesters(metadata); len(got) != 0 {
		t.Fatal("confirmed empty roster restored stale people", got)
	}
}
