package application

import (
	"strings"

	"silverwing-go/internal/integrations"
)

func releaseNoticeTesters(metadata integrations.RequirementMetadataReadResult) []string {
	people := metadata.Participants.QCOwners
	if metadata.ReleaseTesters != nil {
		people = metadata.ReleaseTesters
	}
	var names []string
	seen := map[string]bool{}
	for _, person := range people {
		name := strings.Join(strings.Fields(person.Name), " ")
		if name != "" && !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	return names
}
