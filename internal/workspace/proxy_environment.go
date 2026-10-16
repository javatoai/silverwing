package workspace

import (
	"net/url"

	"silverwing-go/internal/domain"
)

// proxyProcessEnvironment returns a complete proxy override for the selected
// command target. It always removes inherited proxy variables so an unchecked
// proxy target cannot accidentally inherit credentials or routing from the
// desktop process.
func proxyProcessEnvironment(config domain.AppConfig, target domain.CommandProxyTarget) (map[string]string, []string) {
	remove := []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"}
	enabled := false
	for _, configured := range config.CommandProxyTargets {
		if configured == target {
			enabled = true
			break
		}
	}
	if !enabled || config.CommandProxyURL == nil {
		return nil, remove
	}
	proxy, err := domain.NormalizeCommandProxyURL(config.CommandProxyURL)
	if err != nil || proxy == nil {
		return nil, remove
	}
	parsed, err := url.Parse(*proxy)
	if err != nil {
		return nil, remove
	}
	if config.CommandProxyUsername != nil && *config.CommandProxyUsername != "" && config.CommandProxyPassword != nil {
		parsed.User = url.UserPassword(*config.CommandProxyUsername, *config.CommandProxyPassword)
	}
	value := parsed.String()
	env := map[string]string{
		"HTTP_PROXY": value, "HTTPS_PROXY": value, "ALL_PROXY": value,
		"http_proxy": value, "https_proxy": value, "all_proxy": value,
	}
	if noProxy, err := domain.NormalizeCommandProxyNoProxy(config.CommandProxyNoProxy); err == nil && noProxy != nil {
		env["NO_PROXY"], env["no_proxy"] = *noProxy, *noProxy
	}
	return env, remove
}
