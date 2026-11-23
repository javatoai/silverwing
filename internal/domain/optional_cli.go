package domain

import "strings"

// Lark uses its saved executable when its switch is unset. Embedded platforms
// use the credential status from the application, independent of old paths.
type OptionalCLISettings struct {
	Lark   *bool `json:"lark"`
	Genbu  *bool `json:"genbu"`
	Dragon *bool `json:"dragon"`
}

func (cfg AppConfig) OptionalCLIEnabled(tool string) bool {
	if tool == "genbu" || tool == "dragon" {
		return cfg.PlatformEnabled(tool, false)
	}
	if tool == "lark" && cfg.OptionalCLIs.Lark != nil {
		return *cfg.OptionalCLIs.Lark
	}
	return cfg.OptionalCLIPath(tool) != ""
}

func (cfg AppConfig) PlatformEnabled(tool string, configured bool) bool {
	var enabled *bool
	switch tool {
	case "genbu":
		enabled = cfg.OptionalCLIs.Genbu
	case "dragon":
		enabled = cfg.OptionalCLIs.Dragon
	default:
		return false
	}
	if enabled != nil {
		return *enabled
	}
	return configured
}

func (cfg AppConfig) OptionalCLIPath(tool string) string {
	var path *string
	switch tool {
	case "lark":
		path = cfg.LarkExecutablePath
	}
	if path == nil {
		return ""
	}
	return strings.TrimSpace(*path)
}
