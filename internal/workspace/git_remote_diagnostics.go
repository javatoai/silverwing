package workspace

import (
	"context"
	"errors"
	"maps"
	"regexp"
	"slices"
	"strings"

	"silverwing-go/internal/process"
)

type GitRemoteFailureKind string

const (
	GitRemoteAuthentication GitRemoteFailureKind = "authentication"
	GitRemoteHostKey        GitRemoteFailureKind = "host-key"
	GitRemotePortRefused    GitRemoteFailureKind = "port-refused"
	GitRemoteClosed         GitRemoteFailureKind = "connection-closed"
	GitRemoteUnavailable    GitRemoteFailureKind = "unavailable"
	GitRemoteTimeout        GitRemoteFailureKind = "timeout"
	GitRemoteMissing        GitRemoteFailureKind = "remote-missing"
	GitRemoteBranchMissing  GitRemoteFailureKind = "branch-missing"
	GitRemoteTagConflict    GitRemoteFailureKind = "tag-conflict"
	GitRemotePushRejected   GitRemoteFailureKind = "push-rejected"
	GitRemoteOther          GitRemoteFailureKind = "other"
)

// GitRemoteFailure exposes a diagnosis without carrying raw Git or helper output.
type GitRemoteFailure struct {
	Kind GitRemoteFailureKind
	port string
}

func (e *GitRemoteFailure) Error() string {
	switch e.Kind {
	case GitRemoteAuthentication:
		return "远端认证失败，请检查 Git 凭据、SSH 密钥或仓库访问权限。"
	case GitRemoteHostKey:
		return "SSH 主机密钥验证失败，请核对远端主机和 known_hosts 配置。"
	case GitRemotePortRefused:
		if e.port != "" {
			return "远端端口 " + e.port + " 拒绝连接，请检查网络、SSH 或代理配置。"
		}
		return "远端端口拒绝连接，请检查网络或代理配置。"
	case GitRemoteClosed:
		if e.port != "" {
			return "远端连接在端口 " + e.port + " 被关闭，请检查网络、SSH 或代理配置。"
		}
		return "远端连接被关闭，请检查网络、SSH 或代理配置。"
	case GitRemoteUnavailable:
		return "远端暂时不可达，请检查网络或代理配置。"
	case GitRemoteTimeout:
		return "远端连接超时，请检查网络或代理配置后重试。"
	case GitRemoteMissing:
		return "本地仓库未配置所选远端。"
	case GitRemoteBranchMissing:
		return "远端可连接，但所选基础分支不存在。"
	case GitRemoteTagConflict:
		return "本地已有同名 Tag，且指向与远端不同，请核对 Tag 后重试。"
	case GitRemotePushRejected:
		return "远端拒绝推送，请刷新远端后重试。"
	default:
		return "Git 远端访问失败，请检查仓库地址、Git 配置和访问权限。"
	}
}

func GitRemoteNeedsAuthentication(err error) bool {
	var failure *GitRemoteFailure
	return errors.As(err, &failure) && failure.Kind == GitRemoteAuthentication
}

// withRemotePrompt changes only the child environment and leaves Git's
// configured credential helpers and transport configuration in place.
func (g *GitClient) withRemotePrompt(allowHelperUI bool) *GitClient {
	if g == nil {
		g = NewGitClient(nil, "")
	}
	git := *g
	git.Environment = maps.Clone(g.Environment)
	if git.Environment == nil {
		git.Environment = map[string]string{}
	}
	git.RemoveEnvironment = append(slices.Clone(g.RemoveEnvironment), "GIT_CURL_VERBOSE")
	delete(git.Environment, "GIT_CURL_VERBOSE")
	for _, key := range []string{"GIT_TRACE", "GIT_TRACE_PACKET", "GIT_TRACE_CURL", "GIT_TRACE2", "GIT_TRACE2_EVENT", "GIT_TRACE2_PERF", "GCM_TRACE", "GCM_TRACE_SECRETS", "GCM_TRACE_MSAUTH"} {
		git.Environment[key] = "0"
	}
	git.Environment["LC_ALL"] = "C"
	git.Environment["GIT_TRACE_REDACT"] = "1"
	git.Environment["GIT_TERMINAL_PROMPT"] = "0"
	if allowHelperUI {
		git.Environment["GCM_INTERACTIVE"] = "auto"
	} else {
		git.Environment["GCM_INTERACTIVE"] = "never"
		git.Environment["GIT_ASKPASS"] = ""
		git.Environment["SSH_ASKPASS_REQUIRE"] = "never"
	}
	return &git
}

var (
	refusedSSHPort = regexp.MustCompile(`\bport ([0-9]{1,5}): connection refused`)
	closedSSHPort  = regexp.MustCompile(`connection closed by [^\r\n]+ port ([0-9]{1,5})\b`)
)

func diagnoseGitRemoteError(ctx context.Context, err error) error {
	if process.IsCleanupFailure(err) {
		return &process.CleanupError{}
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return &GitRemoteFailure{Kind: GitRemoteTimeout}
	}
	var failure *GitException
	if !errors.As(err, &failure) {
		return &GitRemoteFailure{Kind: GitRemoteOther}
	}
	return ClassifyGitRemoteFailure(failure.Result)
}

// ClassifyGitRemoteFailure diagnoses failed Git operations without exposing command output.
func ClassifyGitRemoteFailure(result process.Result) *GitRemoteFailure {
	output := strings.ToLower(result.Stderr + "\n" + result.Stdout)
	switch {
	case containsGitDiagnostic(output, "host key verification failed", "remote host identification has changed"):
		return &GitRemoteFailure{Kind: GitRemoteHostKey}
	case containsGitDiagnostic(output, "permission denied (", "authentication failed", "authorization failed", "http basic: access denied", "invalid username or token", "could not read username", "could not read password", "terminal prompts disabled", "cannot prompt because user interactivity", "returned error: 401", "returned error: 403", "access denied"):
		return &GitRemoteFailure{Kind: GitRemoteAuthentication}
	case strings.Contains(output, "connection refused"):
		failure := &GitRemoteFailure{Kind: GitRemotePortRefused}
		if match := refusedSSHPort.FindStringSubmatch(output); len(match) == 2 {
			failure.port = match[1]
		}
		return failure
	case strings.Contains(output, "connection closed"):
		failure := &GitRemoteFailure{Kind: GitRemoteClosed}
		if match := closedSSHPort.FindStringSubmatch(output); len(match) == 2 {
			failure.port = match[1]
		}
		return failure
	case containsGitDiagnostic(output, "timed out", "connection timeout", "operation timeout"):
		return &GitRemoteFailure{Kind: GitRemoteTimeout}
	case containsGitDiagnostic(output, "could not resolve", "couldn't resolve", "failed to connect", "connection reset", "network is unreachable", "no route to host", "tls handshake", "http 50", "returned error: 50", "remote end hung up"):
		return &GitRemoteFailure{Kind: GitRemoteUnavailable}
	case strings.Contains(output, "would clobber existing tag"):
		return &GitRemoteFailure{Kind: GitRemoteTagConflict}
	case containsGitDiagnostic(output, "[rejected]", "fetch first", "non-fast-forward"):
		return &GitRemoteFailure{Kind: GitRemotePushRejected}
	default:
		return &GitRemoteFailure{Kind: GitRemoteOther}
	}
}

func containsGitDiagnostic(output string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(output, fragment) {
			return true
		}
	}
	return false
}
