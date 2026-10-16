package workspace

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

func ParseRemoteBranchRef(value string) (domain.RemoteBranchRef, error) {
	normalized := strings.TrimSpace(value)
	separator := strings.IndexByte(normalized, '/')
	if separator <= 0 || separator >= len(normalized)-1 {
		return domain.RemoteBranchRef{}, fmt.Errorf("远程分支必须使用 <remote>/<branch> 格式，例如 origin/release/test")
	}
	ref := domain.RemoteBranchRef{Remote: normalized[:separator], Branch: normalized[separator+1:]}
	if err := validateRemoteBranchRef(ref); err != nil {
		return domain.RemoteBranchRef{}, err
	}
	return ref, nil
}

func validateRemoteBranchRef(r domain.RemoteBranchRef) error {
	if strings.TrimSpace(r.Remote) == "" || strings.Contains(r.Remote, "/") || r.Remote == "." || r.Remote == ".." {
		return fmt.Errorf("远程名称不合法：%s", r.Remote)
	}
	b := r.Branch
	if strings.TrimSpace(b) == "" || strings.HasPrefix(b, "/") || strings.HasSuffix(b, "/") {
		return fmt.Errorf("远程分支不能为空")
	}
	if b == "@" || strings.Contains(b, "..") || strings.Contains(b, "@{") {
		return fmt.Errorf("远程分支格式不合法：%s", b)
	}
	if strings.ContainsAny(b, "~^:?*[\\") {
		return fmt.Errorf("远程分支包含 Git 不允许的字符：%s", b)
	}
	for _, segment := range strings.Split(b, "/") {
		if segment == "" || strings.HasPrefix(segment, ".") || strings.HasSuffix(segment, ".") || strings.HasSuffix(strings.ToLower(segment), ".lock") {
			return fmt.Errorf("远程分支路径片段不合法：%s", b)
		}
		for _, r := range segment {
			if r <= 0x20 || r == 0x7f || unicodeSpace(r) {
				return fmt.Errorf("远程分支包含 Git 不允许的字符：%s", b)
			}
		}
	}
	return nil
}

func FormatRemoteBranchRef(r domain.RemoteBranchRef) string { return r.Remote + "/" + r.Branch }

type BranchReferenceValidator interface {
	IsValid(branch string) bool
}

type GitBranchReferenceValidator struct{ Git *GitClient }

func NewBranchReferenceValidator(git *GitClient) *GitBranchReferenceValidator {
	if git == nil {
		git = NewGitClient(nil, "")
	}
	return &GitBranchReferenceValidator{Git: git}
}

func (v *GitBranchReferenceValidator) IsValid(branch string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := v.Git.RunGlobalContext(ctx, 10*time.Second, false, true, "check-ref-format", "--branch", branch)
	return err == nil && result.ExitCode == 0
}

type RepositoryRemoteCatalog interface {
	List(repository string) ([]string, error)
}

type GitRepositoryRemoteCatalog struct{ Git *GitClient }

func NewRepositoryRemoteCatalog(git *GitClient) *GitRepositoryRemoteCatalog {
	if git == nil {
		git = NewGitClient(nil, "")
	}
	return &GitRepositoryRemoteCatalog{Git: git}
}

func (c *GitRepositoryRemoteCatalog) List(repository string) ([]string, error) {
	values, err := c.Git.RemoteNames(repository)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sortCaseInsensitive(result)
	if len(result) == 0 {
		return nil, fmt.Errorf("仓库没有可用的 Git 远程")
	}
	return result, nil
}

type RepositoryRemoteAddress struct {
	Remote string
	GitURL string
	Fetch  bool
	Push   bool
}

type RepositoryRemoteAddressStage string

const (
	RemoteAddressListStage  RepositoryRemoteAddressStage = "REMOTE_LIST"
	RemoteAddressFetchStage RepositoryRemoteAddressStage = "FETCH_ADDRESSES"
	RemoteAddressPushStage  RepositoryRemoteAddressStage = "PUSH_ADDRESSES"
)

type RepositoryRemoteAddressFailureKind string

const (
	RemoteAddressTimeout RepositoryRemoteAddressFailureKind = "TIMEOUT"
	RemoteAddressOther   RepositoryRemoteAddressFailureKind = "OTHER"
)

type RepositoryRemoteAddressFailure struct {
	Stage RepositoryRemoteAddressStage
	Kind  RepositoryRemoteAddressFailureKind
	cause error
}

func NewRepositoryRemoteAddressFailure(stage RepositoryRemoteAddressStage, kind RepositoryRemoteAddressFailureKind, cause error) RepositoryRemoteAddressFailure {
	if process.IsCleanupFailure(cause) {
		kind = RemoteAddressOther
	}
	return RepositoryRemoteAddressFailure{Stage: stage, Kind: kind, cause: cause}
}

func (f RepositoryRemoteAddressFailure) Unwrap() error { return f.cause }

func (f RepositoryRemoteAddressFailure) Error() string {
	label := map[RepositoryRemoteAddressStage]string{RemoteAddressListStage: "远程列表", RemoteAddressFetchStage: "拉取地址", RemoteAddressPushStage: "推送地址"}[f.Stage]
	if process.IsCleanupFailure(f.cause) {
		return label + "读取失败：命令进程树清理未确认，已停止后续 Git 操作"
	}
	if f.Kind == RemoteAddressTimeout {
		return label + "读取超时，请重试"
	}
	return label + "读取失败，请检查本地路径和 Git 配置"
}

type GitRepositoryRemoteAddressCatalog struct{ Git *GitClient }

func NewRepositoryRemoteAddressCatalog(git *GitClient) *GitRepositoryRemoteAddressCatalog {
	if git == nil {
		git = NewGitClient(nil, "")
	}
	return &GitRepositoryRemoteAddressCatalog{Git: git}
}

func (c *GitRepositoryRemoteAddressCatalog) List(repository string) ([]RepositoryRemoteAddress, error) {
	namesResult, err := c.read(repository, RemoteAddressListStage, "remote")
	if err != nil {
		return nil, err
	}
	names := nonEmptyLines(namesResult)
	sort.SliceStable(names, func(i, j int) bool {
		if names[i] == "origin" {
			return names[j] != "origin"
		}
		if names[j] == "origin" {
			return false
		}
		return names[i] < names[j]
	})
	var all []RepositoryRemoteAddress
	for _, remote := range names {
		fetchOut, err := c.read(repository, RemoteAddressFetchStage, "remote", "get-url", "--all", remote)
		if err != nil {
			return nil, err
		}
		pushOut, err := c.read(repository, RemoteAddressPushStage, "remote", "get-url", "--push", "--all", remote)
		if err != nil {
			return nil, err
		}
		fetch, push := nonEmptyLines(fetchOut), nonEmptyLines(pushOut)
		addresses := map[string]*RepositoryRemoteAddress{}
		for _, raw := range append(append([]string{}, fetch...), push...) {
			safe := SanitizeGitRemoteURL(raw)
			address := addresses[safe]
			if address == nil {
				address = &RepositoryRemoteAddress{Remote: remote, GitURL: safe}
				addresses[safe] = address
			}
			for _, item := range fetch {
				if item == raw {
					address.Fetch = true
					break
				}
			}
			for _, item := range push {
				if item == raw {
					address.Push = true
					break
				}
			}
		}
		for _, item := range addresses {
			all = append(all, *item)
		}
	}
	return all, nil
}

func (c *GitRepositoryRemoteAddressCatalog) read(repository string, stage RepositoryRemoteAddressStage, arguments ...string) (string, error) {
	result, err := c.Git.RunContext(context.Background(), repository, 15*time.Second, true, true, arguments...)
	return repositoryRemoteAddressResult(stage, result, err)
}

func repositoryRemoteAddressResult(stage RepositoryRemoteAddressStage, result process.Result, err error) (string, error) {
	if err == nil {
		return result.Stdout, nil
	}
	kind := RemoteAddressOther
	if !process.IsCleanupFailure(err) && (errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "deadline exceeded") || strings.Contains(strings.ToLower(err.Error()), "timed out")) {
		kind = RemoteAddressTimeout
	}
	return "", NewRepositoryRemoteAddressFailure(stage, kind, err)
}

// SanitizeGitRemoteURL removes credentials from network URLs while preserving
// punctuation that belongs to local paths or scp-style repository names.
func SanitizeGitRemoteURL(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\`) || strings.HasPrefix(value, ".") {
		return value
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err == nil && !strings.EqualFold(parsed.Scheme, "file") && parsed.Host != "" {
			parsed.User = nil
			parsed.RawQuery = ""
			parsed.Fragment = ""
			return parsed.String()
		}
		split := strings.SplitN(value, "://", 2)
		rest := split[1]
		authority, tail, _ := strings.Cut(rest, "/")
		authority = authority[strings.LastIndex(authority, "@")+1:]
		if strings.EqualFold(split[0]+"://", "file://") {
			return split[0] + "://" + authority + tail
		}
		tail = strings.SplitN(strings.SplitN(tail, "?", 2)[0], "#", 2)[0]
		if tail != "" {
			tail = "/" + tail
		}
		return split[0] + "://" + authority + tail
	}
	colon := strings.IndexByte(value, ':')
	if colon > 0 {
		host := value[:colon]
		if strings.Contains(host, "@") && !strings.ContainsAny(host, `/\`) {
			return host[strings.LastIndex(host, "@")+1:] + value[colon:]
		}
	}
	return value
}

var gitWebDotGitSuffix = regexp.MustCompile(`(?i)(?:\.|%2e)git$`)

func GitRemoteBrowserURL(raw string) string {
	safe := SanitizeGitRemoteURL(raw)
	webURL := func(scheme, host string, repository url.URL) string {
		path := strings.TrimRight(repository.EscapedPath(), "/")
		if host == "" || path == "" {
			return ""
		}
		path = gitWebDotGitSuffix.ReplaceAllString(path, "")
		decoded, err := url.PathUnescape(path)
		if err != nil {
			return ""
		}
		u := url.URL{Scheme: scheme, Host: host, Path: decoded, RawPath: path}
		return u.String()
	}
	if strings.Contains(safe, "://") {
		parsed, err := url.Parse(safe)
		if err != nil {
			return ""
		}
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https":
			return webURL(strings.ToLower(parsed.Scheme), parsed.Host, *parsed)
		case "ssh":
			return webURL("https", parsed.Hostname(), *parsed)
		default:
			return ""
		}
	}
	if regexp.MustCompile(`^[A-Za-z]:.*`).MatchString(safe) || strings.HasPrefix(safe, "/") || strings.HasPrefix(safe, ".") {
		return ""
	}
	match := regexp.MustCompile(`^([A-Za-z0-9.-]+):([^:].*)$`).FindStringSubmatch(safe)
	if match == nil {
		return ""
	}
	return webURL("https", match[1], url.URL{Path: "/" + strings.TrimLeft(match[2], "/")})
}

func unicodeSpace(r rune) bool {
	return r == '\u00a0' || r == '\u1680' || (r >= '\u2000' && r <= '\u200a') || r == '\u2028' || r == '\u2029' || r == '\u202f' || r == '\u205f' || r == '\u3000'
}

func sortCaseInsensitive(values []string) {
	sort.SliceStable(values, func(i, j int) bool {
		a, b := strings.ToLower(values[i]), strings.ToLower(values[j])
		if a == b {
			return values[i] < values[j]
		}
		return a < b
	})
}
