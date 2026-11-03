// Package networkproxy applies the shared proxy settings to HTTP and command
// adapters without changing global transports, environment or Git settings.
package networkproxy

import (
	"net/url"
	"strings"

	"silverwing-go/internal/domain"
)

func Enabled(config domain.AppConfig, target domain.CommandProxyTarget) bool {
	for _, candidate := range config.CommandProxyTargets {
		if candidate == target {
			return true
		}
	}
	return false
}

func Environment(config domain.AppConfig, target domain.CommandProxyTarget) (map[string]string, []string) {
	remove := []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"}
	env := map[string]string{}
	if !Enabled(config, target) {
		return env, remove
	}
	endpoint, err := domain.NormalizeCommandProxyURL(config.CommandProxyURL)
	if err != nil || endpoint == nil {
		return env, remove
	}
	parsed, err := url.Parse(*endpoint)
	if err != nil {
		return env, remove
	}
	if config.CommandProxyUsername != nil && strings.TrimSpace(*config.CommandProxyUsername) != "" && config.CommandProxyPassword != nil && *config.CommandProxyPassword != "" {
		parsed.User = url.UserPassword(strings.TrimSpace(*config.CommandProxyUsername), *config.CommandProxyPassword)
	}
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		env[name] = parsed.String()
	}
	if noProxy, err := domain.NormalizeCommandProxyNoProxy(config.CommandProxyNoProxy); err == nil && noProxy != nil {
		env["NO_PROXY"], env["no_proxy"] = *noProxy, *noProxy
	}
	return env, remove
}

func Redact(config domain.AppConfig, message string) string {
	if config.CommandProxyURL != nil && *config.CommandProxyURL != "" {
		message = strings.ReplaceAll(message, *config.CommandProxyURL, "[代理地址已隐藏]")
		if parsed, err := url.Parse(*config.CommandProxyURL); err == nil {
			if config.CommandProxyUsername != nil && config.CommandProxyPassword != nil {
				parsed.User = url.UserPassword(strings.TrimSpace(*config.CommandProxyUsername), *config.CommandProxyPassword)
				message = strings.ReplaceAll(message, parsed.String(), "[代理地址已隐藏]")
			}
			if parsed.Host != "" {
				message = strings.ReplaceAll(message, parsed.Host, "[代理地址已隐藏]")
			}
		}
	}
	for _, secret := range []*string{config.CommandProxyUsername, config.CommandProxyPassword} {
		if secret != nil && *secret != "" {
			message = strings.ReplaceAll(message, *secret, "[已隐藏]")
		}
	}
	return message
}
