package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/domain"
)

type LocalDocumentsCheckout struct {
	Root, RemoteName, URL, Branch string
}

// InspectLocalDocumentsCheckout uses local Git metadata only. It accepts a
// real clone root and never initializes, reconfigures, or contacts the remote.
func (s *MaterialsGitSyncService) InspectLocalDocumentsCheckout(ctx context.Context, path string) (LocalDocumentsCheckout, error) {
	if path == "" || !filepath.IsAbs(path) {
		return LocalDocumentsCheckout{}, errors.New("请选择本地 Git 仓库的根目录")
	}
	root, err := filepath.EvalSymlinks(path)
	if err != nil {
		return LocalDocumentsCheckout{}, err
	}
	root = filepath.Clean(root)
	if filepath.Dir(root) == root {
		return LocalDocumentsCheckout{}, errors.New("请选择本地 Git 仓库的根目录")
	}
	scope := MaterialsGitScope{Root: root}
	local := *s
	local.Git = localDocumentsGitClient(s.Git)
	if err := local.verifyDocumentsCheckoutRoot(ctx, scope); err != nil {
		return LocalDocumentsCheckout{}, err
	}
	name, address, err := local.localDocumentsRemote(ctx, scope)
	if err != nil {
		return LocalDocumentsCheckout{}, err
	}
	branch, err := local.managedRead(ctx, scope, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return LocalDocumentsCheckout{}, errors.New("本地仓库处于分离 HEAD，请先切换到分支")
	}
	if _, err := local.managedRead(ctx, scope, "rev-parse", "--verify", "HEAD^{commit}"); err != nil {
		return LocalDocumentsCheckout{}, errors.New("本地仓库还没有检出的提交")
	}
	return LocalDocumentsCheckout{Root: root, RemoteName: name, URL: address, Branch: strings.TrimSpace(branch.Stdout)}, nil
}

func (s *MaterialsGitSyncService) verifyDocumentsCheckoutRoot(ctx context.Context, scope MaterialsGitScope) error {
	cloned, err := managedRepositoryDirectory(scope.Root)
	if err != nil {
		return err
	}
	if !cloned {
		return errors.New("请选择已克隆的独立 Git 仓库根目录")
	}
	for _, item := range []struct{ arg, expected string }{
		{"--show-toplevel", scope.Root},
		{"--absolute-git-dir", filepath.Join(scope.Root, ".git")},
		{"--git-common-dir", filepath.Join(scope.Root, ".git")},
	} {
		value, err := s.managedRead(ctx, scope, "rev-parse", "--path-format=absolute", item.arg)
		if err != nil || !samePath(strings.TrimSpace(value.Stdout), item.expected) {
			if ctx.Err() != nil || errors.Is(err, ErrMaterialsGitScopeChanged) {
				return err
			}
			return errors.New("本地 Git 元数据与所选仓库目录不一致")
		}
	}
	return nil
}

func (s *MaterialsGitSyncService) localDocumentsRemote(ctx context.Context, scope MaterialsGitScope) (string, string, error) {
	remotes, err := s.managedRead(ctx, scope, "remote")
	if err != nil {
		return "", "", err
	}
	name := strings.TrimRight(remotes.Stdout, "\r\n")
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "\r\n") || domain.ValidateGitRef(&name) != nil {
		return "", "", errors.New("资料仓库必须配置且仅配置一个 Git 远端")
	}
	readURL := func(args ...string) (string, error) {
		result, err := s.managedRead(ctx, scope, append(args, "--", name)...)
		if err != nil {
			return "", err
		}
		address := strings.TrimRight(result.Stdout, "\r\n")
		if address == "" || strings.ContainsAny(address, "\r\n") {
			return "", errors.New("Git 远端必须各配置一个拉取和推送地址")
		}
		return address, nil
	}
	url, err := readURL("remote", "get-url", "--all")
	if err != nil {
		return "", "", err
	}
	pushURL, err := readURL("remote", "get-url", "--push", "--all")
	if err != nil {
		return "", "", err
	}
	if url != pushURL {
		return "", "", errors.New("该远端的拉取和推送地址不同，请先在 Git 中将两者设为同一个地址")
	}
	if filepath.IsAbs(url) {
		url = filepath.Clean(url)
	}
	if err := domain.ValidateLocalDocumentsGitURL(url); err != nil {
		return "", "", err
	}
	return name, url, nil
}

func (s *MaterialsGitSyncService) verifyLocalDocumentsRemote(ctx context.Context, scope MaterialsGitScope) error {
	name, url, err := s.localDocumentsRemote(ctx, scope)
	if err != nil {
		return err
	}
	if scope.RemoteName != name || scope.URL != url {
		return errors.New("本地资料仓库的 Git 远端已变化，请重新选择仓库或恢复原远端")
	}
	return nil
}

// SameDocumentsCheckout also detects path aliases that resolve to the same
// physical directory on the current volume.
func SameDocumentsCheckout(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if samePath(a, b) {
		return true
	}
	left, leftErr := os.Stat(a)
	right, rightErr := os.Stat(b)
	return leftErr == nil && rightErr == nil && os.SameFile(left, right)
}

func localDocumentsGitClient(source *GitClient) *GitClient {
	local := *source
	local.RemoveEnvironment = append(append([]string(nil), local.RemoveEnvironment...),
		"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_SHALLOW_FILE", "GIT_GRAFT_FILE",
		"GIT_CONFIG", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS",
	)
	local.Environment = make(map[string]string, len(source.Environment)+1)
	for key, value := range source.Environment {
		local.Environment[key] = value
	}
	local.Environment["GIT_NO_REPLACE_OBJECTS"] = "1"
	return &local
}
