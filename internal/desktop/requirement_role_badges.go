package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/domain"
)

type requirementRoleBadge struct {
	role, summary, fullText string
}

func requirementPeopleNames(people []domain.RequirementPerson) []string {
	names := make([]string, 0, len(people))
	for _, person := range people {
		name := strings.TrimSpace(person.Name)
		if name == "" && person.Email != nil {
			name = strings.TrimSpace(*person.Email)
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func requirementRoleBadges(developers, testers, products []domain.RequirementPerson) []requirementRoleBadge {
	var badges []requirementRoleBadge
	for _, role := range []struct {
		name   string
		people []domain.RequirementPerson
	}{{"研发", developers}, {"测试", testers}, {"产品", products}} {
		if summary := requirementPeopleSummary(role.name, role.people); summary != "" {
			badges = append(badges, requirementRoleBadge{role.name, summary, role.name + "：" + strings.Join(requirementPeopleNames(role.people), "、")})
		}
	}
	return badges
}

// Both requirement headers share the existing task-detail badge presentation.
// Roles wrap as units; a single long badge stays within the reading column.
func renderRequirementRoleBadges(c *ui.Context, p palette, developers, testers, products []domain.RequirementPerson) {
	badges := requirementRoleBadges(developers, testers, products)
	if len(badges) == 0 {
		return
	}
	ui.Row(c.Key("requirement-role-badges")).FillWidth().MinWidth(0).Gap(6).Wrap().Children(func() {
		for _, badge := range badges {
			ui.Box(c.Key(badge.role)).MinWidth(0).MaxWidthPercent(100).Padding(3, 7).Radius(6).Background(p.soft).
				Tooltip(badge.fullText).Children(func() {
				label(c, badge.summary, 11, p.muted).Description(badge.fullText).MinWidth(0).SingleLine()
			})
		}
	})
}
