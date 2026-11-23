package genbuapi

import (
	"context"
	"fmt"
	"strings"
)

// QueryTagRequest is the typed input for Genbu's query-tag command.
type QueryTagRequest struct {
	Service string `json:"service"`
	Tag     string `json:"tag"`
}

// BuildLogRequest is the typed input for Genbu's tag-build-log command.
type BuildLogRequest struct {
	Service   string `json:"service"`
	Tag       string `json:"tag"`
	TailLines int    `json:"tail_lines"`
}

// ServiceConfigRequest is the typed input for Genbu's query-service-config
// command. Filter is applied only after all sensitive values are redacted.
type ServiceConfigRequest struct {
	Service     string `json:"service"`
	Environment string `json:"environment"`
	Namespace   string `json:"namespace"`
	Filter      string `json:"filter"`
}

// ServicePodsRequest is the typed input for Genbu's query-service-pods command.
type ServicePodsRequest struct {
	Service     string `json:"service"`
	Environment string `json:"environment"`
}

// ServiceConfigResult contains the redacted configuration payload and any
// matching lines. Content and Matches never include known exact credentials.
type ServiceConfigResult struct {
	Service     string   `json:"service"`
	Environment string   `json:"environment"`
	Namespace   string   `json:"namespace"`
	UpdateTime  string   `json:"update_time"`
	Content     string   `json:"content"`
	Contents    []string `json:"contents,omitempty"`
	Matches     []string `json:"matches,omitempty"`
}

// Login exchanges caller-supplied Beast credentials for a Genbu token and
// updates the client's in-memory synchronized configuration.
func (client *Client) Login(ctx context.Context) (Session, error) {
	if client == nil {
		return Session{}, fmt.Errorf("Genbu client is nil")
	}
	ctx, cancel := context.WithTimeout(ctx, client.config.Timeout)
	defer cancel()
	client.mu.Lock()
	defer client.mu.Unlock()
	if strings.TrimSpace(client.config.Username) == "" || client.config.Password == "" {
		return Session{}, fmt.Errorf("Genbu username and password must be configured")
	}
	token, err := client.worker(ctx).login(client.config.Username, client.config.Password)
	if err != nil {
		return Session{}, err
	}
	client.config.Token = token
	client.config.TokenExpiresAt = TokenExpiration(token)
	client.generated++
	return Session{Token: token, TokenExpiresAt: client.config.TokenExpiresAt}, nil
}

// QueryTag fetches the build, UAT, and production status for a service tag.
func (client *Client) QueryTag(ctx context.Context, request QueryTagRequest) (TagStatus, error) {
	var result TagStatus
	err := client.requestToken(ctx, func(worker *genbuClient, token string) error {
		status, err := worker.queryStatus(request.Service, request.Tag, token)
		result = status
		return err
	})
	return result, err
}

// TagBuildLog fetches and redacts a bounded tail of a service tag build log.
func (client *Client) TagBuildLog(ctx context.Context, request BuildLogRequest) (BuildLog, error) {
	if request.TailLines == 0 {
		request.TailLines = DefaultBuildLogTailLines
	}
	if request.TailLines < MinBuildLogTailLines || request.TailLines > MaxBuildLogTailLines {
		return BuildLog{}, fmt.Errorf("tail lines must be between %d and %d", MinBuildLogTailLines, MaxBuildLogTailLines)
	}
	var result BuildLog
	err := client.requestToken(ctx, func(worker *genbuClient, token string) error {
		log, err := worker.buildLog(request.Service, request.Tag, request.TailLines, token)
		if err != nil {
			return err
		}
		config := client.ConfigSnapshot()
		log.Content = redactValues(log.Content, config.Password, token)
		result = log
		return nil
	})
	return result, err
}

// QueryServiceConfig fetches service configuration, redacts it, then applies
// the optional case-insensitive line filter.
func (client *Client) QueryServiceConfig(ctx context.Context, request ServiceConfigRequest) (ServiceConfigResult, error) {
	environment, err := normalizeEnvironmentName(request.Environment)
	if err != nil {
		return ServiceConfigResult{}, err
	}
	request.Environment = environment
	var result ServiceConfigResult
	err = client.requestToken(ctx, func(worker *genbuClient, token string) error {
		snapshot, err := worker.queryConfig(request.Service, request.Namespace, request.Environment, token)
		if err != nil {
			return err
		}
		config := client.ConfigSnapshot()
		contents := make([]string, len(snapshot.Contents))
		for index, item := range snapshot.Contents {
			contents[index] = RedactSensitiveConfig(item, config.Password, token)
		}
		content := RedactSensitiveConfig(JoinConfigContents(snapshot.Contents), config.Password, token)
		var matches []string
		if strings.TrimSpace(request.Filter) != "" {
			matches = FilterConfigLines(content, request.Filter)
		}
		result = ServiceConfigResult{
			Service:     snapshot.Service,
			Environment: snapshot.Environment,
			Namespace:   snapshot.Namespace,
			UpdateTime:  snapshot.UpdateTime,
			Content:     content,
			Contents:    contents,
			Matches:     matches,
		}
		return nil
	})
	return result, err
}

// QueryServicePods fetches the current pod snapshot for a service and
// environment.
func (client *Client) QueryServicePods(ctx context.Context, request ServicePodsRequest) (PodSnapshot, error) {
	environment, err := normalizeEnvironmentName(request.Environment)
	if err != nil {
		return PodSnapshot{}, err
	}
	var result PodSnapshot
	err = client.requestToken(ctx, func(worker *genbuClient, token string) error {
		snapshot, err := worker.queryPods(request.Service, environment, token)
		result = snapshot
		return err
	})
	return result, err
}

// SessionSnapshot returns the current in-memory token for persistence by a
// caller that owns its own config repository.
func (client *Client) SessionSnapshot() Session {
	config := client.ConfigSnapshot()
	return Session{Token: config.Token, TokenExpiresAt: config.TokenExpiresAt}
}

func normalizeEnvironmentName(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "uat", "test", "测试":
		return "UAT", nil
	case "prod", "production", "prd", "生产":
		return "PRD", nil
	default:
		return "", fmt.Errorf("environment must be uat or prod")
	}
}

// JoinConfigContents joins API-returned ConfigMap fragments using the same
// boundary rules as the CLI.
func JoinConfigContents(contents []string) string {
	var builder strings.Builder
	for index, content := range contents {
		if index > 0 && builder.Len() > 0 && content != "" && !strings.HasSuffix(builder.String(), "\n") && !strings.HasPrefix(content, "\n") {
			builder.WriteByte('\n')
		}
		builder.WriteString(content)
	}
	return builder.String()
}
