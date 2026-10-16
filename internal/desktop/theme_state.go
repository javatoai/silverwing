package desktop

import (
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
)

func effectiveThemeDark(preference string, systemDark bool) bool {
	switch domain.ThemePreference(preference) {
	case domain.ThemeSystem:
		return systemDark
	case domain.ThemeDark:
		return true
	default:
		return false
	}
}

// applyThemeFrame must run first in the root view, before SetTheme replaces the
// SDK's native frame theme. An unloaded snapshot retains the startup option.
func (s *app) applyThemeFrame(c *ui.Context) palette {
	systemDark := c.Theme().Dark
	if s.snapshot.Theme != "" {
		s.dark = effectiveThemeDark(s.snapshot.Theme, systemDark)
	}
	return setTheme(c, s.dark)
}
