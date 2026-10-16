package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

func (state *compareState) buildCommitRange(ctx context.Context, head string, request CompareRequest, mainBranch WorkspaceMainBranch) error {
	start, end, err := state.resolveCommitRange(ctx, head, request)
	if err != nil {
		return err
	}
	parents, err := state.commitParents(ctx, start)
	if err != nil {
		return err
	}
	base := ""
	if len(parents) > 0 {
		base = parents[0]
	}
	if err := state.buildTreePair(ctx, base, end); err != nil {
		return err
	}
	state.baseCommit = base
	state.targetCommit = end
	state.baselineRef = "提交范围"
	state.notice = "比较起点提交的第一父版本与终点提交；区间内提交按 Git 祖先关系连续包含。"
	if request.SimulateOnMainline {
		return state.simulateCommitRangeOnMainline(ctx, mainBranch, start, end)
	}
	return nil
}

func (state *compareState) resolveCommitRange(ctx context.Context, head string, request CompareRequest) (string, string, error) {
	startRaw, endRaw := strings.TrimSpace(request.StartCommit), strings.TrimSpace(request.EndCommit)
	if startRaw != "" || endRaw != "" {
		if startRaw == "" || endRaw == "" {
			return "", "", errors.New("提交范围需要同时指定起点和终点")
		}
		start, err := state.resolveCommit(ctx, startRaw)
		if err != nil {
			return "", "", err
		}
		end, err := state.resolveCommit(ctx, endRaw)
		if err != nil {
			return "", "", err
		}
		if err := state.validateCommitOnHead(ctx, start, head); err != nil {
			return "", "", err
		}
		if err := state.validateCommitOnHead(ctx, end, head); err != nil {
			return "", "", err
		}
		ancestor, err := state.isAncestor(ctx, start, end)
		if err != nil {
			return "", "", err
		}
		if !ancestor {
			return "", "", fmt.Errorf("提交 %s 与 %s 没有祖先关系，无法组成连续范围", shortGitHash(start), shortGitHash(end))
		}
		return start, end, nil
	}

	if len(request.CommitHashes) == 0 {
		return "", "", errors.New("请至少选择一个提交")
	}
	commits := make([]string, 0, len(request.CommitHashes))
	seen := make(map[string]bool, len(request.CommitHashes))
	for _, raw := range request.CommitHashes {
		commit, err := state.resolveCommit(ctx, strings.TrimSpace(raw))
		if err != nil {
			return "", "", err
		}
		if seen[commit] {
			continue
		}
		if err := state.validateCommitOnHead(ctx, commit, head); err != nil {
			return "", "", err
		}
		seen[commit] = true
		commits = append(commits, commit)
	}
	if len(commits) == 0 {
		return "", "", errors.New("请至少选择一个有效提交")
	}
	start, end := commits[0], commits[0]
	for _, commit := range commits[1:] {
		beforeStart, err := state.isAncestor(ctx, commit, start)
		if err != nil {
			return "", "", err
		}
		afterStart, err := state.isAncestor(ctx, start, commit)
		if err != nil {
			return "", "", err
		}
		beforeEnd, err := state.isAncestor(ctx, commit, end)
		if err != nil {
			return "", "", err
		}
		afterEnd, err := state.isAncestor(ctx, end, commit)
		if err != nil {
			return "", "", err
		}
		if !beforeStart && !afterStart || !beforeEnd && !afterEnd {
			return "", "", fmt.Errorf("所选提交 %s 之间没有祖先关系，无法组成连续范围", shortGitHash(commit))
		}
		if beforeStart {
			start = commit
		}
		if afterEnd {
			end = commit
		}
	}
	return start, end, nil
}

func (state *compareState) resolveCommit(ctx context.Context, value string) (string, error) {
	if len(value) < 7 || len(value) > 64 || !gitObjectIDPattern.MatchString(value) {
		return "", fmt.Errorf("提交 SHA 格式不合法：%s", value)
	}
	resolved, err := state.service.run(ctx, state.root, nil, 2*1024*1024, false, "rev-parse", "--verify", "--quiet", value+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("读取所选提交 %s 失败：%w", value, err)
	}
	if resolved.ExitCode != 0 || resolved.StdoutTruncated {
		return "", fmt.Errorf("无法读取所选提交：%s", value)
	}
	commit := strings.TrimSpace(resolved.Stdout)
	if commit == "" {
		return "", fmt.Errorf("无法读取所选提交：%s", value)
	}
	return commit, nil
}

func (state *compareState) validateCommitOnHead(ctx context.Context, commit, head string) error {
	ancestor, err := state.isAncestor(ctx, commit, head)
	if err != nil {
		return err
	}
	if !ancestor {
		return fmt.Errorf("所选提交 %s 不属于当前 HEAD 的历史", shortGitHash(commit))
	}
	return nil
}

func (state *compareState) simulateCommitRangeOnMainline(ctx context.Context, mainBranch WorkspaceMainBranch, start, end string) error {
	mainline, err := state.localMainlineCommit(ctx, mainBranch)
	if err != nil {
		return err
	}
	if err := state.readTree(ctx, mainline, false); err != nil {
		return fmt.Errorf("读取本地主分支快照失败：%w", err)
	}
	mainlineTree, err := state.writeTree(ctx)
	if err != nil {
		return fmt.Errorf("读取本地主分支树失败：%w", err)
	}
	rangeBaseTree := state.baseTree
	patch, err := state.service.runInState(ctx, state, maxComparePatchOutput, false, nil,
		"diff", "--binary", "--full-index", "--find-renames", "--no-ext-diff", "--no-textconv", "--no-color",
		"--src-prefix=a/", "--dst-prefix=b/", rangeBaseTree, end, "--")
	if err != nil {
		return fmt.Errorf("读取提交范围净补丁失败：%w", err)
	}
	if patch.ExitCode != 0 || patch.StdoutTruncated || patch.StderrTruncated {
		return errors.New("提交范围净补丁超过安全上限或无法读取，未生成模拟结果")
	}
	state.baseCommit = mainline
	state.baseTree = mainlineTree
	state.baselineRef = firstNonEmpty(mainBranch.DisplayRef, mainBranch.LocalRef)
	if len(patch.Stdout) > 0 {
		applied, applyErr := state.service.runInState(ctx, state, 2*1024*1024, false, []byte(patch.Stdout),
			"apply", "--cached", "--binary", "--3way", "--whitespace=nowarn", "-")
		if applyErr != nil {
			return fmt.Errorf("提交范围无法模拟应用到主分支：%w", applyErr)
		}
		unmerged, unmergedErr := state.unmergedPaths(ctx)
		if unmergedErr != nil {
			return unmergedErr
		}
		if applied.ExitCode != 0 || len(unmerged) > 0 {
			paths := unmerged
			cause := "补丁无法应用"
			if len(paths) == 0 {
				paths = parseApplyFailurePaths(applied.Stderr + "\n" + applied.Stdout)
			}
			if len(paths) == 0 {
				parents, _ := state.commitParents(ctx, start)
				parent := ""
				if len(parents) > 0 {
					parent = parents[0]
				}
				paths, _ = state.commitChangedPaths(ctx, end, parent)
				cause = "补丁无法应用，以下为该范围涉及的文件"
			}
			sort.Strings(paths)
			return &GitCompareConflictError{Commit: end, Purpose: "无法模拟应用到主分支", Paths: paths, Cause: cause}
		}
	}
	targetTree, err := state.writeTree(ctx)
	if err != nil {
		return fmt.Errorf("生成模拟结果树失败：%w", err)
	}
	state.targetTree = targetTree
	state.targetCommit = "tree:" + targetTree
	state.simulateOnMainline = true
	state.notice = fmt.Sprintf("将提交范围 %s…%s 的净改动模拟应用到 %s。", shortGitHash(start), shortGitHash(end), state.baselineRef)
	return nil
}

var gitObjectIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]+$`)

func (state *compareState) isAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	result, err := state.service.run(ctx, state.root, nil, 2*1024*1024, false, "merge-base", "--is-ancestor", ancestor, descendant)
	if err != nil {
		return false, err
	}
	switch result.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, &GitException{Message: "检查提交祖先关系失败", Result: result}
	}
}

func (state *compareState) commitParents(ctx context.Context, commit string) ([]string, error) {
	result, err := state.service.run(ctx, state.root, nil, 2*1024*1024, true, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(result.Stdout)
	if len(fields) == 0 || fields[0] != commit {
		return nil, fmt.Errorf("无法读取提交父节点：%s", shortGitHash(commit))
	}
	return fields[1:], nil
}

func (state *compareState) commitChangedPaths(ctx context.Context, commit, parent string) ([]string, error) {
	args := []string{"diff-tree", "--no-commit-id", "--name-only", "-r", "-z", "--find-renames"}
	if parent == "" {
		args = append(args, "--root", commit)
	} else {
		args = append(args, parent, commit)
	}
	result, err := state.service.run(ctx, state.root, nil, maxCompareFilesOutput, true, args...)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0)
	for _, path := range bytes.Split([]byte(result.Stdout), []byte{0}) {
		if len(path) > 0 {
			paths = append(paths, string(path))
		}
	}
	return paths, nil
}

func (state *compareState) unmergedPaths(ctx context.Context) ([]string, error) {
	result, err := state.service.runInState(ctx, state, maxCompareFilesOutput, true, nil, "ls-files", "-u", "-z")
	if err != nil {
		return nil, fmt.Errorf("读取 Git 补丁冲突文件失败：%w", err)
	}
	set := map[string]bool{}
	for _, entry := range bytes.Split([]byte(result.Stdout), []byte{0}) {
		if tab := bytes.IndexByte(entry, '\t'); tab >= 0 && tab+1 < len(entry) {
			set[string(entry[tab+1:])] = true
		}
	}
	paths := make([]string, 0, len(set))
	for path := range set {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

var applyFailedPathPattern = regexp.MustCompile(`^error: patch failed: (.*):[0-9]+$`)

func parseApplyFailurePaths(output string) []string {
	paths := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if matches := applyFailedPathPattern.FindStringSubmatch(line); matches != nil {
			paths[matches[1]] = true
			continue
		}
		if strings.HasPrefix(line, "error: ") && strings.HasSuffix(line, ": patch does not apply") {
			path := strings.TrimSuffix(strings.TrimPrefix(line, "error: "), ": patch does not apply")
			if path != "" {
				paths[path] = true
			}
		}
	}
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}
