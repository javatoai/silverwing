package delivery

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"silverwing-go/internal/domain"
)

type tagWriteEndpoint struct {
	fetch, push string
	key         string
}

func (e tagWriteEndpoint) identity() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(e.fetch+"\x00"+e.push)))
}

func (s *Service) bindTagWriteEndpoint(ctx context.Context, repository, remote string, operation *domain.TagOperation) (tagWriteEndpoint, error) {
	endpoint, err := s.readTagWriteEndpoint(ctx, repository, remote)
	if err != nil {
		return endpoint, err
	}
	pin := domain.TagWriteEndpointPin{Identity: endpoint.identity(), Key: endpoint.key}
	if operation.WriteEndpoint != nil && *operation.WriteEndpoint != pin {
		return tagWriteEndpoint{}, changedTagWriteEndpoint()
	}
	operation.WriteEndpoint = &pin
	return endpoint, nil
}

func (s *Service) tagRemoteIdentity(ctx context.Context, repository, remote string) (key, identity string, err error) {
	endpoint, err := s.readTagWriteEndpoint(ctx, repository, remote)
	if err != nil {
		return "", "", err
	}
	// Preserve the hash stored by existing conflict operations. The legacy key
	// only groups work and must never be used to authorize a Git write.
	return tagRemoteKey(endpoint.fetch), endpoint.identity(), nil
}

func (s *Service) tagWriteEndpointKey(ctx context.Context, repository, remote string) (string, error) {
	endpoint, err := s.readTagWriteEndpoint(ctx, repository, remote)
	return endpoint.key, err
}

func (s *Service) readTagWriteEndpoint(ctx context.Context, repository, remote string) (tagWriteEndpoint, error) {
	fetch, err := s.effectiveTagRemoteURL(ctx, repository, "remote", "get-url", "--", remote)
	if err != nil {
		return tagWriteEndpoint{}, err
	}
	push, err := s.effectiveTagRemoteURL(ctx, repository, "remote", "get-url", "--push", "--all", "--", remote)
	if err != nil {
		return tagWriteEndpoint{}, err
	}
	key := "url\x00" + canonicalTagBatchURL(push)
	local, pushInfo := s.resolveLocalTagEndpoint(ctx, repository, push)
	if pushInfo != nil {
		key = "local\x00" + filepath.ToSlash(local)
	}
	if fetch != push {
		readLocal, fetchInfo := s.resolveLocalTagEndpoint(ctx, repository, fetch)
		if fetchInfo == nil || pushInfo == nil || !os.SameFile(fetchInfo, pushInfo) || !sameExistingTagDirectory(readLocal, local) {
			return tagWriteEndpoint{}, errors.New("Tag 操作要求读取和推送使用同一个远端仓库")
		}
	}
	return tagWriteEndpoint{fetch: fetch, push: push, key: fmt.Sprintf("%x", sha256.Sum256([]byte(key)))}, nil
}

// Fold only the host spelling and default port for HTTP(S) sequencing. The
// exact configured addresses still determine whether fetch and push match.
func canonicalTagBatchURL(endpoint string) string {
	scheme, rest, _ := strings.Cut(endpoint, "://")
	if scheme != "http" && scheme != "https" {
		return endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" {
		return endpoint
	}
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	start := strings.LastIndexByte(rest[:end], '@') + 1
	host := rest[start:end]
	if scheme == "https" && parsed.Port() == "443" {
		host = strings.TrimSuffix(host, ":443")
	} else if scheme == "http" && parsed.Port() == "80" {
		host = strings.TrimSuffix(host, ":80")
	}
	// A zone identifier is part of a local interface name and can be case sensitive.
	if !strings.Contains(host, "%") {
		host = strings.Map(func(r rune) rune {
			if 'A' <= r && r <= 'Z' {
				return r + ('a' - 'A')
			}
			return r
		}, host)
	}
	return scheme + "://" + rest[:start] + host + rest[end:]
}

func (s *Service) effectiveTagRemoteURL(ctx context.Context, repository string, args ...string) (string, error) {
	result, err := s.run(ctx, repository, 2*time.Minute, false, true, args...)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil || result.ExitCode != 0 || result.StdoutTruncated {
		return "", errors.New("无法读取 Tag 远端仓库地址")
	}
	value := strings.TrimSuffix(result.Stdout, "\n")
	if value == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("Tag 操作要求一个有效的读取地址和一个有效的推送地址")
	}
	return value, nil
}

func (s *Service) resolveLocalTagEndpoint(ctx context.Context, repository, endpoint string) (string, os.FileInfo) {
	path := localTagRemotePath(repository, endpoint)
	if path == "" {
		return "", nil
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil
	}
	resolvedInfo, err := os.Stat(resolved)
	if err != nil || !os.SameFile(info, resolvedInfo) {
		return "", nil
	}
	common, err := s.gitCommonDirectory(ctx, resolved)
	if err != nil {
		return "", nil
	}
	if !sameExistingTagDirectory(common, resolved) {
		top, err := s.gitTopLevel(ctx, resolved)
		if err != nil || !sameExistingTagDirectory(top, resolved) {
			return "", nil
		}
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return "", nil
	}
	return common, info
}

func localTagRemotePath(repository, endpoint string) string {
	if strings.HasPrefix(endpoint, "file:///") {
		parsed, err := url.Parse(endpoint)
		if err != nil || strings.ContainsAny(endpoint, "?#") {
			return ""
		}
		endpoint = parsed.Path
		if runtime.GOOS == "windows" && strings.HasPrefix(endpoint, "/") && filepath.VolumeName(endpoint[1:]) != "" {
			endpoint = endpoint[1:]
		}
		if !filepath.IsAbs(endpoint) {
			return ""
		}
	}
	if filepath.IsAbs(endpoint) {
		return filepath.FromSlash(endpoint)
	}
	if runtime.GOOS == "windows" && (strings.HasPrefix(endpoint, "/") || strings.HasPrefix(endpoint, `\`)) {
		return ""
	}
	if filepath.VolumeName(endpoint) != "" || strings.HasPrefix(endpoint, "~") || strings.Contains(endpoint, "://") {
		return ""
	}
	if colon := strings.IndexByte(endpoint, ':'); colon >= 0 && !strings.Contains(endpoint[:colon], "/") {
		return ""
	}
	// Keep dot segments until the filesystem resolves any intervening symlinks.
	return repository + string(filepath.Separator) + filepath.FromSlash(endpoint)
}

func sameExistingTagDirectory(first, second string) bool {
	a, err := os.Stat(first)
	if err != nil || !a.IsDir() {
		return false
	}
	b, err := os.Stat(second)
	return err == nil && b.IsDir() && os.SameFile(a, b)
}

type tagRemoteIdentityContextKey struct{}

type tagRemoteGuard struct{ identity, endpointKey string }

func withTagRemoteIdentity(ctx context.Context, identity, endpointKey string) context.Context {
	return context.WithValue(ctx, tagRemoteIdentityContextKey{}, tagRemoteGuard{identity, endpointKey})
}

func tagRemoteGuardFromContext(ctx context.Context) tagRemoteGuard {
	guard, _ := ctx.Value(tagRemoteIdentityContextKey{}).(tagRemoteGuard)
	return guard
}

func tagRemoteIdentityFromContext(ctx context.Context) string {
	return tagRemoteGuardFromContext(ctx).identity
}

func (s *Service) requireTagRemoteIdentity(ctx context.Context, repository, remote, expected string) error {
	endpoint, err := s.readTagWriteEndpoint(ctx, repository, remote)
	if err != nil {
		return err
	}
	guard := tagRemoteGuardFromContext(ctx)
	if endpoint.identity() != expected || (guard.endpointKey != "" && endpoint.key != guard.endpointKey) {
		return changedTagWriteEndpoint()
	}
	return nil
}

func changedTagWriteEndpoint() error {
	return errors.New("远端仓库配置已变化，已保留 Tag 操作现场")
}

func guardedTagGitFailure(ctx context.Context, action string, exitCode int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("%s（退出码 %d）", action, exitCode)
}

func (s *Service) fetchTagRemote(ctx context.Context, repository, remote string, options ...string) error {
	if remote == "" {
		remote = "origin"
	}
	args := append([]string{"fetch"}, options...)
	identity := tagRemoteIdentityFromContext(ctx)
	if identity != "" {
		if err := s.requireTagRemoteIdentity(ctx, repository, remote, identity); err != nil {
			return err
		}
		args = append(args, "--")
	}
	result, err := s.run(ctx, repository, 5*time.Minute, identity == "", false, append(args, remote)...)
	if identity != "" && (err != nil || result.ExitCode != 0) {
		return guardedTagGitFailure(ctx, "获取 Tag 远端引用失败", result.ExitCode)
	}
	return err
}

func (s *Service) pushPinnedTagBranch(ctx context.Context, repository, remote, identity, ref, before, after string) error {
	if !strings.HasPrefix(ref, "refs/heads/") || !validCommitSHA(after) || (before != "" && !validCommitSHA(before)) {
		return errors.New("Tag 操作的目标引用或提交无效")
	}
	if before != "" && before != after {
		fastForward, err := s.isAncestor(ctx, repository, before, after)
		if err != nil {
			return err
		}
		if !fastForward {
			return fmt.Errorf("远端分支 %s 无法从 %s 快进到 %s", ref, before, after)
		}
	}
	if err := s.requireTagRemoteIdentity(ctx, repository, remote, identity); err != nil {
		return err
	}
	var pushErr error
	if before != after {
		// The lease enforces the approved old value. The ancestry check above
		// prevents this invocation from authorizing a non-fast-forward update.
		push, err := s.run(ctx, repository, 10*time.Minute, false, false, "push", "--no-follow-tags", "--no-mirror",
			"--recurse-submodules=no", "--force-with-lease="+ref+":"+before, "--", remote, after+":"+ref)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || push.ExitCode != 0 {
			pushErr = fmt.Errorf("推送远端分支 %s 失败（退出码 %d）", ref, push.ExitCode)
		}
		if err := s.requireTagRemoteIdentity(ctx, repository, remote, identity); err != nil {
			return errors.Join(pushErr, err)
		}
	}
	actual, _, err := s.tagRemoteBranchTips(ctx, repository, remote, ref, "")
	if err != nil {
		return errors.Join(pushErr, err)
	}
	if actual != after {
		return errors.Join(pushErr, changedTagBaseline(remote+"/"+ref, after, actual))
	}
	return nil
}
