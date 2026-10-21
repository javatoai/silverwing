package workspace

import (
	"strings"

	"silverwing-go/internal/domain"
)

// FormatTaskServices preserves task workspace order, including repeated
// service names when a service has several modules.
func FormatTaskServices(task domain.TaskManifest, includeRequirementLink bool) string {
	return formatTaskBranchInfo(task, includeRequirementLink, false)
}

func FormatTaskBranchInfo(task domain.TaskManifest, includeRequirementLink bool) string {
	return formatTaskBranchInfo(task, includeRequirementLink, true)
}

func formatTaskBranchInfo(task domain.TaskManifest, includeRequirementLink, branches bool) string {
	var output strings.Builder
	if includeRequirementLink && strings.TrimSpace(task.RequirementLink) != "" {
		output.WriteString("需求链接：")
		output.WriteString(strings.TrimSpace(task.RequirementLink))
		output.WriteString("\n\n")
	}
	for index, entry := range task.Services {
		if index > 0 {
			output.WriteByte('\n')
		}
		output.WriteString(entry.ServiceName)
		if branches {
			output.WriteString("：")
			output.WriteString(entry.Branch)
		}
	}
	return output.String()
}
