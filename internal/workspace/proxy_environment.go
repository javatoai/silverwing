package workspace

import (
	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
)

func proxyProcessEnvironment(config domain.AppConfig, target domain.CommandProxyTarget) (map[string]string, []string) {
	return commandruntime.ProxyEnvironment(config, target)
}
