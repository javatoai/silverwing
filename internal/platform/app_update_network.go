package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/networkproxy"
)

func (s *Service) appUpdateHTTPClient(timeout time.Duration) (*http.Client, error) {
	client, _, err := s.appUpdateHTTPClientSnapshot(timeout)
	return client, err
}

// Cooldowns follow the explicitly selected route. A settings change takes
// effect on the next operation; no credentials are retained in the route key.
func (s *Service) appUpdateHTTPClientSnapshot(timeout time.Duration) (*http.Client, string, error) {
	config, err := s.loadConfig()
	if err != nil {
		return nil, "", err
	}
	client, err := networkproxy.NewHTTPClient(config, domain.ProxyTargetAppUpdates, timeout)
	if err != nil {
		return nil, "", err
	}
	environment, _ := networkproxy.Environment(config, domain.ProxyTargetAppUpdates)
	encoded, _ := json.Marshal(environment)
	digest := sha256.Sum256(encoded)
	return client, hex.EncodeToString(digest[:]), nil
}
