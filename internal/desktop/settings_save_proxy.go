package desktop

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"silverwing-go/internal/domain"
)

type settingsProxyDraft struct {
	scheme, host, port, noProxy, username, password string
	authentication                                  bool
	targets                                         []string
}

type settingsProxyAutosaveDraft struct {
	Scheme, Host, Port, NoProxy, Username, Password string
	Authentication                                  bool
	Targets                                         []string
}

func (s *app) observeSettingsProxyAutosave(readDraft func() settingsProxyDraft) {
	readLatest := func() string {
		draft := readDraft()
		encoded, _ := json.Marshal(settingsProxyAutosaveDraft{Scheme: draft.scheme, Host: draft.host, Port: draft.port, NoProxy: draft.noProxy,
			Username: draft.username, Password: draft.password, Authentication: draft.authentication, Targets: draft.targets})
		return string(encoded)
	}
	s.observeSettingsAutosave("settings:proxy-form", readLatest(), s.saveSettingsProxyAutosave, readLatest)
}

func (s *app) saveSettingsProxyAutosave(value string) {
	var captured settingsProxyAutosaveDraft
	if err := json.Unmarshal([]byte(value), &captured); err != nil {
		s.lastError = "无法读取代理设置草稿。"
		return
	}
	config, err := s.settingsConfigMap()
	if err != nil {
		s.lastError = err.Error()
		return
	}
	draft := settingsProxyDraft{scheme: captured.Scheme, host: captured.Host, port: captured.Port, noProxy: captured.NoProxy,
		username: captured.Username, password: captured.Password, authentication: captured.Authentication, targets: captured.Targets}
	if err := updateSettingsProxyConfig(config, draft); err != nil {
		s.lastError = err.Error()
		return
	}
	s.saveSettingsConfigMap(config, "commandProxyUrl", "commandProxyNoProxy", "commandProxyUsername", "commandProxyPassword", "commandProxyTargets")
}

// Validate the complete form before changing its save payload. Passwords are
// literal input: only an empty password means no authentication.
func updateSettingsProxyConfig(config map[string]any, draft settingsProxyDraft) error {
	port := strings.TrimSpace(draft.port)
	if strings.TrimSpace(draft.host) != "" {
		parsed, err := strconv.Atoi(port)
		if err != nil {
			return fmt.Errorf("命令代理端口必须是数字")
		}
		if parsed < 1 || parsed > 65535 {
			return fmt.Errorf("命令代理端口必须在 1 到 65535 之间")
		}
		port = strconv.Itoa(parsed)
	}
	endpointInput := proxyURL(draft.host, port, draft.scheme)
	endpoint, err := domain.NormalizeCommandProxyURL(&endpointInput)
	if err != nil {
		return err
	}
	noProxy, err := domain.NormalizeCommandProxyNoProxy(&draft.noProxy)
	if err != nil {
		return err
	}
	username, password := "", ""
	if draft.authentication {
		username, password = strings.TrimSpace(draft.username), draft.password
		if username == "" || password == "" {
			return fmt.Errorf("启用代理认证后，账号和密码必须同时填写")
		}
	}
	if endpoint == nil && (noProxy != nil || username != "" || password != "") {
		return fmt.Errorf("请先填写代理主机和端口，再设置例外地址或认证信息")
	}
	config["commandProxyUrl"], config["commandProxyNoProxy"], config["commandProxyUsername"], config["commandProxyPassword"] = nil, nil, nil, nil
	config["commandProxyTargets"] = []string{}
	if endpoint != nil {
		config["commandProxyUrl"] = *endpoint
		if noProxy != nil {
			config["commandProxyNoProxy"] = *noProxy
		}
		if draft.authentication {
			config["commandProxyUsername"], config["commandProxyPassword"] = username, password
		}
		config["commandProxyTargets"] = append([]string{}, draft.targets...)
	}
	return nil
}
