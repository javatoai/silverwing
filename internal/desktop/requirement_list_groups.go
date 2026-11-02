package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

type requirementListGroup struct {
	key, title string
	items      []application.Requirement
}

// Project groups without reordering the source list: selection and refresh
// snapshots keep their original indices and work-item identities.
func groupRequirementList(items []application.Requirement) []requirementListGroup {
	groups := [...]requirementListGroup{
		{key: "story", title: "Story"},
		{key: "technical", title: "Technical"},
		{key: "bug", title: "Bug"},
		{key: "task", title: "Task"},
		{key: "other", title: "Other"},
	}
	for _, item := range items {
		index := len(groups) - 1
		switch strings.ToLower(strings.TrimSpace(item.Type)) {
		case "userstory":
			index = 0
		case "technical":
			index = 1
		case "bug":
			index = 2
		case "othertask":
			index = 3
		}
		groups[index].items = append(groups[index].items, item)
	}
	visible := make([]requirementListGroup, 0, len(groups))
	for _, group := range groups {
		if len(group.items) > 0 {
			visible = append(visible, group)
		}
	}
	return visible
}

func requirementGroupHeader(c *ui.Context, p palette, group requirementListGroup) {
	ui.Row(c.Key("requirement-group-"+group.key)).FillWidth().Height(22).Padding(0, 2).Gap(8).AlignItems(ui.Center).Children(func() {
		label(c, fmt.Sprintf("%s · %d", group.title, len(group.items)), 11, p.muted).FontWeight(600).SingleLine()
		ui.Box(c).Grow(1).MinWidth(0).Height(1).Background(p.line)
	})
}
