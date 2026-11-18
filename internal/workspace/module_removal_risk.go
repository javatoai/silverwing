package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"silverwing-go/internal/domain"
)

type moduleRemovalRisk struct {
	staged, unstaged, untracked bool
	files                       []string
	base, upstream              moduleCachedComparison
	fingerprint, admin          string
}

type moduleCachedComparison struct {
	commit        string
	ahead, behind int
	known         bool
}

func inspectModuleRemovalRisk(ctx context.Context, git *GitClient, ws domain.ServiceWorkspace, target, admin string) (moduleRemovalRisk, error) {
	risk := moduleRemovalRisk{files: []string{}, admin: admin}
	if err := requireModuleGitIdle(admin); err != nil {
		return risk, err
	}
	status, err := git.runReadBudget(ctx, target, 2*time.Minute, 16*1024*1024,
		"-c", "core.fsmonitor=false", "status", "--porcelain=v2", "--branch", "-z",
		"--no-ahead-behind", "--no-renames", "--ignore-submodules=none", "--untracked-files=all")
	if err != nil {
		return risk, err
	}
	branch, head := "", ""
	var changedPaths []string
	for _, line := range strings.Split(status.Stdout, "\x00") {
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.oid "):
			head = strings.TrimPrefix(line, "# branch.oid ")
		case strings.HasPrefix(line, "? "):
			path := strings.TrimPrefix(line, "? ")
			risk.untracked = true
			risk.files = append(risk.files, "?? "+path)
			changedPaths = append(changedPaths, path)
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "u "):
			fields := 8
			if line[0] == 'u' {
				fields = 10
			}
			change, ok := parsePorcelainFile(line, line[0], fields)
			if !ok || line[4] != ' ' {
				return risk, errors.New("无法读取模块的工作区状态")
			}
			risk.staged = risk.staged || line[2] != '.'
			risk.unstaged = risk.unstaged || line[3] != '.'
			risk.files = append(risk.files, strings.ReplaceAll(line[2:4], ".", " ")+" "+change.Path)
			changedPaths = append(changedPaths, change.Path)
		case strings.HasPrefix(line, "# "), line == "":
			// Other branch headers are included in the fingerprint.
		default:
			return risk, errors.New("无法读取模块的工作区状态")
		}
	}
	if branch == "" || branch == "(detached)" || !validGitObjectID(head) || zeroGitObjectID(head) {
		return risk, errors.New("工作区缺少有效分支或提交，无法安全回收")
	}
	if branch != ws.Branch {
		return risk, fmt.Errorf("当前分支与任务记录不一致：%s != %s", branch, ws.Branch)
	}
	baseRef, baseErr := ParseRemoteBranchRef(stringValue(ws.BaseRef))
	if baseErr == nil {
		risk.base, err = compareModuleCachedRef(ctx, git, target, "refs/remotes/"+FormatRemoteBranchRef(baseRef), head)
		if err != nil {
			return risk, err
		}
	}
	upstream, err := git.RunContext(ctx, target, 2*time.Minute, false, true,
		"rev-parse", "--symbolic-full-name", "--verify", "--quiet", "--end-of-options", "@{upstream}")
	if err != nil {
		return risk, err
	}
	upstreamRef := strings.TrimSpace(upstream.Stdout)
	if upstream.ExitCode == 0 && strings.HasPrefix(upstreamRef, "refs/remotes/") {
		risk.upstream, err = compareModuleCachedRef(ctx, git, target, upstreamRef, head)
		if err != nil {
			return risk, err
		}
	}
	digest := sha256.New()
	fmt.Fprintf(digest, "%q\x00%q\x00%+v\x00%+v\x00", status.Stdout, upstreamRef, risk.base, risk.upstream)
	if err := fingerprintModuleChangedPaths(ctx, target, changedPaths, digest); err != nil {
		return risk, err
	}
	if err := requireModuleGitIdle(admin); err != nil {
		return risk, err
	}
	risk.fingerprint = fmt.Sprintf("%x", digest.Sum(nil))
	return risk, ctx.Err()
}

// Counts come only from locally recorded remote-tracking references and their
// locally available objects. Missing refs or comparison objects are unknown.
func compareModuleCachedRef(ctx context.Context, git *GitClient, target, ref, head string) (moduleCachedComparison, error) {
	var comparison moduleCachedComparison
	resolved, err := git.RunContext(ctx, target, 2*time.Minute, false, true, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	if err != nil || resolved.ExitCode != 0 {
		return comparison, err
	}
	comparison.commit = strings.TrimSpace(resolved.Stdout)
	if !validGitObjectID(comparison.commit) {
		return comparison, nil
	}
	count, err := git.RunContext(ctx, target, 2*time.Minute, false, true, "rev-list", "--left-right", "--count", comparison.commit+"..."+head)
	if err != nil || count.ExitCode != 0 {
		return comparison, err
	}
	parts := strings.Fields(count.Stdout)
	if len(parts) == 2 {
		behind, behindErr := strconv.Atoi(parts[0])
		ahead, aheadErr := strconv.Atoi(parts[1])
		if behindErr == nil && aheadErr == nil && behind >= 0 && ahead >= 0 {
			comparison.ahead, comparison.behind, comparison.known = ahead, behind, true
		}
	}
	return comparison, nil
}

func fingerprintModuleChangedPaths(ctx context.Context, target string, paths []string, digest io.Writer) error {
	if len(paths) == 0 {
		return nil
	}
	root, err := os.OpenRoot(target)
	if err != nil {
		return err
	}
	defer root.Close()
	buffer := make([]byte, 64*1024)
	for _, name := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Clean(filepath.FromSlash(name))
		if !filepath.IsLocal(path) || path == "." {
			return fmt.Errorf("工作区文件路径超出模块范围：%q", name)
		}
		info, err := root.Lstat(path)
		if os.IsNotExist(err) {
			fmt.Fprintf(digest, "%q missing\x00", name)
			continue
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(digest, "%q %d %d %d\x00", name, info.Mode(), info.Size(), info.ModTime().UnixNano())
		switch {
		case info.Mode().IsRegular():
			sum, err := fingerprintModuleRegularFile(ctx, root, path, info, buffer)
			if err != nil {
				return err
			}
			fmt.Fprintf(digest, "%x\x00", sum)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := root.Readlink(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(digest, "%q\x00", link)
		}
		current, err := root.Lstat(path)
		if err != nil || !sameModuleFileState(info, current) {
			return errModuleRemovalChanged
		}
	}
	return nil
}

func fingerprintModuleRegularFile(ctx context.Context, root *os.Root, path string, expected os.FileInfo, buffer []byte) ([]byte, error) {
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	pinned, err := file.Stat()
	if err != nil || !sameModuleFileState(expected, pinned) {
		return nil, errModuleRemovalChanged
	}
	digest := sha256.New()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := file.Read(buffer)
		_, _ = digest.Write(buffer[:n])
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	current, err := file.Stat()
	if err != nil || !sameModuleFileState(pinned, current) {
		return nil, errModuleRemovalChanged
	}
	return digest.Sum(nil), nil
}

func sameModuleFileState(before, current os.FileInfo) bool {
	return os.SameFile(before, current) && before.Mode() == current.Mode() &&
		before.Size() == current.Size() && before.ModTime().Equal(current.ModTime())
}

func requireModuleGitIdle(admin string) error {
	for _, marker := range []string{
		"MERGE_HEAD", "rebase-merge", "rebase-apply", "CHERRY_PICK_HEAD", "REVERT_HEAD",
		"sequencer", "BISECT_LOG", "BISECT_START", "index.lock", "HEAD.lock",
	} {
		if _, err := os.Lstat(filepath.Join(admin, marker)); err == nil {
			return fmt.Errorf("存在进行中的 Git 操作（%s），请完成或中止后再回收", marker)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
