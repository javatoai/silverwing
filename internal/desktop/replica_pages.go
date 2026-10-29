package desktop

import (
	"encoding/json"
	"fmt"
	"strings"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo/ui"
)

func (s *app) requirementForURL(url string) (application.Requirement, bool) {
	url = strings.TrimSpace(url)
	if url == "" {
		return application.Requirement{}, false
	}
	for _, requirement := range s.requirementUnit.state.requirements {
		if strings.TrimSpace(requirement.URL) == url && requirement.WorkItem.ID != "" {
			return requirement, true
		}
	}
	return application.Requirement{}, false
}

func setFormValue(fields []formField, key, value string) {
	for index := range fields {
		if fields[index].key == key {
			fields[index].value = value
			return
		}
	}
}

func jsonBoolString(value any, fallback string) string {
	if boolean, ok := value.(bool); ok {
		return fmt.Sprint(boolean)
	}
	return fallback
}

func jsonStringOrDefault(value any, fallback string) string {
	if value == nil {
		return fallback
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fallback
	}
	return string(encoded)
}

func (s *app) replicaSettings(c *ui.Context, p palette) {
	s.settingsScreen(c, p)
}

func (s *app) replicaSkills(c *ui.Context, p palette) {
	s.renderSkillsBrowser(c, p)
}

func emptyPanel(c *ui.Context, p palette, title, detail string) {
	ui.Column(c).FillWidth().Padding(20).Gap(8).Background(p.surface).Border(1, p.line).Radius(12).Children(func() {
		label(c, title, 14, p.text).FontWeight(600)
		label(c, detail, 12, p.muted).LineHeight(1.5)
	})
}
