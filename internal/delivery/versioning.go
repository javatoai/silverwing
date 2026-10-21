package delivery

import (
	"strconv"
	"strings"

	"silverwing-go/internal/domain"
)

func versionTagsFromOutput(output string) []domain.VersionTag {
	entries := make([]domain.VersionTag, 0)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, " ", 2)
		if len(fields) != 2 {
			continue
		}
		stamp, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(fields[1])
		if name != "" {
			entries = append(entries, domain.VersionTag{Name: name, CreatedAtEpochSeconds: stamp})
		}
	}
	return entries
}
