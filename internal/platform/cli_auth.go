package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type cliAuthSession struct {
	SessionID              string         `json:"sessionId"`
	Tool                   string         `json:"tool"`
	State                  string         `json:"state"`
	AuthorizationURL       string         `json:"authorizationUrl"`
	UserCode               string         `json:"userCode,omitempty"`
	ExpiresInSeconds       int64          `json:"expiresInSeconds"`
	ExpiresAtEpochMillis   int64          `json:"expiresAtEpochMillis"`
	PollingIntervalSeconds int64          `json:"pollingIntervalSeconds"`
	Polling                bool           `json:"polling"`
	Error                  string         `json:"error,omitempty"`
	Status                 map[string]any `json:"status,omitempty"`
	deviceCode             string
	clientID               string
	host                   string
	verifyURL              string
	expiresAt              time.Time
	pollCancel             context.CancelFunc
	pollGeneration         uint64
}

func (s *Service) executeCLIAuth(ctx context.Context, kind string, payload map[string]any) (any, error) {
	switch kind {
	case "cli.auth.start":
		return s.startCLIAuth(ctx, payload)
	case "cli.auth.session":
		return s.readCLIAuthSession(payload)
	case "cli.auth.poll":
		return s.pollCLIAuth(ctx, payload)
	case "cli.auth.cancel":
		return s.cancelCLIAuth(payload)
	case "cli.auth.logout":
		return s.logoutCLIAuth(ctx, payload)
	default:
		return nil, fmt.Errorf("未知 CLI 认证操作：%s", kind)
	}
}

func (s *Service) startCLIAuth(ctx context.Context, payload map[string]any) (any, error) {
	tool := strings.ToLower(payloadString(payload, "tool"))
	if tool != "meegle" {
		return nil, fmt.Errorf("只支持 Meegle 设备码登录")
	}
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, err
	}
	var command string
	var target domain.CommandProxyTarget
	cfg, command, target, err = s.resolveCLIExecutable(ctx, cfg, tool)
	if err != nil {
		return nil, err
	}
	session := &cliAuthSession{Tool: tool, State: "AUTHORIZATION_PENDING", Polling: true}
	if tool == "meegle" {
		host := payloadString(payload, "host")
		if host == "" {
			host = "project.feishu.cn"
		}
		args := []string{"auth", "login", "--device-code", "--phase", "init", "--host", host, "--format", "json"}
		result, runErr := s.runToolWithEnv(ctx, cfg, target, command, args, "", 30*time.Second, cliEnvironmentWithRunner(ctx, s.runner, tool, command))
		if runErr != nil {
			return nil, wrapCommandDisplayError(fmt.Sprintf("生成 Meegle 登录验证码失败：%s", redactProxy(cfg, safeCLIAuthError(runErr.Error(), nil))), runErr)
		}
		var response meegleDeviceCodeInit
		if err := json.Unmarshal([]byte(result.Stdout), &response); err != nil {
			return nil, fmt.Errorf("Meegle 登录验证码 JSON 解析失败：%w", err)
		}
		if response.VerificationURI == "" || response.UserCode == "" || response.DeviceCode == "" || response.ClientID == "" || response.ExpiresInSeconds <= 0 {
			return nil, fmt.Errorf("Meegle 登录验证码响应不完整")
		}
		interval := response.Interval
		if interval < 2 {
			interval = 2
		}
		if interval > 60 {
			interval = 60
		}
		session.AuthorizationURL = firstNonBlank(response.VerificationURIComplete, response.VerificationURI)
		session.UserCode = response.UserCode
		session.ExpiresInSeconds = response.ExpiresInSeconds
		session.PollingIntervalSeconds = interval
		session.deviceCode = response.DeviceCode
		session.clientID = response.ClientID
		session.host = host
		session.verifyURL = response.VerificationURI
	}
	if session.AuthorizationURL == "" {
		return nil, fmt.Errorf("CLI 登录响应缺少授权链接")
	}
	if session.ExpiresInSeconds > 24*60*60 {
		session.ExpiresInSeconds = 24 * 60 * 60
	}
	session.expiresAt = time.Now().Add(time.Duration(session.ExpiresInSeconds) * time.Second)
	session.ExpiresAtEpochMillis = session.expiresAt.UnixMilli()
	if session.SessionID, err = newAuthSessionID(); err != nil {
		return nil, err
	}
	s.authMu.Lock()
	for id, existing := range s.authSessions {
		if existing.Tool == tool && existing.State != "AUTHENTICATED" && existing.State != "EXPIRED" && existing.State != "FAILED" && existing.State != "CANCELLED" {
			if existing.pollCancel != nil {
				existing.pollCancel()
			}
			existing.deviceCode = ""
			existing.clientID = ""
			existing.State = "CANCELLED"
			existing.Polling = false
			existing.Error = ""
			s.authSessions[id] = existing
		}
	}
	s.authSessions[session.SessionID] = session
	result := publicAuthSession(session)
	s.authMu.Unlock()
	return result, nil
}

func (s *Service) readCLIAuthSession(payload map[string]any) (any, error) {
	id := payloadString(payload, "sessionId")
	tool := strings.ToLower(payloadString(payload, "tool"))
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if id == "" {
		id = latestAuthSessionID(s.authSessions, tool)
	}
	session := s.authSessions[id]
	if session == nil || (tool != "" && session.Tool != tool) {
		return nil, fmt.Errorf("找不到 CLI 登录会话")
	}
	s.expireAuthSessionLocked(session)
	return publicAuthSession(session), nil
}

func (s *Service) pollCLIAuth(ctx context.Context, payload map[string]any) (any, error) {
	id := payloadString(payload, "sessionId")
	if id == "" {
		return nil, fmt.Errorf("缺少 sessionId")
	}
	s.authMu.Lock()
	session := s.authSessions[id]
	if session == nil {
		s.authMu.Unlock()
		return nil, fmt.Errorf("找不到 CLI 登录会话")
	}
	s.expireAuthSessionLocked(session)
	if session.State == "EXPIRED" || session.State == "AUTHENTICATED" || session.State == "FAILED" || session.State == "CANCELLED" {
		result := publicAuthSession(session)
		s.authMu.Unlock()
		return result, nil
	}
	if session.Polling && session.pollCancel != nil {
		result := publicAuthSession(session)
		s.authMu.Unlock()
		return result, nil
	}
	session.Polling = true
	session.Error = ""
	session.pollGeneration++
	generation := session.pollGeneration
	pollCtx, cancel := context.WithCancel(ctx)
	session.pollCancel = cancel
	snapshot := *session
	s.authMu.Unlock()

	var status map[string]any
	var pollErr error
	var nextState string
	var nextInterval int64 = snapshot.PollingIntervalSeconds
	if snapshot.Tool == "meegle" {
		if snapshot.State == "CONFIRMING" {
			status, pollErr = s.currentCLIStatus(pollCtx, "meegle")
			if pollErr == nil && statusBool(status, "authenticated") {
				nextState = "AUTHENTICATED"
			} else {
				nextState = "CONFIRMING"
			}
		} else if time.Now().After(snapshot.expiresAt) {
			nextState = "EXPIRED"
		} else {
			cfg, err := s.loadConfig()
			var command string
			var target domain.CommandProxyTarget
			if err == nil {
				cfg, command, target, err = s.resolveCLIExecutable(pollCtx, cfg, snapshot.Tool)
			}
			if err != nil {
				pollErr = err
				nextState = "AUTHORIZATION_PENDING"
			} else {
				args := []string{"auth", "login", "--device-code", "--phase", "poll", "--device-code-value", snapshot.deviceCode, "--client-id", snapshot.clientID, "--host", snapshot.host, "--once", "--format", "json"}
				result, runErr := s.runToolWithEnv(pollCtx, cfg, target, command, args, "", 20*time.Second, cliEnvironmentWithRunner(pollCtx, s.runner, "meegle", command))
				if runErr != nil {
					pollErr = runErr
					nextState = "AUTHORIZATION_PENDING"
				} else {
					var response meegleDeviceCodePoll
					if err := json.Unmarshal([]byte(result.Stdout), &response); err != nil {
						pollErr = fmt.Errorf("Meegle 登录授权状态 JSON 解析失败")
						nextState = "AUTHORIZATION_PENDING"
					} else {
						switch response.Status {
						case "ok":
							nextState = "CONFIRMING"
							status, pollErr = s.currentCLIStatus(pollCtx, "meegle")
							if pollErr == nil && statusBool(status, "authenticated") {
								nextState = "AUTHENTICATED"
							}
						case "authorization_pending":
							nextState = "AUTHORIZATION_PENDING"
						case "slow_down":
							nextState = "AUTHORIZATION_PENDING"
							nextInterval += 5
							if nextInterval > 60 {
								nextInterval = 60
							}
						case "expired_token":
							nextState = "EXPIRED"
						default:
							pollErr = fmt.Errorf("Meegle 登录授权状态异常：%s", firstNonBlank(response.Status, "未知"))
							nextState = "AUTHORIZATION_PENDING"
						}
					}
				}
			}
		}
	}
	cancel()
	if pollErr != nil {
		// Use the request snapshot before cancellation or terminal state clears
		// its device-code secrets, and retain cleanup/cancellation identity.
		pollErr = wrapCommandDisplayError(safeCLIAuthError(redactProxyForTool(s, snapshot.Tool, pollErr.Error()), &snapshot), pollErr)
	}

	s.authMu.Lock()
	current := s.authSessions[id]
	if current == nil || current.pollGeneration != generation {
		s.authMu.Unlock()
		if process.IsCleanupFailure(pollErr) {
			return nil, pollErr
		}
		return nil, fmt.Errorf("CLI 登录会话已结束")
	}
	resultErr := finishCLIAuthPollState(current, nextState, nextInterval, status, pollErr)
	result := publicAuthSession(current)
	s.authMu.Unlock()
	return result, resultErr
}

// The caller has already validated the session generation and sanitized the
// error with the request's secrets. Cleanup failure is terminal even when its
// cause includes cancellation; cancelled and expired sessions keep their state.
func finishCLIAuthPollState(current *cliAuthSession, nextState string, nextInterval int64, status map[string]any, pollErr error) error {
	cleanupFailure := process.IsCleanupFailure(pollErr)
	if cleanupFailure {
		nextState = "FAILED"
	}
	current.pollCancel = nil
	current.Polling = nextState == "AUTHORIZATION_PENDING" || nextState == "CONFIRMING"
	if current.State == "CANCELLED" || current.State == "EXPIRED" {
		current.Polling = false
		if cleanupFailure {
			return pollErr
		}
		return nil
	}
	if nextState != "" {
		current.State = nextState
	}
	current.PollingIntervalSeconds = nextInterval
	if pollErr != nil && (cleanupFailure || !errors.Is(pollErr, context.Canceled)) {
		current.Error = safeCLIAuthError(pollErr.Error(), current)
	}
	if status != nil {
		current.Status = status
	}
	if current.State == "AUTHENTICATED" || current.State == "EXPIRED" || current.State == "FAILED" {
		current.deviceCode = ""
		current.clientID = ""
		current.verifyURL = ""
		current.pollCancel = nil
		current.Polling = false
	}
	if cleanupFailure {
		return pollErr
	}
	return nil
}

func (s *Service) cancelCLIAuth(payload map[string]any) (any, error) {
	id := payloadString(payload, "sessionId")
	tool := strings.ToLower(payloadString(payload, "tool"))
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if id == "" {
		id = latestAuthSessionID(s.authSessions, tool)
	}
	session := s.authSessions[id]
	if session == nil || (tool != "" && session.Tool != tool) {
		return nil, fmt.Errorf("找不到 CLI 登录会话")
	}
	if session.State == "AUTHENTICATED" || session.State == "EXPIRED" || session.State == "FAILED" || session.State == "CANCELLED" {
		return publicAuthSession(session), nil
	}
	if session.pollCancel != nil {
		session.pollCancel()
		session.pollCancel = nil
	}
	session.deviceCode = ""
	session.clientID = ""
	session.verifyURL = ""
	session.State = "CANCELLED"
	session.Polling = false
	session.Error = ""
	return publicAuthSession(session), nil
}

func (s *Service) logoutCLIAuth(ctx context.Context, payload map[string]any) (any, error) {
	tool := strings.ToLower(payloadString(payload, "tool"))
	if tool != "meegle" {
		return nil, fmt.Errorf("只支持退出 Meegle 登录")
	}
	s.authMu.Lock()
	for _, session := range s.authSessions {
		if session.Tool != tool {
			continue
		}
		if session.pollCancel != nil {
			session.pollCancel()
			session.pollCancel = nil
		}
		session.deviceCode = ""
		session.clientID = ""
		session.verifyURL = ""
		session.State = "CANCELLED"
		session.Polling = false
	}
	s.authMu.Unlock()
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, err
	}
	cfg, command, target, err := s.resolveCLIExecutable(ctx, cfg, tool)
	if err != nil {
		return nil, err
	}
	if tool == "meegle" {
		if _, err := s.runToolWithEnv(ctx, cfg, target, command, []string{"auth", "logout"}, "", 10*time.Second, cliEnvironmentWithRunner(ctx, s.runner, tool, command)); err != nil {
			return nil, wrapCommandDisplayError(fmt.Sprintf("退出 Meegle 登录失败：%s", safeCLIAuthError(redactProxy(cfg, err.Error()), nil)), err)
		}
	}
	status, err := s.currentCLIStatus(ctx, tool)
	if err != nil {
		return nil, err
	}
	return map[string]any{"tool": tool, "loggedOut": true, "status": status}, nil
}

func (s *Service) currentCLIStatus(ctx context.Context, tool string) (map[string]any, error) {
	value, err := s.executeCLI(ctx, "cli.status", map[string]any{"tool": tool})
	if err != nil {
		return nil, err
	}
	status, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("读取 %s 登录状态失败", tool)
	}
	return status, nil
}

func (s *Service) expireAuthSessionLocked(session *cliAuthSession) {
	if session == nil || session.State == "AUTHENTICATED" || session.State == "EXPIRED" || session.State == "FAILED" || session.State == "CANCELLED" {
		return
	}
	if !session.expiresAt.IsZero() && !time.Now().Before(session.expiresAt) {
		if session.pollCancel != nil {
			session.pollCancel()
			session.pollCancel = nil
		}
		session.State = "EXPIRED"
		session.Polling = false
		session.deviceCode = ""
		session.clientID = ""
		session.verifyURL = ""
		session.Error = "授权码已过期，请重新生成验证码。"
	}
}

func publicAuthSession(session *cliAuthSession) map[string]any {
	if session == nil {
		return nil
	}
	copy := *session
	copy.deviceCode, copy.clientID, copy.host, copy.verifyURL = "", "", "", ""
	copy.pollCancel = nil
	copy.expiresAt = time.Time{}
	copy.pollGeneration = 0
	return map[string]any{
		"sessionId":              copy.SessionID,
		"tool":                   copy.Tool,
		"state":                  copy.State,
		"authorizationUrl":       copy.AuthorizationURL,
		"userCode":               copy.UserCode,
		"expiresInSeconds":       copy.ExpiresInSeconds,
		"expiresAtEpochMillis":   copy.ExpiresAtEpochMillis,
		"pollingIntervalSeconds": copy.PollingIntervalSeconds,
		"polling":                copy.Polling,
		"error":                  copy.Error,
		"status":                 copy.Status,
	}
}

func latestAuthSessionID(sessions map[string]*cliAuthSession, tool string) string {
	latestID := ""
	var latestCreated int64
	for id, session := range sessions {
		if tool != "" && session.Tool != tool {
			continue
		}
		if session.ExpiresAtEpochMillis > latestCreated {
			latestCreated = session.ExpiresAtEpochMillis
			latestID = id
		}
	}
	return latestID
}

type meegleDeviceCodeInit struct {
	DeviceCode              string `json:"device_code"`
	ClientID                string `json:"client_id"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresInSeconds        int64  `json:"expires_in"`
	Interval                int64  `json:"interval"`
}

type meegleDeviceCodePoll struct {
	Status string `json:"status"`
}

func newAuthSessionID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("生成 CLI 登录会话 ID：%w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func statusBool(status map[string]any, key string) bool {
	value, _ := status[key].(bool)
	return value
}

func safeCLIAuthError(message string, session *cliAuthSession) string {
	if session != nil {
		if session.deviceCode != "" {
			message = strings.ReplaceAll(message, session.deviceCode, "[已隐藏]")
		}
		if session.clientID != "" {
			message = strings.ReplaceAll(message, session.clientID, "[已隐藏]")
		}
		if session.verifyURL != "" {
			message = strings.ReplaceAll(message, session.verifyURL, "[授权链接已隐藏]")
		}
	}
	message = safeAuthDiagnostic(message)
	message = strings.TrimSpace(strings.SplitN(message, "\n", 2)[0])
	if len(message) > 240 {
		message = message[:240]
	}
	if message == "" {
		return "未知错误"
	}
	return message
}

func safeAuthDiagnostic(message string) string {
	message = regexpSecretFields.ReplaceAllString(message, "$1=[已隐藏]")
	message = regexpURLs.ReplaceAllString(message, "[链接已隐藏]")
	return message
}

func redactProxyForTool(s *Service, tool, message string) string {
	if s == nil {
		return message
	}
	cfg, err := s.loadConfig()
	if err != nil {
		return message
	}
	return redactProxy(cfg, message)
}

// Auth control commands can succeed with top-level fields and no data payload.
var regexpSecretFields = regexp.MustCompile(`(?i)(device[_-]?code|access[_-]?token|refresh[_-]?token)\s*[:=]\s*\S+`)
var regexpURLs = regexp.MustCompile(`https?://\S+`)
